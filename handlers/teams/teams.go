package teams

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/buger/jsonparser"
	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/courier/utils"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

var sendBaseURL = "https://smba.trafficmanager.net/br"

func init() {
	courier.RegisterHandler(newHandler())
}

type handler struct {
	handlers.BaseHandler
}

func newHandler() courier.ChannelHandler {
	return &handler{handlers.NewBaseHandler(courier.ChannelType("TM"), "Teams")}
}

func (h *handler) Initialize(s courier.Server) error {
	h.SetServer(s)
	s.AddHandlerRoute(h, http.MethodPost, "receive", h.receiveEvent)
	return nil
}

func (h *handler) receiveEvent(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {
	payload := &mtPayload{}
	err := handlers.DecodeAndValidateJSON(payload, r)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	validationToken := channel.ConfigForKey(courier.ConfigAuthToken, "")
	tokenHeader := strings.Replace(r.Header.Get("authorization"), "Bearer ", "", 1)
	if validationToken != tokenHeader {
		w.WriteHeader(http.StatusForbidden)
		return nil, fmt.Errorf("Wrong validation token for channel: %s", channel.UUID())
	}

	return nil, nil
}

type mtPayload struct {
	Activity    Activity         `json:"activity"`
	TopicName   string           `json:"topicname,omitempty"`
	Bot         ChannelAccount   `json:"bot,omitempty"`
	Members     []ChannelAccount `json:"members,omitempty"`
	IsGroup     bool             `json:"isGroup,omitempty"`
	TenantId    string           `json:"tenantId,omitempty"`
	ChannelData ChannelData      `json:"channelData,omitempty"`
}

type ChannelData struct {
	AadObjectId string `json:"aadObjectId"`
	Tenant      struct {
		ID string `json:"id"`
	} `josn:"tenant"`
}

type ChannelAccount struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Role        string `json:"role"`
	AadObjectId string `json:"aadObjectId,omitempty"`
}

type ConversationAccount struct {
	ID               string `json:"id"`
	ConversationType string `json:"conversationType"`
	TenantID         string `json:"tenantId"`
	Role             string `json:"role"`
	Name             string `json:"name"`
	IsGroup          bool   `json:"isGroup"`
	AadObjectId      string `json:"aadObjectId"`
}

type Attachment struct {
	ContentType string `json:"contentType"`
	ContentUrl  string `json:"contentUrl"`
	Name        string `json:"name,omitempty"`
}

type ConversationReference struct {
	ActivityId   string              `json:"activityId"`
	Bot          ChannelAccount      `json:"bot"`
	ChannelId    string              `json:"channelId"`
	Conversation ConversationAccount `json:"conversation"`
	ServiceUrl   string              `json:"serviceUrl"`
	User         ChannelAccount      `json:"user"`
}

type Activity struct {
	Action           string                `json:"action,omitempty"`
	AttachmentLayout string                `json:"attachmentLayout,omitempty"`
	Attachments      []Attachment          `json:"attachments,omitempty"`
	CallerId         string                `json:"callerId,omitempty"`
	ChannelId        string                `json:"channelId,omitempty"`
	Code             string                `json:"code,omitempty"`
	Conversation     ConversationAccount   `json:"conversation,omitempty"`
	DeliveryMode     string                `json:"deliveryMode,omitempty"`
	Expiration       string                `json:"expiration,omitempty"`
	From             ChannelAccount        `json:"from,omitempty"`
	Id               string                `json:"id,omitempty"`
	Importance       string                `json:"importance,omitempty"` //values: low, normal, high
	InputHint        string                `json:"inputHint,omitempty"`  //values: acceptingInput, expectingInput, ignoringInput
	Locale           string                `json:"locale,omitempty"`     //default: en-US
	LocalTimestamp   string                `json:"localTimestamp,omitempty"`
	MembersAdded     []ChannelAccount      `json:"membersAdded,omitempty"`
	MembersRemoved   []ChannelAccount      `json:"membersRemoved,omitempty"`
	Name             string                `json:"name,omitempty"`
	Recipient        ChannelAccount        `json:"recipient,omitempty"`
	RelatesTo        ConversationReference `json:"realatesTo,omitempty"`
	ReplyToID        string                `json:"replyToId,omitempty"`
	ServiceUrl       string                `json:"serviceUrl,omitempty"`
	Text             string                `json:"text"`
	TextFormat       string                `json:"textFormat,omitempty"`
	Type             string                `json:"type"`
	Timestamp        string                `json:"timestamp,omitempty"`
	TopicName        string                `json:"topicName,omitempty"`
}

func (h *handler) SendMsg(ctx context.Context, msg courier.Msg) (courier.MsgStatus, error) {

	token := msg.Channel().StringConfigForKey(courier.ConfigAuthToken, "")
	if token == "" {
		return nil, fmt.Errorf("missing token for TM channel")
	}

	status := h.Backend().NewMsgStatusForID(msg.Channel(), msg.ID(), courier.MsgErrored)

	msgActivity := Activity{}
	payload := mtPayload{}

	msgURL, _ := url.Parse(sendBaseURL) //isso vai ter q mudar para pegar a serviceURL do contato junto com o ID da conversa e ID da atividade

	for _, attachment := range msg.Attachments() { //verificar envio de attachments
		attType, attURL := handlers.SplitAttachment(attachment)
		filename, err := utils.BasePathForURL(attURL)
		if err != nil {
			logrus.WithField("channel_uuid", msg.Channel().UUID().String()).WithError(err).Error("Error while parsing the media URL")
		}
		msgActivity.Attachments = append(msgActivity.Attachments, Attachment{attType, attURL, filename})
	}

	if msg.Text() != "" {
		msgActivity.Type = "message"
		msgActivity.Text = msg.Text()
		payload.Activity = msgActivity
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return status, err
	}

	req, err := http.NewRequest(http.MethodPost, msgURL.String(), bytes.NewReader(jsonBody))

	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	rr, err := utils.MakeHTTPRequest(req)

	// record our status and log
	log := courier.NewChannelLogFromRR("Message Sent", msg.Channel(), msg.ID(), rr).WithError("Message Send Error", err)
	status.AddLog(log)
	if err != nil {
		return status, nil
	}
	externalID, err := jsonparser.GetString(rr.Body, "id")
	if err != nil {
		log.WithError("Message Send Error", errors.Errorf("unable to get message_id from body"))
		return status, nil
	}
	status.SetExternalID(externalID)

	return status, nil
}
