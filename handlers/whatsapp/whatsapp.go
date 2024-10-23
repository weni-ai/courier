package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/buger/jsonparser"
	"github.com/gabriel-vasile/mimetype"
	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/backends/rapidpro"
	"github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/courier/utils"
	"github.com/nyaruka/gocommon/rcache"
	"github.com/nyaruka/gocommon/urns"
	"github.com/patrickmn/go-cache"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/mod/semver"
)

const (
	configNamespace  = "fb_namespace"
	configHSMSupport = "hsm_support"

	d3AuthorizationKey = "D360-API-KEY"

	channelTypeWa  = "WA"
	channelTypeD3  = "D3"
	channelTypeTXW = "TXW"

	mediaCacheKeyPattern = "whatsapp_media_%s"

	interactiveMsgMinSupVersion = "v2.35.2"
)

const (
	InteractiveProductSingleType         = "product"
	InteractiveProductListType           = "product_list"
	InteractiveProductCatalogType        = "catalog_product"
	InteractiveProductCatalogMessageType = "catalog_message"
)

var (
	retryParam = ""
)

var failedMediaCache *cache.Cache

func init() {
	courier.RegisterHandler(newWAHandler(courier.ChannelType(channelTypeWa), "WhatsApp"))
	courier.RegisterHandler(newWAHandler(courier.ChannelType(channelTypeD3), "360Dialog"))
	courier.RegisterHandler(newWAHandler(courier.ChannelType(channelTypeTXW), "TextIt"))

	failedMediaCache = cache.New(15*time.Minute, 15*time.Minute)
}

type handler struct {
	handlers.BaseHandler
}

func newWAHandler(channelType courier.ChannelType, name string) courier.ChannelHandler {
	return &handler{handlers.NewBaseHandler(channelType, name)}
}

// Initialize is called by the engine once everything is loaded
func (h *handler) Initialize(s courier.Server) error {
	h.SetServer(s)
	s.AddHandlerRoute(h, http.MethodPost, "receive", h.receiveEvent)
	return nil
}

