package teams

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/buger/jsonparser"
	"github.com/golang-jwt/jwt/v4"
	"github.com/lestrrat-go/jwx/jwk"
	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/courier/utils"
	"github.com/nyaruka/gocommon/urns"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

var (
	jv                       = JwtTokenValidator{}
	AllowedSigningAlgorithms = []string{"RS256", "RS384", "RS512"}
)

const (
	fetchTimeout                = 20
	ToBotFromChannelTokenIssuer = "https://api.botframework.com"
	sendBaseURL                 = "https://smba.trafficmanager.net/br"
	metadataURL                 = "https://login.botframework.com/v1/.well-known/openidconfiguration"
)

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

type metadata struct {
	JwksURI string `json:"jwks_uri"`
}

func getJwkURL(metadataURL string) (string, error) {

	response, err := http.NewRequest(http.MethodGet, metadataURL, nil)
	if err != nil {
		return "", fmt.Errorf("Error getting metadata document")
	}

	data := metadata{}
	err = json.NewDecoder(response.Body).Decode(&data)
	return data.JwksURI, err
}

// AuthCache is a general purpose cache
type AuthCache struct {
	Keys   interface{}
	Expiry time.Time
}

// JwtTokenValidator is the default implementation of TokenValidator.
type JwtTokenValidator struct {
	AuthCache
}

// IsExpired checks if the Keys have expired.
// Compares Expiry time with current time.
func (cache *AuthCache) IsExpired() bool {

	if diff := time.Now().Sub(cache.Expiry).Hours(); diff > 0 {
		return true
	}
	return false
}

func (h *handler) receiveEvent(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {
	payload := &mtPayload{}
	err := handlers.DecodeAndValidateJSON(payload, r)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	validationToken := channel.StringConfigForKey(courier.ConfigAuthToken, "")
	tokenHeader := strings.Replace(r.Header.Get("authorization"), "Bearer ", "", 1)
	if validationToken != tokenHeader {
		w.WriteHeader(http.StatusForbidden)
		return nil, fmt.Errorf("Wrong validation token for channel: %s", channel.UUID())
	}

	getKey := func(token *jwt.Token) (interface{}, error) {

		jwksURL, err := getJwkURL(metadataURL)
		if err != nil {
			return nil, err
		}

		// Get new JWKs if the cache is expired
		if jv.AuthCache.IsExpired() {
			ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout*time.Second)
			defer cancel()
			set, err := jwk.Fetch(ctx, jwksURL)
			if err != nil {
				return nil, err
			}
			// Update the cache
			// The expiry time is set to be of 5 days
			jv.AuthCache = AuthCache{
				Keys:   set,
				Expiry: time.Now().Add(time.Hour * 24 * 5),
			}
		}

		keyID, ok := token.Header["kid"].(string)
		if !ok {
			return nil, fmt.Errorf("Expecting JWT header to have string kid")
		}

		// Return cached JWKs
		key, ok := jv.AuthCache.Keys.(jwk.Set).LookupKeyID(keyID)
		if ok {
			var rawKey interface{}
			err := key.Raw(&rawKey)
			if err != nil {
				return nil, err
			}
			return rawKey, nil
		}

		return nil, fmt.Errorf("Could not find public key")
	}

	// TODO: Add options verify_aud and verify_exp
	token, err := jwt.Parse(tokenHeader, getKey)
	if err != nil {
		return nil, err
	}

	// Check allowed signing algorithms
	alg := token.Header["alg"]
	isAllowed := func() bool {
		for _, allowed := range AllowedSigningAlgorithms {
			if allowed == alg {
				return true
			}
		}
		return false
	}()

	if !isAllowed {
		return nil, fmt.Errorf("Unauthorized. Invalid signing algorithm")
	}

	serviceURL := token.Claims.(jwt.MapClaims)["serviceurl"].(string)

	if payload.Members[0].ID != channel.Address() || channel.StringConfigForKey("serviceURL", "") != serviceURL {
		return nil, fmt.Errorf("Unauthorized, service_url claim is invalid")
	}

	issuer := token.Claims.(jwt.MapClaims)["iss"].(string)

	if issuer != ToBotFromChannelTokenIssuer {
		return nil, fmt.Errorf("Unauthorized, invalid token issuer")
	}

	audience := token.Claims.(jwt.MapClaims)["aud"].(string)
	appID := channel.StringConfigForKey("appID", "")

	if audience != appID {
		return nil, fmt.Errorf("Unauthorized: invalid AppId passed on token")
	}

	// the list of events we deal with
	events := make([]courier.Event, 0, 2)

	// the list of data we will return in our response
	data := make([]interface{}, 0, 2)

	var urn urns.URN

	// create our date from the timestamp (they give us millis, arg is nanos)
	date := time.Unix(0, payload.Activity.Timestamp*1000000).UTC() //fzr conversão de string para time

	if payload.Activity.Type == "message" {

		sender := payload.Activity.Conversation.ID
		urn, err = urns.NewTeamsURN(sender) //criar urn teams
		if err != nil {
			return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
		}

		text := payload.Activity.Text
		attachmentURLs := make([]string, 0, 2)

		for _, att := range payload.Activity.Attachments {
			if att.ContentType != "" && att.ContentUrl != "" {
				attachmentURLs = append(attachmentURLs, att.ContentUrl)
			}
		}

		ev := h.Backend().NewIncomingMsg(channel, urn, text).WithExternalID(payload.Activity.Id).WithReceivedOn(date)
		event := h.Backend().CheckExternalIDSeen(ev)

		// add any attachment URL found
		for _, attURL := range attachmentURLs {
			event.WithAttachment(attURL)
		}

		err := h.Backend().WriteMsg(ctx, event)
		if err != nil {
			return nil, err
		}

		h.Backend().WriteExternalIDSeen(event)

		events = append(events, event)
		data = append(data, courier.NewMsgReceiveData(event))
	}

	if payload.Activity.Type == "conversationUpdate" {

	}

	if payload.Activity.Type == "messageReaction" {

	}

	return events, courier.WriteDataResponse(ctx, w, http.StatusOK, "Events Handled", data)
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
