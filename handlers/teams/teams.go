package teams

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
	jv                          = JwtTokenValidator{}
	AllowedSigningAlgorithms    = []string{"RS256", "RS384", "RS512"}
	ToBotFromChannelTokenIssuer = "https://api.botframework.com"
	jwks_uri                    = "https://login.botframework.com/v1/.well-known/keys"
)

const fetchTimeout = 20

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

type Keys struct {
	Keys struct {
		Kty          string   `json:"kty"`
		Kid          string   `json:"kid"`
		Endorsements []string `json:"endorsements"`
	}
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

func validateToken(channel courier.Channel, w http.ResponseWriter, r *http.Request) error {
	tokenH := r.Header.Get("Authorization")
	tokenHeader := strings.Replace(tokenH, "Bearer ", "", 1)
	getKey := func(token *jwt.Token) (interface{}, error) {
		// Get new JWKs if the cache is expired
		if jv.AuthCache.IsExpired() {

			ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout*time.Second)
			defer cancel()
			set, err := jwk.Fetch(ctx, jwks_uri)
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

	token, _ := jwt.Parse(tokenHeader, getKey)

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
		return fmt.Errorf("Unauthorized. Invalid signing algorithm")
	}

	issuer := token.Claims.(jwt.MapClaims)["iss"].(string)

	if issuer != ToBotFromChannelTokenIssuer {
		return fmt.Errorf("Unauthorized, invalid token issuer")
	}

	audience := token.Claims.(jwt.MapClaims)["aud"].(string)
	appID := channel.StringConfigForKey("appID", "")

	if audience != appID {
		return fmt.Errorf("Unauthorized: invalid AppId passed on token")
	}

	return nil
}

func (h *handler) receiveEvent(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {
	payload := &Activity{}
	err := handlers.DecodeAndValidateJSON(payload, r)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	err = validateToken(channel, w, r)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	serviceURL := payload.ServiceUrl
	var urn urns.URN

	// the list of events we deal with
	events := make([]courier.Event, 0, 2)

	// the list of data we will return in our response
	data := make([]interface{}, 0, 2)

	date, err := time.Parse(time.RFC3339, payload.Timestamp)
	if err != nil {
		return nil, err
	}

	if payload.Type == "message" {
		sender := payload.Conversation.ID

		urn, err = urns.NewTeamsURN(sender + ":serviceURL:" + serviceURL)
		if err != nil {
			return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
		}

		text := payload.Text
		attachmentURLs := make([]string, 0, 2)

		for _, att := range payload.Attachments {
			switch content := att.Content.(type) {
			case string:
				if strings.Contains(content, "Reply") {
					substrings := strings.Split(text, "\r\n\r\n")
					if len(substrings) > 1 {
						text = substrings[len(substrings)-1]
						text = strings.TrimRight(text, "\r\n")
					}
				}
			case map[string]interface{}:
				downloadURL, ok := content["downloadUrl"].(string)
				if ok && downloadURL != "" {
					attachmentURLs = append(attachmentURLs, downloadURL)
				}
			default:
			}
		}

		ev := h.Backend().NewIncomingMsg(channel, urn, text).WithExternalID(payload.Id).WithReceivedOn(date)
		event := h.Backend().CheckExternalIDSeen(ev)

		email, err := getContactEmail(channel, urn)
		if err != nil {
			logrus.WithField("channel_uuid", event.Channel().UUID().String()).WithError(err).Error("Error getting contact email")
		} else {
			ctEmail := struct {
				Email string `json:"email"`
			}{Email: email}

			md, err := json.Marshal(ctEmail)
			if err != nil {
				courier.LogRequestError(r, channel, err)
			}

			metadata := json.RawMessage(md)
			event.WithMetadata(metadata)
		}

		// add any attachment URL found
		for _, attURL := range attachmentURLs {
			event.WithAttachment(attURL)
		}

		err = h.Backend().WriteMsg(ctx, event)
		if err != nil {
			return nil, err
		}

		h.Backend().WriteExternalIDSeen(event)

		events = append(events, event)
		data = append(data, courier.NewMsgReceiveData(event))
	}

	if payload.Type == "conversationUpdate" {
		userID := payload.MembersAdded[0].ID

		if userID == "" {
			return nil, nil
		}

		act := Activity{}

		act.Text = "Create Conversation"
		act.Type = "message"

		bot := ChannelAccount{}

		bot.ID = channel.StringConfigForKey("botID", "")
		bot.Role = "bot"

		members := []ChannelAccount{}

		members = append(members, ChannelAccount{ID: userID, Role: payload.MembersAdded[0].Role})
		tenantID := channel.StringConfigForKey("tenantID", "")

		ConversationJson := &mtPayload{
			Activity: act,
			Bot:      bot,
			Members:  members,
			IsGroup:  false,
			TenantId: tenantID,
		}
		jsonBody, err := json.Marshal(ConversationJson)
		if err != nil {
			return nil, err
		}
		token := channel.StringConfigForKey(courier.ConfigAuthToken, "")
		req, err := http.NewRequest(http.MethodPost, serviceURL+"/v3/conversations", bytes.NewReader(jsonBody))

		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

		rr, err := utils.MakeHTTPRequest(req)
		if err != nil {
			return nil, err
		}

		var body ConversationAccount

		err = json.Unmarshal(rr.Body, &body)
		if err != nil {
			return nil, err
		}

		urn, err = urns.NewTeamsURN(body.ID + ":serviceURL:" + serviceURL)
		if err != nil {
			return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
		}

		event := h.Backend().NewChannelEvent(channel, courier.NewConversation, urn).WithOccurredOn(date)
		events = append(events, event)
		data = append(data, courier.NewEventReceiveData(event))
	}
	// Ignore activity of type messageReaction
	if payload.Type == "messageReaction" {
		data = append(data, courier.NewInfoData("ignoring messageReaction"))
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
	} `json:"tenant"`
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
	ContentType string      `json:"contentType"`
	ContentUrl  string      `json:"contentUrl"`
	Name        string      `json:"name,omitempty"`
	Content     interface{} `json:"content,omitempty"`
}

type CardAction struct {
	Title string `json:"title"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

type SuggestedActions struct {
	Actions []CardAction `json:"actions"`
	To      []string     `json:"to"`
}

type Activity struct {
	Action           string              `json:"action,omitempty"`
	Attachments      []Attachment        `json:"attachments,omitempty"`
	ChannelId        string              `json:"channelId,omitempty"`
	Conversation     ConversationAccount `json:"conversation,omitempty"`
	Id               string              `json:"id,omitempty"`
	MembersAdded     []ChannelAccount    `json:"membersAdded,omitempty"`
	Name             string              `json:"name,omitempty"`
	Recipient        ChannelAccount      `json:"recipient,omitempty"`
	ServiceUrl       string              `json:"serviceUrl,omitempty"`
	Text             string              `json:"text,omitempty"`
	Type             string              `json:"type"`
	Timestamp        string              `json:"timestamp,omitempty"`
	SuggestedActions SuggestedActions    `json:"suggestedActions,omitempty"`
}

func (h *handler) SendMsg(ctx context.Context, msg courier.Msg) (courier.MsgStatus, error) {

	token := msg.Channel().StringConfigForKey(courier.ConfigAuthToken, "")
	if token == "" {
		return nil, fmt.Errorf("missing token for TM channel")
	}

	status := h.Backend().NewMsgStatusForID(msg.Channel(), msg.ID(), courier.MsgErrored)

	payloadArray := []Activity{}

	path := strings.Split(msg.URN().Path(), ":")
	conversationID := path[1]

	msgURL := msg.URN().TeamsServiceURL() + "v3/conversations/a:" + conversationID + "/activities"

	for _, attachment := range msg.Attachments() {
		// Process each attachment separately
		mimeType, attURL := handlers.SplitAttachment(attachment)
		attType := strings.Split(mimeType, "/")[0]
		//filename, err := utils.BasePathForURL(attURL)
		// if err != nil {
		// 	logrus.WithField("channel_uuid", msg.Channel().UUID().String()).WithError(err).Error("Error while parsing the media URL")
		// }

		if attType == "application" {
			attType = "document"
		}

		// Create a new payload for each attachment
		attPayload := Activity{Type: "message"}
		if attType == "video" || attType == "document" || attType == "audio" || attType == "image" {
			attPayload.Text = attURL
		}
		// make code snippet unusable while image upload is not working
		/*else {
			attPayload.Attachments = append(attPayload.Attachments, Attachment{mimeType, attURL, filename, struct {
				DownloadUrl string "json:\"downloadUrl,omitempty\""
				UniqueId    string "json:\"uniqueId,omitempty\""
				FileType    string "json:\"fileType,omitempty\""
			}{}})
		}*/

		payloadArray = append(payloadArray, attPayload)
	}

	textPayload := Activity{Type: "message"}
	if msg.Text() != "" {
		textPayload.Text = msg.Text()
	}

	for _, qr := range msg.QuickReplies() {

		ca := CardAction{
			Title: qr,
			Type:  "imBack",
			Value: qr,
		}

		textPayload.SuggestedActions.Actions = append(textPayload.SuggestedActions.Actions, ca)
		textPayload.SuggestedActions.To = append(textPayload.SuggestedActions.To, conversationID)
	}

	payloadArray = append(payloadArray, textPayload)

	for _, payload := range payloadArray {
		jsonBody, err := json.Marshal(payload)
		if err != nil {
			return status, err
		}

		req, err := http.NewRequest(http.MethodPost, msgURL, bytes.NewReader(jsonBody))

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
			return status, err
		}
		status.SetStatus(courier.MsgWired)
		externalID, err := jsonparser.GetString(rr.Body, "id")
		if err != nil {
			log.WithError("Message Send Error", errors.Errorf("unable to get message_id from body"))
			return status, nil
		}
		status.SetExternalID(externalID)
	}

	return status, nil
}

func (h *handler) DescribeURN(ctx context.Context, channel courier.Channel, urn urns.URN) (map[string]string, error) {

	accessToken := channel.StringConfigForKey(courier.ConfigAuthToken, "")
	if accessToken == "" {
		return nil, fmt.Errorf("missing access token")
	}

	// build a request to lookup the stats for this contact
	pathSplit := strings.Split(urn.Path(), ":")
	conversationID := pathSplit[1]
	url := urn.TeamsServiceURL() + "v3/conversations/a:" + conversationID + "/members"

	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	rr, err := utils.MakeHTTPRequest(req)
	if err != nil {
		return nil, fmt.Errorf("unable to look up contact data:%s\n%s", err, rr.Response)
	}

	// read our first and last name
	givenName, _ := jsonparser.GetString(rr.Body, "[0]", "givenName")
	surname, _ := jsonparser.GetString(rr.Body, "[0]", "surname")

	return map[string]string{"name": utils.JoinNonEmpty(" ", givenName, surname)}, nil
}

func getContactEmail(channel courier.Channel, urn urns.URN) (string, error) {
	accessToken := channel.StringConfigForKey(courier.ConfigAuthToken, "")
	if accessToken == "" {
		return "", fmt.Errorf("missing access token")
	}

	// build a request to lookup the stats for this contact
	pathSplit := strings.Split(urn.Path(), ":")
	conversationID := pathSplit[1]
	url := urn.TeamsServiceURL() + "/v3/conversations/a:" + conversationID + "/members"

	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	rr, err := utils.MakeHTTPRequest(req)
	if err != nil {
		return "", fmt.Errorf("unable to look up contact data:%s\n%s", err, rr.Response)
	}

	//read our contact email
	contactEmail, _ := jsonparser.GetString(rr.Body, "[0]", "email")

	return contactEmail, nil

}