//	{
//	  "statuses": [{
//	    "id": "9712A34B4A8B6AD50F",
//	    "recipient_id": "16315555555",
//	    "status": "sent",
//	    "timestamp": "1518694700"
//	  }],
//	  "messages": [ {
//	    "from": "16315555555",
//	    "id": "3AF99CB6BE490DCAF641",
//	    "timestamp": "1518694235",
//	    "text": {
//	      "body": "Hello this is an answer"
//	    },
//	    "type": "text"
//	  }]
//	}
type eventPayload struct {
	Contacts []struct {
		Profile struct {
			Name string `json:"name"`
		} `json:"profile"`
		WaID string `json:"wa_id"`
	} `json:"contacts"`
	Messages []struct {
		From      string `json:"from"      validate:"required"`
		ID        string `json:"id"        validate:"required"`
		Timestamp string `json:"timestamp" validate:"required"`
		Type      string `json:"type"      validate:"required"`
		Text      struct {
			Body string `json:"body"`
		} `json:"text"`
		Audio *struct {
			File     string `json:"file"      validate:"required"`
			ID       string `json:"id"        validate:"required"`
			Link     string `json:"link"`
			MimeType string `json:"mime_type" validate:"required"`
			Sha256   string `json:"sha256"    validate:"required"`
		} `json:"audio"`
		Button *struct {
			Payload string `json:"payload"`
			Text    string `json:"text"    validate:"required"`
		} `json:"button"`
		Document *struct {
			File     string `json:"file"      validate:"required"`
			ID       string `json:"id"        validate:"required"`
			Link     string `json:"link"`
			MimeType string `json:"mime_type" validate:"required"`
			Sha256   string `json:"sha256"    validate:"required"`
			Caption  string `json:"caption"`
			Filename string `json:"filename"`
		} `json:"document"`
		Image *struct {
			File     string `json:"file"      validate:"required"`
			ID       string `json:"id"        validate:"required"`
			Link     string `json:"link"`
			MimeType string `json:"mime_type" validate:"required"`
			Sha256   string `json:"sha256"    validate:"required"`
			Caption  string `json:"caption"`
		} `json:"image"`
		Sticker *struct {
			Animated bool   `json:"animated"`
			ID       string `json:"id"`
			Mimetype string `json:"mime_type"`
			SHA256   string `json:"sha256"`
		}
		Interactive *struct {
			ButtonReply *struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"button_reply"`
			ListReply *struct {
				ID          string `json:"id"`
				Title       string `json:"title"`
				Description string `json:"description"`
			} `json:"list_reply"`
			Type string `json:"type"`
		}
		Location *struct {
			Address   string  `json:"address"   validate:"required"`
			Latitude  float32 `json:"latitude"  validate:"required"`
			Longitude float32 `json:"longitude" validate:"required"`
			Name      string  `json:"name"      validate:"required"`
			URL       string  `json:"url"       validate:"required"`
		} `json:"location"`
		Video *struct {
			File     string `json:"file"      validate:"required"`
			ID       string `json:"id"        validate:"required"`
			Link     string `json:"link"`
			MimeType string `json:"mime_type" validate:"required"`
			Sha256   string `json:"sha256"    validate:"required"`
		} `json:"video"`
		Voice *struct {
			File     string `json:"file"      validate:"required"`
			ID       string `json:"id"        validate:"required"`
			Link     string `json:"link"`
			MimeType string `json:"mime_type" validate:"required"`
			Sha256   string `json:"sha256"    validate:"required"`
		} `json:"voice"`
		Contacts []struct {
			Phones []struct {
				Phone string `json:"phone"`
			} `json:"phones"`
		} `json:"contacts"`
		Referral struct {
			Headline   string `json:"headline"`
			Body       string `json:"body"`
			SourceType string `json:"source_type"`
			SourceID   string `json:"source_id"`
			SourceURL  string `json:"source_url"`
			Image      *struct {
				File     string `json:"file"      validate:"required"`
				ID       string `json:"id"        validate:"required"`
				Link     string `json:"link"`
				MimeType string `json:"mime_type" validate:"required"`
				Sha256   string `json:"sha256"    validate:"required"`
			} `json:"image"`
			Video *struct {
				File     string `json:"file"      validate:"required"`
				ID       string `json:"id"        validate:"required"`
				Link     string `json:"link"`
				MimeType string `json:"mime_type" validate:"required"`
				Sha256   string `json:"sha256"    validate:"required"`
			} `json:"video"`
		} `json:"referral"`
		Order struct {
			CatalogID    string `json:"catalog_id"`
			Text         string `json:"text"`
			ProductItems []struct {
				ProductRetailerID string  `json:"product_retailer_id"`
				Quantity          int     `json:"quantity"`
				ItemPrice         float64 `json:"item_price"`
				Currency          string  `json:"currency"`
			} `json:"product_items"`
		} `json:"order"`
	} `json:"messages"`
	Statuses []struct {
		ID          string `json:"id"           validate:"required"`
		RecipientID string `json:"recipient_id" validate:"required"`
		Timestamp   string `json:"timestamp"    validate:"required"`
		Status      string `json:"status"       validate:"required"`
	} `json:"statuses"`
}

// checkBlockedContact is a function to verify if the contact from msg has status blocked to return an error or not if it is active
func checkBlockedContact(payload *eventPayload, ctx context.Context, channel courier.Channel, h *handler) error {
	if len(payload.Contacts) > 0 {
		if contactURN, err := urns.NewWhatsAppURN(payload.Contacts[0].WaID); err == nil {
			if contact, err := h.Backend().GetContact(ctx, channel, contactURN, channel.StringConfigForKey(courier.ConfigAuthToken, ""), payload.Contacts[0].Profile.Name); err == nil {
				c, err := json.Marshal(contact)
				if err != nil {
					return err
				}
				var dbc rapidpro.DBContact
				if err = json.Unmarshal(c, &dbc); err != nil {
					return err
				}
				if dbc.Status_ == "B" {
					return errors.New("blocked contact sending message")
				}
			}
		}
	}
	return nil
}

// receiveMessage is our HTTP handler function for incoming messages
func (h *handler) receiveEvent(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {
	payload := &eventPayload{}

	err := handlers.DecodeAndValidateJSON(payload, r)
	if err != nil {
		if r.ContentLength > 999999 {
			return nil, errors.New("too large body")
		}
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	err = checkBlockedContact(payload, ctx, channel, h)
	if err != nil {
		return nil, err
	}
	// the list of events we deal with
	events := make([]courier.Event, 0, 2)

	// the list of data we will return in our response
	data := make([]interface{}, 0, 2)

	var contactNames = make(map[string]string)
	for _, contact := range payload.Contacts {
		contactNames[contact.WaID] = contact.Profile.Name
	}

	// first deal with any received messages
	for _, msg := range payload.Messages {
		// create our date from the timestamp
		ts, err := strconv.ParseInt(msg.Timestamp, 10, 64)
		if err != nil {
			return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, fmt.Errorf("invalid timestamp: %s", msg.Timestamp))
		}
		date := time.Unix(ts, 0).UTC()

		// create our URN
		urn, err := urns.NewWhatsAppURN(msg.From)
		if err != nil {
			return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
		}

		text := ""
		mediaURL := ""

		if msg.Type == "text" {
			text = msg.Text.Body
		} else if msg.Type == "audio" && msg.Audio != nil {
			mediaURL, err = resolveMediaURL(channel, msg.Audio.ID)
		} else if msg.Type == "button" && msg.Button != nil {
			text = msg.Button.Text
		} else if msg.Type == "document" && msg.Document != nil {
			text = msg.Document.Caption
			mediaURL, err = resolveMediaURL(channel, msg.Document.ID)
		} else if msg.Type == "image" && msg.Image != nil {
			text = msg.Image.Caption
			mediaURL, err = resolveMediaURL(channel, msg.Image.ID)
		} else if msg.Type == "sticker" && msg.Sticker != nil {
			mediaURL, err = resolveMediaURL(channel, msg.Sticker.ID)
		} else if msg.Type == "interactive" {
			if msg.Interactive.Type == "button_reply" {
				text = msg.Interactive.ButtonReply.Title
			} else {
				text = msg.Interactive.ListReply.Title
			}
		} else if msg.Type == "location" && msg.Location != nil {
			mediaURL = fmt.Sprintf("geo:%f,%f", msg.Location.Latitude, msg.Location.Longitude)
		} else if msg.Type == "video" && msg.Video != nil {
			mediaURL, err = resolveMediaURL(channel, msg.Video.ID)
		} else if msg.Type == "voice" && msg.Voice != nil {
			mediaURL, err = resolveMediaURL(channel, msg.Voice.ID)
		} else if msg.Type == "order" {
			text = msg.Order.Text
		} else if msg.Type == "contacts" {
			if len(msg.Contacts) == 0 {
				return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, errors.New("no shared contact"))
			}

			// put phones in a comma-separated string
			var phones []string
			for _, phone := range msg.Contacts[0].Phones {
				phones = append(phones, phone.Phone)
			}
			text = strings.Join(phones, ", ")
		} else {
			// we received a message type we do not support.
			courier.LogRequestError(r, channel, fmt.Errorf("unsupported message type %s", msg.Type))
		}

		// create our message
		ev := h.Backend().NewIncomingMsg(channel, urn, text).WithReceivedOn(date).WithExternalID(msg.ID).WithContactName(contactNames[msg.From])
		event := h.Backend().CheckExternalIDSeen(ev)

		// we had an error downloading media
		if err != nil {
			courier.LogRequestError(r, channel, err)
		}

		if msg.Type == "order" {
			orderM := map[string]interface{}{"order": msg.Order}
			orderJSON, err := json.Marshal(orderM)
			if err != nil {
				courier.LogRequestError(r, channel, err)
			}
			metadata := json.RawMessage(orderJSON)
			event.WithMetadata(metadata)
		}

		if msg.Referral.Headline != "" {

			referral, err := json.Marshal(msg.Referral)
			if err != nil {
				courier.LogRequestError(r, channel, err)
			}
			metadata := json.RawMessage(referral)
			event.WithMetadata(metadata)
		}

		if mediaURL != "" {
			event.WithAttachment(mediaURL)
		}

		err = h.Backend().WriteMsg(ctx, event)
		if err != nil {
			return nil, err
		}

		h.Backend().WriteExternalIDSeen(event)

		events = append(events, event)
		data = append(data, courier.NewMsgReceiveData(event))
	}

	// now with any status updates
	for _, status := range payload.Statuses {
		msgStatus, found := waStatusMapping[status.Status]
		if !found {
			if waIgnoreStatuses[status.Status] {
				data = append(data, courier.NewInfoData(fmt.Sprintf("ignoring status: %s", status.Status)))
			} else {
				handlers.WriteAndLogRequestError(ctx, h, channel, w, r, fmt.Errorf("unknown status: %s", status.Status))
			}
			continue
		}

		event := h.Backend().NewMsgStatusForExternalID(channel, status.ID, msgStatus)
		err := h.Backend().WriteMsgStatus(ctx, event)

		// we don't know about this message, just tell them we ignored it
		if err == courier.ErrMsgNotFound {
			data = append(data, courier.NewInfoData(fmt.Sprintf("message id: %s not found, ignored", status.ID)))
			continue
		}

		if err != nil {
			return nil, err
		}

		events = append(events, event)
		data = append(data, courier.NewStatusData(event))
	}

	webhook := channel.ConfigForKey("webhook", nil)
	if webhook != nil {
		er := handlers.SendWebhooksExternal(r, webhook)
		if er != nil {
			courier.LogRequestError(r, channel, fmt.Errorf("could not send webhook: %s", er))
		}
	}

	return events, courier.WriteDataResponse(ctx, w, http.StatusOK, "Events Handled", data)
}

func resolveMediaURL(channel courier.Channel, mediaID string) (string, error) {
	urlStr := channel.StringConfigForKey(courier.ConfigBaseURL, "")
	url, err := url.Parse(urlStr)
	if err != nil {
		return "", fmt.Errorf("invalid base url set for WA channel: %s", err)
	}

	mediaPath, _ := url.Parse("/v1/media")
	mediaEndpoint := url.ResolveReference(mediaPath).String()

	fileURL := fmt.Sprintf("%s/%s", mediaEndpoint, mediaID)

	return fileURL, nil
}

// BuildDownloadMediaRequest to download media for message attachment with Bearer token set
func (h *handler) BuildDownloadMediaRequest(ctx context.Context, b courier.Backend, channel courier.Channel, attachmentURL string) (*http.Request, error) {
	token := channel.StringConfigForKey(courier.ConfigAuthToken, "")
	if token == "" {
		return nil, fmt.Errorf("missing token for WA channel")
	}

	// set the access token as the authorization header
	req, _ := http.NewRequest(http.MethodGet, attachmentURL, nil)
	req.Header.Set("User-Agent", utils.HTTPUserAgent)
	setWhatsAppAuthHeader(&req.Header, channel)
	return req, nil
}

var waStatusMapping = map[string]courier.MsgStatusValue{
	"sending":   courier.MsgWired,
	"sent":      courier.MsgSent,
	"delivered": courier.MsgDelivered,
	"read":      courier.MsgRead,
	"failed":    courier.MsgFailed,
}

var waIgnoreStatuses = map[string]bool{
	"deleted": true,
}

// {
//   "to": "16315555555",
//   "type": "text | audio | document | image",
//   "text": {
//     "body": "text message"
//   }
//	 "audio": {
//     "id": "the-audio-id"
// 	 }
//	 "document": {
//     "id": "the-document-id"
//     "caption": "the optional document caption"
// 	 }
//	 "image": {
//     "id": "the-image-id"
//     "caption": "the optional image caption"
// 	 }
//	 "video": {
//     "id": "the-video-id"
//     "caption": "the optional video caption"
//   }
// }

type mtTextPayload struct {
	To         string `json:"to"    validate:"required"`
	Type       string `json:"type"  validate:"required"`
	PreviewURL bool   `json:"preview_url,omitempty"`
	Text       struct {
		Body string `json:"body,omitempty"`
	} `json:"text,omitempty"`
}

type mtInteractivePayload struct {
	To          string `json:"to" validate:"required"`
	Type        string `json:"type" validate:"required"`
	Interactive struct {
		Type   string `json:"type" validate:"required"` //"text" | "image" | "video" | "document"
		Header *struct {
			Type     string      `json:"type"`
			Text     string      `json:"text,omitempty"`
			Video    mediaObject `json:"video,omitempty"`
			Image    mediaObject `json:"image,omitempty"`
			Document mediaObject `json:"document,omitempty"`
		} `json:"header,omitempty"`
		Body struct {
			Text string `json:"text"`
		} `json:"body" validate:"required"`
		Footer *struct {
			Text string `json:"text"`
		} `json:"footer,omitempty"`
		Action *struct {
			Button            string            `json:"button,omitempty"`
			Sections          []mtSection       `json:"sections,omitempty"`
			Buttons           []mtButton        `json:"buttons,omitempty"`
			CatalogID         string            `json:"catalog_id,omitempty"`
			ProductRetailerID string            `json:"product_retailer_id,omitempty"`
			Name              string            `json:"name,omitempty"`
			Parameters        map[string]string `json:"parameters,omitempty"`
		} `json:"action" validate:"required"`
	} `json:"interactive"`
}

type mtSection struct {
	Title        string          `json:"title,omitempty"`
	Rows         []mtSectionRow  `json:"rows,omitempty"`
	ProductItems []mtProductItem `json:"product_items,omitempty"`
}

type mtProductItem struct {
	ProductRetailerID string `json:"product_retailer_id" validate:"required"`
}

type mtSectionRow struct {
	ID          string `json:"id" validate:"required"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

type mtButton struct {
	Type  string `json:"type" validate:"required"`
	Reply struct {
		ID    string `json:"id" validate:"required"`
		Title string `json:"title" validate:"required"`
	} `json:"reply" validate:"required"`
}

type mediaObject struct {
	ID       string `json:"id,omitempty"`
	Link     string `json:"link,omitempty"`
	Caption  string `json:"caption,omitempty"`
	Filename string `json:"filename,omitempty"`
}

type LocalizableParam struct {
	Default string `json:"default"`
}

type mmtImage struct {
	Link string `json:"link,omitempty"`
}

type mmtDocument struct {
	Link     string `json:"link,omitempty"`
	Filename string `json:"filename,omitempty"`
}

type mmtVideo struct {
	Link string `json:"link,omitempty"`
}

type Param struct {
	Type     string       `json:"type"`
	Text     string       `json:"text,omitempty"`
	Image    *mmtImage    `json:"image,omitempty"`
	Document *mmtDocument `json:"document,omitempty"`
	Video    *mmtVideo    `json:"video,omitempty"`
}

type Component struct {
	Type       string  `json:"type"`
	Parameters []Param `json:"parameters"`
}

type templatePayload struct {
	To       string `json:"to"`
	Type     string `json:"type"`
	Template struct {
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
		Language  struct {
			Policy string `json:"policy"`
			Code   string `json:"code"`
		} `json:"language"`
		Components []Component `json:"components"`
	} `json:"template"`
}

type hsmPayload struct {
	To   string `json:"to"`
	Type string `json:"type"`
	HSM  struct {
		Namespace   string `json:"namespace"`
		ElementName string `json:"element_name"`
		Language    struct {
			Policy string `json:"policy"`
			Code   string `json:"code"`
		} `json:"language"`
		LocalizableParams []LocalizableParam `json:"localizable_params"`
	} `json:"hsm"`
}

type mtAudioPayload struct {
	To    string       `json:"to"    validate:"required"`
	Type  string       `json:"type"  validate:"required"`
	Audio *mediaObject `json:"audio"`
}

type mtDocumentPayload struct {
	To       string       `json:"to"    validate:"required"`
	Type     string       `json:"type"  validate:"required"`
	Document *mediaObject `json:"document"`
}

type mtImagePayload struct {
	To    string       `json:"to"    validate:"required"`
	Type  string       `json:"type"  validate:"required"`
	Image *mediaObject `json:"image"`
}

type mtStickerPayload struct {
	To      string       `json:"to"    validate:"required"`
	Type    string       `json:"type"  validate:"required"`
	Sticker *mediaObject `json:"sticker"`
}

type mtVideoPayload struct {
	To    string       `json:"to" validate:"required"`
	Type  string       `json:"type" validate:"required"`
	Video *mediaObject `json:"video"`
}

type mtErrorPayload struct {
	Errors []struct {
		Code    int    `json:"code"`
		Title   string `json:"title"`
		Details string `json:"details"`
	} `json:"errors"`
}

// whatsapp only allows messages up to 4096 chars
const maxMsgLength = 4096

// SendMsg sends the passed in message, returning any error
func (h *handler) SendMsg(ctx context.Context, msg courier.Msg) (courier.MsgStatus, error) {
	start := time.Now()

	// get our token
	token := msg.Channel().StringConfigForKey(courier.ConfigAuthToken, "")
	if token == "" {
		return nil, fmt.Errorf("missing token for WA channel")
	}

	urlStr := msg.Channel().StringConfigForKey(courier.ConfigBaseURL, "")
	url, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid base url set for WA channel: %s", err)
	}
	sendPath, _ := url.Parse("/v1/messages")

	status := h.Backend().NewMsgStatusForID(msg.Channel(), msg.ID(), courier.MsgErrored)

	var wppID string
	var logs []*courier.ChannelLog

	payloads, logs, err := buildPayloads(msg, h)

	fail := payloads == nil && err != nil
	if fail {
		return nil, err
	}
	for _, log := range logs {
		status.AddLog(log)
	}

	for i, payload := range payloads {
		externalID := ""

		wppID, externalID, logs, err = sendWhatsAppMsg(msg, sendPath, payload)
		// add logs to our status
		for _, log := range logs {
			status.AddLog(log)
		}
		if err != nil {
			break
		}

		// if this is our first message, record the external id
		if i == 0 {
			status.SetExternalID(externalID)
		}
	}

	// we are wired it there were no errors
	if err == nil {
		// so update contact URN if wppID != ""
		if wppID != "" {
			newURN, _ := urns.NewWhatsAppURN(wppID)
			err = status.SetUpdatedURN(msg.URN(), newURN)

			if err != nil {
				elapsed := time.Since(start)
				log := courier.NewChannelLogFromError("unable to update contact URN", msg.Channel(), msg.ID(), elapsed, err)
				status.AddLog(log)
			}
		}
		status.SetStatus(courier.MsgWired)
	}

	return status, nil
}

func buildPayloads(msg courier.Msg, h *handler) ([]interface{}, []*courier.ChannelLog, error) {
	start := time.Now()
	var payloads []interface{}
	var logs []*courier.ChannelLog
	var err error

	// do we have a template?
	templating, err := h.getTemplate(msg)
	qrs := msg.QuickReplies()

	if templating != nil || len(msg.Attachments()) == 0 && !(len(msg.Products()) > 0 || msg.SendCatalog()) {

		if err != nil {
			return nil, nil, errors.Wrapf(err, "unable to decode template: %s for channel: %s", string(msg.Metadata()), msg.Channel().UUID())
		}
		if templating != nil {
			namespace := templating.Namespace
			if namespace == "" {
				namespace = msg.Channel().StringConfigForKey(configNamespace, "")
			}
			if namespace == "" {
				return nil, nil, errors.Errorf("cannot send template message without Facebook namespace for channel: %s", msg.Channel().UUID())
			}

			if msg.Channel().BoolConfigForKey(configHSMSupport, false) {
				payload := hsmPayload{
					To:   msg.URN().Path(),
					Type: "hsm",
				}
				payload.HSM.Namespace = namespace
				payload.HSM.ElementName = templating.Template.Name
				payload.HSM.Language.Policy = "deterministic"
				payload.HSM.Language.Code = templating.Language
				for _, v := range templating.Variables {
					payload.HSM.LocalizableParams = append(payload.HSM.LocalizableParams, LocalizableParam{Default: v})
				}
				payloads = append(payloads, payload)
			} else {
				payload := templatePayload{
					To:   msg.URN().Path(),
					Type: "template",
				}
				payload.Template.Namespace = namespace
				payload.Template.Name = templating.Template.Name
				payload.Template.Language.Policy = "deterministic"
				payload.Template.Language.Code = templating.Language

				component := &Component{Type: "body"}

				for _, v := range templating.Variables {
					component.Parameters = append(component.Parameters, Param{Type: "text", Text: v})
				}
				payload.Template.Components = append(payload.Template.Components, *component)

				if len(msg.Attachments()) > 0 {

					header := &Component{Type: "header"}

					for _, attachment := range msg.Attachments() {

						mimeType, mediaURL := handlers.SplitAttachment(attachment)
						mediaID, mediaLogs, err := h.fetchMediaID(msg, mimeType, mediaURL)
						if len(mediaLogs) > 0 {
							logs = append(logs, mediaLogs...)
						}
						if err != nil {
							logrus.WithField("channel_uuid", msg.Channel().UUID().String()).WithError(err).Error("error while uploading media to whatsapp")
						}
						fileURL := mediaURL
						if err != nil && mediaID != "" {
							mediaURL = ""
						}
						if strings.HasPrefix(mimeType, "image") {
							image := &mmtImage{
								Link: mediaURL,
							}
							header.Parameters = append(header.Parameters, Param{Type: "image", Image: image})
							payload.Template.Components = append(payload.Template.Components, *header)
						} else if strings.HasPrefix(mimeType, "application") {

							filename, err := utils.BasePathForURL(fileURL)
							if err != nil {
								logrus.WithField("channel_uuid", msg.Channel().UUID().String()).WithError(err).Error("Error while parsing the media URL")
							}

							document := &mmtDocument{
								Link:     mediaURL,
								Filename: filename,
							}
							header.Parameters = append(header.Parameters, Param{Type: "document", Document: document})
							payload.Template.Components = append(payload.Template.Components, *header)
						} else if strings.HasPrefix(mimeType, "video") {
							video := &mmtVideo{
								Link: mediaURL,
							}
							header.Parameters = append(header.Parameters, Param{Type: "video", Video: video})
							payload.Template.Components = append(payload.Template.Components, *header)
						} else {
							duration := time.Since(start)
							err = fmt.Errorf("unknown attachment mime type: %s", mimeType)
							attachmentLogs := []*courier.ChannelLog{courier.NewChannelLogFromError("Error sending message", msg.Channel(), msg.ID(), duration, err)}
							logs = append(logs, attachmentLogs...)
							break
						}
					}
				}
				payloads = append(payloads, payload)
			}
		} else {
			parts := handlers.SplitMsgByChannel(msg.Channel(), msg.Text(), maxMsgLength)
			wppVersion := msg.Channel().ConfigForKey("version", "0").(string)
			isInteractiveMsgCompatible := semver.Compare(wppVersion, interactiveMsgMinSupVersion)
			isInteractiveMsg := (isInteractiveMsgCompatible >= 0) && (len(qrs) > 0) || (isInteractiveMsgCompatible >= 0) && len(msg.ListMessage().ListItems) > 0 || msg.InteractionType() == "cta_url"

			if isInteractiveMsg {
				for i, part := range parts {
					if i < (len(parts) - 1) { //if split into more than one message, the first parts will be text and the last interactive
						payload := mtTextPayload{
							To:   msg.URN().Path(),
							Type: "text",
						}
						payload.Text.Body = part
						payloads = append(payloads, payload)

					} else {
						payload := mtInteractivePayload{
							To:   msg.URN().Path(),
							Type: "interactive",
							Interactive: struct {
								Type   string "json:\"type\" validate:\"required\""
								Header *struct {
									Type     string      "json:\"type\""
									Text     string      "json:\"text,omitempty\""
									Video    mediaObject "json:\"video,omitempty\""
									Image    mediaObject "json:\"image,omitempty\""
									Document mediaObject "json:\"document,omitempty\""
								} "json:\"header,omitempty\""
								Body struct {
									Text string "json:\"text\""
								} "json:\"body\" validate:\"required\""
								Footer *struct {
									Text string "json:\"text\""
								} "json:\"footer,omitempty\""
								Action *struct {
									Button            string            "json:\"button,omitempty\""
									Sections          []mtSection       "json:\"sections,omitempty\""
									Buttons           []mtButton        "json:\"buttons,omitempty\""
									CatalogID         string            "json:\"catalog_id,omitempty\""
									ProductRetailerID string            "json:\"product_retailer_id,omitempty\""
									Name              string            "json:\"name,omitempty\""
									Parameters        map[string]string "json:\"parameters,omitempty\""
								} "json:\"action\" validate:\"required\""
							}{},
						}

						// up to 3 qrs the interactive message will be button type, otherwise it will be list
						if (len(qrs) <= 3 && len(msg.ListMessage().ListItems) == 0) && msg.InteractionType() != "cta_url" {
							payload.Interactive.Type = "button"
							payload.Interactive.Body.Text = part

							if msg.Footer() != "" {
								payload.Interactive.Footer = &struct {
									Text string "json:\"text\""
								}{Text: parseBacklashes(msg.Footer())}
							}

							if msg.HeaderText() != "" {
								payload.Interactive.Header = &struct {
									Type     string      "json:\"type\""
									Text     string      "json:\"text,omitempty\""
									Video    mediaObject "json:\"video,omitempty\""
									Image    mediaObject "json:\"image,omitempty\""
									Document mediaObject "json:\"document,omitempty\""
								}{Type: "text", Text: parseBacklashes(msg.HeaderText())}
							}

							btns := make([]mtButton, len(qrs))
							for i, qr := range qrs {
								btns[i] = mtButton{
									Type: "reply",
								}
								btns[i].Reply.ID = fmt.Sprint(i)
								text := parseBacklashes(qr)
								btns[i].Reply.Title = text
							}
							payload.Interactive.Action = &struct {
								Button            string            "json:\"button,omitempty\""
								Sections          []mtSection       "json:\"sections,omitempty\""
								Buttons           []mtButton        "json:\"buttons,omitempty\""
								CatalogID         string            "json:\"catalog_id,omitempty\""
								ProductRetailerID string            "json:\"product_retailer_id,omitempty\""
								Name              string            "json:\"name,omitempty\""
								Parameters        map[string]string "json:\"parameters,omitempty\""
							}{Buttons: btns}
							payloads = append(payloads, payload)
						} else if (len(qrs) <= 10 || len(msg.ListMessage().ListItems) > 0) && msg.InteractionType() != "cta_url" {
							payload.Interactive.Type = "list"
							payload.Interactive.Body.Text = part

							buttonName := "Menu"
							if len(msg.TextLanguage()) > 0 {
								buttonName = languageMenuMap[msg.TextLanguage()]
							}
							payload.Interactive.Action = &struct {
								Button            string            "json:\"button,omitempty\""
								Sections          []mtSection       "json:\"sections,omitempty\""
								Buttons           []mtButton        "json:\"buttons,omitempty\""
								CatalogID         string            "json:\"catalog_id,omitempty\""
								ProductRetailerID string            "json:\"product_retailer_id,omitempty\""
								Name              string            "json:\"name,omitempty\""
								Parameters        map[string]string "json:\"parameters,omitempty\""
							}{Button: buttonName}

							var section mtSection
							if len(qrs) > 0 && len(msg.ListMessage().ListItems) == 0 {
								section = mtSection{
									Rows: make([]mtSectionRow, len(qrs)),
								}
								for i, qr := range qrs {
									text := parseBacklashes(qr)
									section.Rows[i] = mtSectionRow{
										ID:    fmt.Sprint(i),
										Title: text,
									}
								}
							} else if len(msg.ListMessage().ListItems) > 0 {
								section = mtSection{
									Rows: make([]mtSectionRow, len(msg.ListMessage().ListItems)),
								}
								for i, listItem := range msg.ListMessage().ListItems {
									titleText := parseBacklashes(listItem.Title)
									descriptionText := parseBacklashes(listItem.Description)
									section.Rows[i] = mtSectionRow{
										ID:          listItem.UUID,
										Title:       titleText,
										Description: descriptionText,
									}
								}
								if msg.Footer() != "" {
									payload.Interactive.Footer = &struct {
										Text string "json:\"text\""
									}{Text: parseBacklashes(msg.Footer())}
								}
							}
							payload.Interactive.Action.Sections = []mtSection{section}
							if len(msg.ListMessage().ButtonText) > 0 {
								payload.Interactive.Action.Button = msg.ListMessage().ButtonText
							} else if len(msg.TextLanguage()) > 0 {
								payload.Interactive.Action.Button = languageMenuMap[msg.TextLanguage()]
							}

							payloads = append(payloads, payload)
						} else if msg.InteractionType() == "cta_url" {
							if ctaMessage := msg.CTAMessage(); ctaMessage != nil {
								payload.Interactive.Type = "cta_url"
								payload.Interactive.Body = struct {
									Text string "json:\"text\""
								}{msg.Text()}
								payload.Interactive.Action = &struct {
									Button            string            "json:\"button,omitempty\""
									Sections          []mtSection       "json:\"sections,omitempty\""
									Buttons           []mtButton        "json:\"buttons,omitempty\""
									CatalogID         string            "json:\"catalog_id,omitempty\""
									ProductRetailerID string            "json:\"product_retailer_id,omitempty\""
									Name              string            "json:\"name,omitempty\""
									Parameters        map[string]string "json:\"parameters,omitempty\""
								}{
									Name: "cta_url",
									Parameters: map[string]string{
										"display_text": parseBacklashes(ctaMessage.DisplayText),
										"url":          ctaMessage.URL,
									},
								}
								if msg.Footer() != "" {
									payload.Interactive.Footer = &struct {
										Text string "json:\"text\""
									}{Text: parseBacklashes(msg.Footer())}
								}

								if msg.HeaderText() != "" {
									payload.Interactive.Header = &struct {
										Type     string      "json:\"type\""
										Text     string      "json:\"text,omitempty\""
										Video    mediaObject "json:\"video,omitempty\""
										Image    mediaObject "json:\"image,omitempty\""
										Document mediaObject "json:\"document,omitempty\""
									}{
										Type: "text",
										Text: parseBacklashes(msg.HeaderText()),
									}
								}
								payloads = append(payloads, payload)
							}

						}
					}
				}
			} else {
				for _, part := range parts {

					//check if you have a link
					var payload mtTextPayload
					if strings.Contains(part, "https://") || strings.Contains(part, "http://") {
						payload = mtTextPayload{
							To:         msg.URN().Path(),
							Type:       "text",
							PreviewURL: true,
						}
					} else {
						payload = mtTextPayload{
							To:   msg.URN().Path(),
							Type: "text",
						}
					}
					payload.Text.Body = part
					payloads = append(payloads, payload)
				}
			}
		}
	} else {
		if (len(msg.Attachments()) > 0 && len(qrs) == 0 && len(msg.ListMessage().ListItems) == 0) || len(qrs) > 3 && len(msg.Attachments()) > 0 ||
			len(msg.ListMessage().ListItems) > 0 && len(msg.Attachments()) > 0 {
			for attachmentCount, attachment := range msg.Attachments() {
				mimeType, mediaURL := handlers.SplitAttachment(attachment)
				splitedAttType := strings.Split(mimeType, "/")
				mimeType = splitedAttType[0]
				attFormat := ""
				if len(splitedAttType) > 1 {
					attFormat = splitedAttType[1]
				}
				mediaID, mediaLogs, err := h.fetchMediaID(msg, mimeType, mediaURL)
				if len(mediaLogs) > 0 {
					logs = append(logs, mediaLogs...)
				}
				if err != nil {
					logrus.WithField("channel_uuid", msg.Channel().UUID().String()).WithError(err).Error("error while uploading media to whatsapp")
				}
				fileURL := mediaURL
				if err == nil && mediaID != "" {
					mediaURL = ""
				}
				mediaPayload := &mediaObject{ID: mediaID, Link: mediaURL}
				if strings.HasPrefix(mimeType, "audio") {
					payload := mtAudioPayload{
						To:   msg.URN().Path(),
						Type: "audio",
					}
					payload.Audio = mediaPayload
					payloads = append(payloads, payload)
					if attachmentCount == 0 && len(msg.Text()) > 0 {
						payloadText := mtTextPayload{
							To:   msg.URN().Path(),
							Type: "text",
						}
						payloadText.Text.Body = msg.Text()
						payloads = append(payloads, payloadText)
					}
				} else if strings.HasPrefix(mimeType, "application") {
					payload := mtDocumentPayload{
						To:   msg.URN().Path(),
						Type: "document",
					}
					if attachmentCount == 0 {
						mediaPayload.Caption = msg.Text()
					}
					mediaPayload.Filename, err = utils.BasePathForURL(fileURL)
					// Logging error
					if err != nil {
						logrus.WithField("channel_uuid", msg.Channel().UUID().String()).WithError(err).Error("Error while parsing the media URL")
					}
					payload.Document = mediaPayload
					payloads = append(payloads, payload)
				} else if strings.HasPrefix(mimeType, "image") {
					var payload interface{}
					if attFormat == "webp" {
						payload = mtStickerPayload{
							To:      msg.URN().Path(),
							Type:    "sticker",
							Sticker: mediaPayload,
						}
						if attachmentCount == 0 && len(msg.Text()) > 0 {
							payloadText := mtTextPayload{
								To:   msg.URN().Path(),
								Type: "text",
							}
							payloadText.Text.Body = msg.Text()
							payloads = append(payloads, payloadText)
						}
					} else {
						if attachmentCount == 0 {
							mediaPayload.Caption = msg.Text()
						}
						payload = mtImagePayload{
							To:    msg.URN().Path(),
							Type:  "image",
							Image: mediaPayload,
						}
					}
					payloads = append(payloads, payload)
				} else if strings.HasPrefix(mimeType, "video") {
					payload := mtVideoPayload{
						To:   msg.URN().Path(),
						Type: "video",
					}
					if attachmentCount == 0 {
						mediaPayload.Caption = msg.Text()
					}
					payload.Video = mediaPayload
					payloads = append(payloads, payload)
				} else {
					duration := time.Since(start)
					err = fmt.Errorf("unknown attachment mime type: %s", mimeType)
					attachmentLogs := []*courier.ChannelLog{courier.NewChannelLogFromError("Error sending message", msg.Channel(), msg.ID(), duration, err)}
					logs = append(logs, attachmentLogs...)
					break
				}
			}
		} else {
			payload := mtInteractivePayload{
				To:   msg.URN().Path(),
				Type: "interactive",
				Interactive: struct {
					Type   string "json:\"type\" validate:\"required\""
					Header *struct {
						Type     string      "json:\"type\""
						Text     string      "json:\"text,omitempty\""
						Video    mediaObject "json:\"video,omitempty\""
						Image    mediaObject "json:\"image,omitempty\""
						Document mediaObject "json:\"document,omitempty\""
					} "json:\"header,omitempty\""
					Body struct {
						Text string "json:\"text\""
					} "json:\"body\" validate:\"required\""
					Footer *struct {
						Text string "json:\"text\""
					} "json:\"footer,omitempty\""
					Action *struct {
						Button            string            "json:\"button,omitempty\""
						Sections          []mtSection       "json:\"sections,omitempty\""
						Buttons           []mtButton        "json:\"buttons,omitempty\""
						CatalogID         string            "json:\"catalog_id,omitempty\""
						ProductRetailerID string            "json:\"product_retailer_id,omitempty\""
						Name              string            "json:\"name,omitempty\""
						Parameters        map[string]string "json:\"parameters,omitempty\""
					} "json:\"action\" validate:\"required\""
				}{},
			}

			if (len(qrs) > 0 || len(msg.ListMessage().ListItems) > 0) && msg.InteractionType() != "cta_url" {
				// We can use buttons
				if len(qrs) <= 3 && len(msg.ListMessage().ListItems) == 0 {
					payload.Interactive.Type = "button"
					payload.Interactive.Body.Text = msg.Text()
					mimeType, mediaURL := handlers.SplitAttachment(msg.Attachments()[0])
					splitedAttType := strings.Split(mimeType, "/")
					mimeType = splitedAttType[0]

					mediaID, mediaLogs, err := h.fetchMediaID(msg, mimeType, mediaURL)
					filename, _ := utils.BasePathForURL(mediaURL)
					if len(mediaLogs) > 0 {
						logs = append(logs, mediaLogs...)
					}
					if err != nil {
						logrus.WithField("channel_uuid", msg.Channel().UUID().String()).WithError(err).Error("error while uploading media to whatsapp")
					}
					if err == nil && mediaID != "" {
						mediaURL = ""
					}
					mediaPayload := &mediaObject{ID: mediaID, Link: mediaURL}
					if strings.HasPrefix(mimeType, "application") {
						mediaPayload.Filename = filename
						payload.Interactive.Header = &struct {
							Type     string      "json:\"type\""
							Text     string      "json:\"text,omitempty\""
							Video    mediaObject "json:\"video,omitempty\""
							Image    mediaObject "json:\"image,omitempty\""
							Document mediaObject "json:\"document,omitempty\""
						}{Type: "document", Document: *mediaPayload}
					} else if strings.HasPrefix(mimeType, "image") {
						payload.Interactive.Header = &struct {
							Type     string      "json:\"type\""
							Text     string      "json:\"text,omitempty\""
							Video    mediaObject "json:\"video,omitempty\""
							Image    mediaObject "json:\"image,omitempty\""
							Document mediaObject "json:\"document,omitempty\""
						}{Type: "image", Image: *mediaPayload}
					} else if strings.HasPrefix(mimeType, "video") {
						payload.Interactive.Header = &struct {
							Type     string      "json:\"type\""
							Text     string      "json:\"text,omitempty\""
							Video    mediaObject "json:\"video,omitempty\""
							Image    mediaObject "json:\"image,omitempty\""
							Document mediaObject "json:\"document,omitempty\""
						}{Type: "video", Video: *mediaPayload}
					} else if strings.HasPrefix(mimeType, "audio") {
						payloadAudio := mtAudioPayload{
							To:   msg.URN().Path(),
							Type: "audio",
						}
						payloadAudio.Audio = mediaPayload
						payloads = append(payloads, payloadAudio)
					} else {
						duration := time.Since(start)
						err = fmt.Errorf("unknown attachment mime type: %s", mimeType)
						attachmentLogs := []*courier.ChannelLog{courier.NewChannelLogFromError("Error sending message", msg.Channel(), msg.ID(), duration, err)}
						logs = append(logs, attachmentLogs...)
					}

					btns := make([]mtButton, len(qrs))
					for i, qr := range qrs {
						btns[i] = mtButton{
							Type: "reply",
						}
						btns[i].Reply.ID = fmt.Sprint(i)
						text := parseBacklashes(qr)
						btns[i].Reply.Title = text
					}
					payload.Interactive.Action = &struct {
						Button            string            "json:\"button,omitempty\""
						Sections          []mtSection       "json:\"sections,omitempty\""
						Buttons           []mtButton        "json:\"buttons,omitempty\""
						CatalogID         string            "json:\"catalog_id,omitempty\""
						ProductRetailerID string            "json:\"product_retailer_id,omitempty\""
						Name              string            "json:\"name,omitempty\""
						Parameters        map[string]string "json:\"parameters,omitempty\""
					}{Buttons: btns}
					if msg.Footer() != "" {
						payload.Interactive.Footer = &struct {
							Text string "json:\"text\""
						}{Text: parseBacklashes(msg.Footer())}
					}
					payloads = append(payloads, payload)

				} else if len(qrs) <= 10 || len(msg.ListMessage().ListItems) > 0 {
					payload.Interactive.Type = "list"
					payload.Interactive.Body.Text = msg.Text()

					var section mtSection

					if len(qrs) > 0 {
						section = mtSection{
							Rows: make([]mtSectionRow, len(qrs)),
						}
						for i, qr := range qrs {
							text := parseBacklashes(qr)
							section.Rows[i] = mtSectionRow{
								ID:    fmt.Sprint(i),
								Title: text,
							}
						}
					} else {
						section = mtSection{
							Rows: make([]mtSectionRow, len(msg.ListMessage().ListItems)),
						}
						for i, listItem := range msg.ListMessage().ListItems {
							titleText := parseBacklashes(listItem.Title)
							descriptionText := parseBacklashes(listItem.Description)
							section.Rows[i] = mtSectionRow{
								ID:          listItem.UUID,
								Title:       titleText,
								Description: descriptionText,
							}
						}
						if msg.Footer() != "" {
							payload.Interactive.Footer = &struct {
								Text string "json:\"text\""
							}{Text: parseBacklashes(msg.Footer())}
						}
					}

					payload.Interactive.Action = &struct {
						Button            string            "json:\"button,omitempty\""
						Sections          []mtSection       "json:\"sections,omitempty\""
						Buttons           []mtButton        "json:\"buttons,omitempty\""
						CatalogID         string            "json:\"catalog_id,omitempty\""
						ProductRetailerID string            "json:\"product_retailer_id,omitempty\""
						Name              string            "json:\"name,omitempty\""
						Parameters        map[string]string "json:\"parameters,omitempty\""
					}{Button: "Menu", Sections: []mtSection{section}}

					if len(msg.ListMessage().ButtonText) > 0 {
						payload.Interactive.Action.Button = msg.ListMessage().ButtonText
					} else if len(msg.TextLanguage()) > 0 {
						payload.Interactive.Action.Button = languageMenuMap[msg.TextLanguage()]
					}
					payloads = append(payloads, payload)
				}
			} else {
				if ctaMessage := msg.CTAMessage(); ctaMessage != nil {
					payload.Interactive.Type = "cta_url"
					payload.Interactive.Body = struct {
						Text string "json:\"text\""
					}{parseBacklashes(msg.Text())}
					payload.Interactive.Action = &struct {
						Button            string            "json:\"button,omitempty\""
						Sections          []mtSection       "json:\"sections,omitempty\""
						Buttons           []mtButton        "json:\"buttons,omitempty\""
						CatalogID         string            "json:\"catalog_id,omitempty\""
						ProductRetailerID string            "json:\"product_retailer_id,omitempty\""
						Name              string            "json:\"name,omitempty\""
						Parameters        map[string]string "json:\"parameters,omitempty\""
					}{
						Name: "cta_url",
						Parameters: map[string]string{
							"display_text": parseBacklashes(ctaMessage.DisplayText),
							"url":          ctaMessage.URL,
						},
					}

					if msg.Footer() != "" {
						payload.Interactive.Footer = &struct {
							Text string "json:\"text\""
						}{Text: parseBacklashes(msg.Footer())}
					}

					if msg.HeaderText() != "" {
						payload.Interactive.Header = &struct {
							Type     string      "json:\"type\""
							Text     string      "json:\"text,omitempty\""
							Video    mediaObject "json:\"video,omitempty\""
							Image    mediaObject "json:\"image,omitempty\""
							Document mediaObject "json:\"document,omitempty\""
						}{
							Type: "text",
							Text: parseBacklashes(msg.HeaderText()),
						}
					}
					payloads = append(payloads, payload)
				}

			}
		}
	}

	if len(msg.Products()) > 0 || msg.SendCatalog() {

		catalogID := msg.Channel().StringConfigForKey("catalog_id", "")
		if catalogID == "" {
			return payloads, logs, errors.New("Catalog ID not found in channel config")
		}

		payload := mtInteractivePayload{Type: "interactive", To: msg.URN().Path(), Interactive: struct {
			Type   string "json:\"type\" validate:\"required\""
			Header *struct {
				Type     string      "json:\"type\""
				Text     string      "json:\"text,omitempty\""
				Video    mediaObject "json:\"video,omitempty\""
				Image    mediaObject "json:\"image,omitempty\""
				Document mediaObject "json:\"document,omitempty\""
			} "json:\"header,omitempty\""
			Body struct {
				Text string "json:\"text\""
			} "json:\"body\" validate:\"required\""
			Footer *struct {
				Text string "json:\"text\""
			} "json:\"footer,omitempty\""
			Action *struct {
				Button            string            "json:\"button,omitempty\""
				Sections          []mtSection       "json:\"sections,omitempty\""
				Buttons           []mtButton        "json:\"buttons,omitempty\""
				CatalogID         string            "json:\"catalog_id,omitempty\""
				ProductRetailerID string            "json:\"product_retailer_id,omitempty\""
				Name              string            "json:\"name,omitempty\""
				Parameters        map[string]string "json:\"parameters,omitempty\""
			} "json:\"action\" validate:\"required\""
		}{}}

		products := msg.Products()

		isUnitaryProduct := true
		var unitaryProduct string
		for _, product := range products {
			retailerIDs := toStringSlice(product["ProductRetailerIDs"])
			if len(products) > 1 || len(retailerIDs) > 1 {
				isUnitaryProduct = false
			} else {
				unitaryProduct = retailerIDs[0]
			}
		}

		var interactiveType string
		if msg.SendCatalog() {
			interactiveType = InteractiveProductCatalogMessageType
		} else if !isUnitaryProduct {
			interactiveType = InteractiveProductListType
		} else {
			interactiveType = InteractiveProductSingleType
		}

		payload.Interactive.Type = interactiveType

		payload.Interactive.Body = struct {
			Text string `json:"text"`
		}{
			Text: msg.Body(),
		}

		if msg.Header() != "" && !isUnitaryProduct && !msg.SendCatalog() {
			payload.Interactive.Header = &struct {
				Type     string      `json:"type"`
				Text     string      `json:"text,omitempty"`
				Video    mediaObject `json:"video,omitempty"`
				Image    mediaObject `json:"image,omitempty"`
				Document mediaObject `json:"document,omitempty"`
			}{
				Type: "text",
				Text: msg.Header(),
			}
		}

		if msg.Footer() != "" {
			payload.Interactive.Footer = &struct {
				Text string "json:\"text\""
			}{
				Text: parseBacklashes(msg.Footer()),
			}
		}

		if msg.SendCatalog() {
			payload.Interactive.Action = &struct {
				Button            string            `json:"button,omitempty"`
				Sections          []mtSection       `json:"sections,omitempty"`
				Buttons           []mtButton        `json:"buttons,omitempty"`
				CatalogID         string            `json:"catalog_id,omitempty"`
				ProductRetailerID string            `json:"product_retailer_id,omitempty"`
				Name              string            `json:"name,omitempty"`
				Parameters        map[string]string "json:\"parameters,omitempty\""
			}{
				Name: "catalog_message",
			}
			payloads = append(payloads, payload)
		} else if len(products) > 0 {
			if !isUnitaryProduct {
				actions := [][]mtSection{}
				sections := []mtSection{}
				i := 0

				for _, product := range products {
					i++
					retailerIDs := toStringSlice(product["ProductRetailerIDs"])
					sproducts := []mtProductItem{}

					for _, p := range retailerIDs {
						sproducts = append(sproducts, mtProductItem{
							ProductRetailerID: p,
						})
					}

					title := product["Product"].(string)
					if title == "product_retailer_id" {
						title = "items"
					}

					sections = append(sections, mtSection{Title: title, ProductItems: sproducts})

					if len(sections) == 6 || i == len(products) {
						actions = append(actions, sections)
						sections = []mtSection{}
					}
				}

				for _, sections := range actions {
					payload.Interactive.Action = &struct {
						Button            string            `json:"button,omitempty"`
						Sections          []mtSection       `json:"sections,omitempty"`
						Buttons           []mtButton        `json:"buttons,omitempty"`
						CatalogID         string            `json:"catalog_id,omitempty"`
						ProductRetailerID string            `json:"product_retailer_id,omitempty"`
						Name              string            `json:"name,omitempty"`
						Parameters        map[string]string "json:\"parameters,omitempty\""
					}{
						CatalogID: catalogID,
						Sections:  sections,
						Name:      msg.Action(),
					}

					payloads = append(payloads, payload)
				}

			} else {
				payload.Interactive.Action = &struct {
					Button            string            `json:"button,omitempty"`
					Sections          []mtSection       `json:"sections,omitempty"`
					Buttons           []mtButton        `json:"buttons,omitempty"`
					CatalogID         string            `json:"catalog_id,omitempty"`
					ProductRetailerID string            `json:"product_retailer_id,omitempty"`
					Name              string            `json:"name,omitempty"`
					Parameters        map[string]string "json:\"parameters,omitempty\""
				}{
					CatalogID:         catalogID,
					Name:              msg.Action(),
					ProductRetailerID: unitaryProduct,
				}
				payloads = append(payloads, payload)
			}
		}
	}

	return payloads, logs, err
}

// fetchMediaID tries to fetch the id for the uploaded media, setting the result in redis.
func (h *handler) fetchMediaID(msg courier.Msg, mimeType, mediaURL string) (string, []*courier.ChannelLog, error) {
	var logs []*courier.ChannelLog

	// check in cache first
	rc := h.Backend().RedisPool().Get()
	defer rc.Close()

	cacheKey := fmt.Sprintf(mediaCacheKeyPattern, msg.Channel().UUID().String())
	mediaID, err := rcache.Get(rc, cacheKey, mediaURL)
	if err != nil {
		return "", logs, errors.Wrapf(err, "error reading media id from redis: %s : %s", cacheKey, mediaURL)
	} else if mediaID != "" {
		return mediaID, logs, nil
	}

	// check in failure cache
	failKey := fmt.Sprintf("%s-%s", msg.Channel().UUID().String(), mediaURL)
	found, _ := failedMediaCache.Get(failKey)

	// any non nil value means we cached a failure, don't try again until our cache expires
	if found != nil {
		return "", logs, nil
	}

	// download media
	req, err := http.NewRequest("GET", mediaURL, nil)
	if err != nil {
		return "", logs, errors.Wrapf(err, "error building media request")
	}
	rr, err := utils.MakeHTTPRequest(req)
	log := courier.NewChannelLogFromRR("Fetching media", msg.Channel(), msg.ID(), rr).WithError("error fetching media", err)
	logs = append(logs, log)
	if err != nil {
		failedMediaCache.Set(failKey, true, cache.DefaultExpiration)
		return "", logs, nil
	}

	// upload media to WhatsApp
	baseURL := msg.Channel().StringConfigForKey(courier.ConfigBaseURL, "")
	url, err := url.Parse(baseURL)
	if err != nil {
		return "", logs, errors.Wrapf(err, "invalid base url set for WA channel: %s", baseURL)
	}
	dockerMediaURL, _ := url.Parse("/v1/media")

	req, err = http.NewRequest("POST", dockerMediaURL.String(), bytes.NewReader(rr.Body))
	if err != nil {
		return "", logs, errors.Wrapf(err, "error building request to media endpoint")
	}
	setWhatsAppAuthHeader(&req.Header, msg.Channel())
	mtype := http.DetectContentType(rr.Body)

	if mtype != mimeType || mtype == "application/octet-stream" || mtype == "application/zip" {
		mimeT := mimetype.Detect(rr.Body)
		req.Header.Add("Content-Type", mimeT.String())
	} else {
		req.Header.Add("Content-Type", mtype)
	}
	rr, err = utils.MakeHTTPRequest(req)
	log = courier.NewChannelLogFromRR("Uploading media to WhatsApp", msg.Channel(), msg.ID(), rr).WithError("Error uploading media to WhatsApp", err)
	logs = append(logs, log)
	if err != nil {
		failedMediaCache.Set(failKey, true, cache.DefaultExpiration)
		return "", logs, errors.Wrapf(err, "error uploading media to whatsapp")
	}

	// take uploaded media id
	mediaID, err = jsonparser.GetString(rr.Body, "media", "[0]", "id")
	if err != nil {
		return "", logs, errors.Wrapf(err, "error reading media id from response")
	}

	// put in cache
	err = rcache.Set(rc, cacheKey, mediaURL, mediaID)
	if err != nil {
		return "", logs, errors.Wrapf(err, "error setting media id in cache")
	}

	return mediaID, logs, nil
}

func sendWhatsAppMsg(msg courier.Msg, sendPath *url.URL, payload interface{}) (string, string, []*courier.ChannelLog, error) {
	start := time.Now()
	jsonBody, err := json.Marshal(payload)

	if err != nil {
		elapsed := time.Now().Sub(start)
		log := courier.NewChannelLogFromError("unable to build JSON body", msg.Channel(), msg.ID(), elapsed, err)
		return "", "", []*courier.ChannelLog{log}, err
	}

	req, _ := http.NewRequest(http.MethodPost, sendPath.String(), bytes.NewReader(jsonBody))
	req.Header = buildWhatsAppHeaders(msg.Channel())
	rr, err := utils.MakeHTTPRequest(req)
	log := courier.NewChannelLogFromRR("Message Sent", msg.Channel(), msg.ID(), rr).WithError("Message Send Error", err)
	errPayload := &mtErrorPayload{}
	err = json.Unmarshal(rr.Body, errPayload)

	// handle send msg errors
	if err == nil && len(errPayload.Errors) > 0 {
		if !hasWhatsAppContactError(*errPayload) {
			err := errors.Errorf("received error from send endpoint: %s", errPayload.Errors[0].Title)
			return "", "", []*courier.ChannelLog{log}, err
		}
		// check contact
		baseURL := fmt.Sprintf("%s://%s", sendPath.Scheme, sendPath.Host)
		rrCheck, err := checkWhatsAppContact(msg.Channel(), baseURL, msg.URN())

		if rrCheck == nil {
			elapsed := time.Now().Sub(start)
			checkLog := courier.NewChannelLogFromError("unable to build contact check request", msg.Channel(), msg.ID(), elapsed, err)
			return "", "", []*courier.ChannelLog{log, checkLog}, err
		}
		checkLog := courier.NewChannelLogFromRR("Contact check", msg.Channel(), msg.ID(), rrCheck).WithError("Status Error", err)

		if err != nil {
			return "", "", []*courier.ChannelLog{log, checkLog}, err
		}
		// update contact URN and msg destiny with returned wpp id
		wppID, err := jsonparser.GetString(rrCheck.Body, "contacts", "[0]", "wa_id")

		if err == nil {
			var updatedPayload interface{}

			// handle msg type casting
			switch v := payload.(type) {
			case mtTextPayload:
				v.To = wppID
				updatedPayload = v
			case mtImagePayload:
				v.To = wppID
				updatedPayload = v
			case mtVideoPayload:
				v.To = wppID
				updatedPayload = v
			case mtAudioPayload:
				v.To = wppID
				updatedPayload = v
			case mtDocumentPayload:
				v.To = wppID
				updatedPayload = v
			case templatePayload:
				v.To = wppID
				updatedPayload = v
			case hsmPayload:
				v.To = wppID
				updatedPayload = v
			}
			// marshal updated payload
			if updatedPayload != nil {
				payload = updatedPayload
				jsonBody, err = json.Marshal(payload)

				if err != nil {
					elapsed := time.Now().Sub(start)
					log := courier.NewChannelLogFromError("unable to build JSON body", msg.Channel(), msg.ID(), elapsed, err)
					return "", "", []*courier.ChannelLog{log, checkLog}, err
				}
			}
		}
		// try send msg again
		reqRetry, err := http.NewRequest(http.MethodPost, sendPath.String(), bytes.NewReader(jsonBody))
		if err != nil {
			return "", "", nil, err
		}
		reqRetry.Header = buildWhatsAppHeaders(msg.Channel())

		if retryParam != "" {
			reqRetry.URL.RawQuery = fmt.Sprintf("%s=1", retryParam)
		}

		rrRetry, err := utils.MakeHTTPRequest(reqRetry)
		retryLog := courier.NewChannelLogFromRR("Message Sent", msg.Channel(), msg.ID(), rrRetry).WithError("Message Send Error", err)

		if err != nil {
			return "", "", []*courier.ChannelLog{log, checkLog, retryLog}, err
		}
		externalID, err := getSendWhatsAppMsgId(rrRetry)
		return wppID, externalID, []*courier.ChannelLog{log, checkLog, retryLog}, err
	}
	externalID, err := getSendWhatsAppMsgId(rr)
	if err != nil {
		return "", "", []*courier.ChannelLog{log}, err
	}
	wppID, err := jsonparser.GetString(rr.Body, "contacts", "[0]", "wa_id")
	if wppID != "" && wppID != msg.URN().Path() {
		return wppID, externalID, []*courier.ChannelLog{log}, err
	}
	return "", externalID, []*courier.ChannelLog{log}, nil
}

func setWhatsAppAuthHeader(header *http.Header, channel courier.Channel) {
	authToken := channel.StringConfigForKey(courier.ConfigAuthToken, "")

	if channel.ChannelType() == channelTypeD3 {
		header.Set(d3AuthorizationKey, authToken)
	} else {
		header.Set("Authorization", fmt.Sprintf("Bearer %s", authToken))
	}
}

func buildWhatsAppHeaders(channel courier.Channel) http.Header {
	header := http.Header{
		"Content-Type": []string{"application/json"},
		"Accept":       []string{"application/json"},
		"User-Agent":   []string{utils.HTTPUserAgent},
	}
	setWhatsAppAuthHeader(&header, channel)
	return header
}

func hasWhatsAppContactError(payload mtErrorPayload) bool {
	for _, err := range payload.Errors {
		if err.Code == 1006 && err.Title == "Resource not found" && err.Details == "unknown contact" {
			return true
		}
	}
	return false
}

func getSendWhatsAppMsgId(rr *utils.RequestResponse) (string, error) {
	if externalID, err := jsonparser.GetString(rr.Body, "messages", "[0]", "id"); err == nil {
		return externalID, nil
	} else {
		return "", errors.Errorf("unable to get message id from response body")
	}
}

type mtContactCheckPayload struct {
	Blocking   string   `json:"blocking"`
	Contacts   []string `json:"contacts"`
	ForceCheck bool     `json:"force_check"`
}

func checkWhatsAppContact(channel courier.Channel, baseURL string, urn urns.URN) (*utils.RequestResponse, error) {
	payload := mtContactCheckPayload{
		Blocking:   "wait",
		Contacts:   []string{fmt.Sprintf("+%s", urn.Path())},
		ForceCheck: true,
	}
	reqBody, err := json.Marshal(payload)

	if err != nil {
		return nil, err
	}
	sendURL := fmt.Sprintf("%s/v1/contacts", baseURL)
	req, _ := http.NewRequest(http.MethodPost, sendURL, bytes.NewReader(reqBody))
	req.Header = buildWhatsAppHeaders(channel)
	rr, err := utils.MakeHTTPRequest(req)

	if err != nil {
		return rr, err
	}
	// check contact status
	if status, err := jsonparser.GetString(rr.Body, "contacts", "[0]", "status"); err == nil {
		if status == "valid" {
			return rr, nil
		} else {
			return rr, errors.Errorf(`contact status is "%s"`, status)
		}
	} else {
		return rr, err
	}
}

func (h *handler) getTemplate(msg courier.Msg) (*MsgTemplating, error) {
	mdJSON := msg.Metadata()
	if len(mdJSON) == 0 {
		return nil, nil
	}
	metadata := &TemplateMetadata{}
	err := json.Unmarshal(mdJSON, metadata)
	if err != nil {
		return nil, err
	}
	templating := metadata.Templating
	if templating == nil {
		return nil, nil
	}

	// check our template is valid
	err = handlers.Validate(templating)
	if err != nil {
		return nil, errors.Wrapf(err, "invalid templating definition")
	}
	// check country
	if templating.Country != "" {
		templating.Language = fmt.Sprintf("%s_%s", templating.Language, templating.Country)
	}

	// map our language from iso639-3_iso3166-2 to the WA country / iso638-2 pair
	language, found := languageMap[templating.Language]
	if !found {
		return nil, fmt.Errorf("unable to find mapping for language: %s", templating.Language)
	}
	templating.Language = language

	return templating, err
}

func parseBacklashes(baseText string) string {
	var text string
	if strings.Contains(baseText, "\\/") {
		text = strings.Replace(baseText, "\\", "", -1)
	} else if strings.Contains(baseText, "\\\\") {
		text = strings.Replace(baseText, "\\\\", "\\", -1)
	} else {
		text = baseText
	}
	return text
}

type TemplateMetadata struct {
	Templating *MsgTemplating `json:"templating"`
}

type MsgTemplating struct {
	Template struct {
		Name string `json:"name" validate:"required"`
		UUID string `json:"uuid" validate:"required"`
	} `json:"template" validate:"required,dive"`
	Language  string   `json:"language" validate:"required"`
	Country   string   `json:"country"`
	Namespace string   `json:"namespace"`
	Variables []string `json:"variables"`
}

// mapping from iso639-3_iso3166-2 to WA language code
var languageMap = map[string]string{
	"afr":    "af",    // Afrikaans
	"sqi":    "sq",    // Albanian
	"ara":    "ar",    // Arabic
	"aze":    "az",    // Azerbaijani
	"ben":    "bn",    // Bengali
	"bul":    "bg",    // Bulgarian
	"cat":    "ca",    // Catalan
	"zho":    "zh_CN", // Chinese
	"zho_CN": "zh_CN", // Chinese (CHN)
	"zho_HK": "zh_HK", // Chinese (HKG)
	"zho_TW": "zh_TW", // Chinese (TAI)
	"hrv":    "hr",    // Croatian
	"ces":    "cs",    // Czech
	"dah":    "da",    // Danish
	"nld":    "nl",    // Dutch
	"eng":    "en",    // English
	"eng_GB": "en_GB", // English (UK)
	"eng_US": "en_US", // English (US)
	"est":    "et",    // Estonian
	"fil":    "fil",   // Filipino
	"fin":    "fi",    // Finnish
	"fra":    "fr",    // French
	"deu":    "de",    // German
	"ell":    "el",    // Greek
	"guj":    "gu",    // Gujarati
	"hau":    "ha",    // Hausa
	"enb":    "he",    // Hebrew
	"hin":    "hi",    // Hindi
	"hun":    "hu",    // Hungarian
	"ind":    "id",    // Indonesian
	"gle":    "ga",    // Irish
	"ita":    "it",    // Italian
	"jpn":    "ja",    // Japanese
	"kan":    "kn",    // Kannada
	"kaz":    "kk",    // Kazakh
	"kor":    "ko",    // Korean
	"kir":    "ky_KG", // Kyrgyzstan
	"lao":    "lo",    // Lao
	"lav":    "lv",    // Latvian
	"lit":    "lt",    // Lithuanian
	"mal":    "ml",    // Malayalam
	"mkd":    "mk",    // Macedonian
	"msa":    "ms",    // Malay
	"mar":    "mr",    // Marathi
	"nob":    "nb",    // Norwegian
	"fas":    "fa",    // Persian
	"pol":    "pl",    // Polish
	"por":    "pt_PT", // Portuguese
	"por_BR": "pt_BR", // Portuguese (BR)
	"por_PT": "pt_PT", // Portuguese (POR)
	"pan":    "pa",    // Punjabi
	"ron":    "ro",    // Romanian
	"rus":    "ru",    // Russian
	"srp":    "sr",    // Serbian
	"slk":    "sk",    // Slovak
	"slv":    "sl",    // Slovenian
	"spa":    "es",    // Spanish
	"spa_AR": "es_AR", // Spanish (ARG)
	"spa_ES": "es_ES", // Spanish (SPA)
	"spa_MX": "es_MX", // Spanish (MEX)
	"swa":    "sw",    // Swahili
	"swe":    "sv",    // Swedish
	"tam":    "ta",    // Tamil
	"tel":    "te",    // Telugu
	"tha":    "th",    // Thai
	"tur":    "tr",    // Turkish
	"ukr":    "uk",    // Ukrainian
	"urd":    "ur",    // Urdu
	"uzb":    "uz",    // Uzbek
	"vie":    "vi",    // Vietnamese
	"zul":    "zu",    // Zulu
}

// iso language code mapping to respective "Menu" word translation
var languageMenuMap = map[string]string{
	"da-DK": "Menu",
	"de-DE": "Speisekarte",
	"en-AU": "Menu",
	"en-CA": "Menu",
	"en-GB": "Menu",
	"en-IN": "Menu",
	"en-US": "Menu",
	"ca-ES": "Menú",
	"es-ES": "Menú",
	"es-MX": "Menú",
	"fi-FI": "Valikko",
	"fr-CA": "Menu",
	"fr-FR": "Menu",
	"it-IT": "Menù",
	"ja-JP": "メニュー",
	"ko-KR": "메뉴",
	"nb-NO": "Meny",
	"nl-NL": "Menu",
	"pl-PL": "Menu",
	"pt-BR": "Menu",
	"ru-RU": "Меню",
	"sv-SE": "Meny",
	"zh-CN": "菜单",
	"zh-HK": "菜單",
	"zh-TW": "菜單",
	"ar-JO": "قائمة",
}

func toStringSlice(v interface{}) []string {
	if list, ok := v.([]interface{}); ok {
		result := make([]string, len(list))
		for i, item := range list {
			if str, ok := item.(string); ok {
				result[i] = str
			}
		}
		return result
	}
	return nil
}
