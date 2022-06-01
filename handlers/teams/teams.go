package teams

import (
	"context"
	"net/http"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/handlers"
)

var apiURL = "https://smba.trafficmanager.net/"

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
	s.AddHandlerRoute(h, http.MethodPost, "receive", h.receiveMessage)
	return nil
}

func (h *handler) receiveMessage(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {
	return nil, nil
}

type Activity struct {
	Action           string `json:"action"`
	AttachmentLayout string `json:"attachmentLayout"`
	Attachments      []struct {
		// 	Content struct { //ver como funciona rich card
		// 	} `json:"content, omitempty"`
		ContentType  string `json:"contentType"`
		ContentUrl   string `json:"contentUrl"`
		Name         string `json:"name"`
		ThumbnailUrl string `json:"thumbnailUrl"`
	} `json:"attachments"`
	CallerId     string `json:"callerId,omitempty"`
	ChannelId    string `json:"channelId"`
	Code         string `json:"code"`
	Conversation struct {
		ID               string `json:"id"`
		ConversationType string `json:"conversationType"`
		TenantID         string `json:"tenantId"`
		Role             string `json:"role"`
		Name             string `json:"name"`
		IsGroup          bool   `json:"isGroup"`
		AadObjectId      string `json:"aadObjectId"`
	} `json:"conversation"`
	DeliveryMode string `json:"deliveryMode"`
	Expiration   string `json:"expiration"`
	From         struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Role        string `json:"role"`
		AadObjectId string `json:"aadObjectId"`
	} `json:"from"`
	Id             string `json:"id"`
	Importance     string `json:"importance"` //values: low, normal, high
	InputHint      string `json:"inputHint"`  //values: acceptingInput, expectingInput, ignoringInput
	Locale         string `json:"locale"`     //default: en-US
	LocalTimestamp string `json:"localTimestamp"`
	MembersAdded   []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Role        string `json:"role"`
		AadObjectId string `json:"aadObjectId"`
	} `json:"membersAdded"`
	MembersRemoved []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Role        string `json:"role"`
		AadObjectId string `json:"aadObjectId"`
	} `json:"membersRemoved"`
	Name      string `json:"name"`
	Recipient struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Role        string `json:"role"`
		AadObjectId string `json:"aadObjectId"`
	} `json:"recipient"`
	RelatesTo struct {
		ActivityId string `json:"activityId"`
		Bot        struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Role        string `json:"role"`
			AadObjectId string `json:"aadObjectId"`
		} `json:"bot"`
		ChannelId    string `json:"channelId"`
		Conversation struct {
			ID               string `json:"id"`
			ConversationType string `json:"conversationType"`
			TenantID         string `json:"tenantId"`
			Role             string `json:"role"`
			Name             string `json:"name"`
			IsGroup          bool   `json:"isGroup"`
			AadObjectId      string `json:"aadObjectId"`
		} `json:"conversation"`
		ServiceUrl string `json:"serviceUrl"`
		User       struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Role        string `json:"role"`
			AadObjectId string `json:"aadObjectId"`
		} `json:"user"`
	} `json:"realatesTo"`
	ReplyToID  string `json:"replyToId"`
	ServiceUrl string `json:"serviceUrl"`
	Text       string `json:"text"`
	TextFormat string `json:"textFormat"`
	Type       string `json:"type"`
	Timestamp  string `json:"timestamp"`
	TopicName  string `json:"topicName"`
}

func (h *handler) SendMsg(ctx context.Context, msg courier.Msg) (courier.MsgStatus, error) {

	return nil, nil
}
