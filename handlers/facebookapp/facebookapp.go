package facebookapp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/buger/jsonparser"
	"github.com/gabriel-vasile/mimetype"
	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/courier/utils"
	"github.com/nyaruka/gocommon/rcache"
	"github.com/nyaruka/gocommon/urns"
	"github.com/patrickmn/go-cache"
	"github.com/pkg/errors"
)

// Endpoints we hit
var (
	sendURL  = "https://graph.facebook.com/v12.0/me/messages"
	graphURL = "https://graph.facebook.com/v12.0/"

	signatureHeader = "X-Hub-Signature"

	// max for the body
	maxMsgLengthIG  = 1000
	maxMsgLengthFBA = 2000
	maxMsgLengthWAC = 4096

	// Sticker ID substitutions
	stickerIDToEmoji = map[int64]string{
		369239263222822: "👍", // small
		369239343222814: "👍", // medium
		369239383222810: "👍", // big
	}

	tagByTopic = map[string]string{
		"event":    "CONFIRMED_EVENT_UPDATE",
		"purchase": "POST_PURCHASE_UPDATE",
		"account":  "ACCOUNT_UPDATE",
		"agent":    "HUMAN_AGENT",
	}
)

// keys for extra in channel events
const (
	referrerIDKey = "referrer_id"
	sourceKey     = "source"
	adIDKey       = "ad_id"
	typeKey       = "type"
	titleKey      = "title"
	payloadKey    = "payload"
)

var waStatusMapping = map[string]courier.MsgStatusValue{
	"sent":      courier.MsgSent,
	"delivered": courier.MsgDelivered,
	"read":      courier.MsgRead,
	"failed":    courier.MsgFailed,
}

var waIgnoreStatuses = map[string]bool{
	"deleted": true,
}

const (
	mediaCacheKeyPatternWhatsapp = "whatsapp_cloud_media_%s"
)

var failedMediaCache *cache.Cache

const (
	InteractiveProductSingleType         = "product"
	InteractiveProductListType           = "product_list"
	InteractiveProductCatalogType        = "catalog_product"
	InteractiveProductCatalogMessageType = "catalog_message"
)

func newHandler(channelType courier.ChannelType, name string, useUUIDRoutes bool) courier.ChannelHandler {
	return &handler{handlers.NewBaseHandlerWithParams(channelType, name, useUUIDRoutes)}
}

func init() {
	courier.RegisterHandler(newHandler("IG", "Instagram", false))
	courier.RegisterHandler(newHandler("FBA", "Facebook", false))
	courier.RegisterHandler(newHandler("WAC", "WhatsApp Cloud", false))

	failedMediaCache = cache.New(15*time.Minute, 15*time.Minute)
}

type handler struct {
	handlers.BaseHandler
}

// Initialize is called by the engine once everything is loaded
func (h *handler) Initialize(s courier.Server) error {
	h.SetServer(s)
	s.AddHandlerRoute(h, http.MethodGet, "receive", h.receiveVerify)
	s.AddHandlerRoute(h, http.MethodPost, "receive", h.receiveEvent)
	return nil
}

type Sender struct {
	ID      string `json:"id"`
	UserRef string `json:"user_ref,omitempty"`
}

type User struct {
	ID string `json:"id"`
}

// {
//   "object":"page",
//   "entry":[{
//     "id":"180005062406476",
//     "time":1514924367082,
//     "messaging":[{
//       "sender":  {"id":"1630934236957797"},
//       "recipient":{"id":"180005062406476"},
//       "timestamp":1514924366807,
//       "message":{
//         "mid":"mid.$cAAD5QiNHkz1m6cyj11guxokwkhi2",
//         "seq":33116,
//         "text":"65863634"
//       }
//     }]
//   }]
// }

type wacMedia struct {
	Caption  string `json:"caption"`
	Filename string `json:"filename"`
	ID       string `json:"id"`
	Mimetype string `json:"mime_type"`
	SHA256   string `json:"sha256"`
}

type wacSticker struct {
	Animated bool   `json:"animated"`
	ID       string `json:"id"`
	Mimetype string `json:"mime_type"`
	SHA256   string `json:"sha256"`
}

type moPayload struct {
	Object string `json:"object"`
	Entry  []struct {
		ID      string `json:"id"`
		Time    int64  `json:"time"`
		Changes []struct {
			Field string `json:"field"`
			Value struct {
				MessagingProduct string `json:"messaging_product"`
				Metadata         *struct {
					DisplayPhoneNumber string `json:"display_phone_number"`
					PhoneNumberID      string `json:"phone_number_id"`
				} `json:"metadata"`
				Contacts []struct {
					Profile struct {
						Name string `json:"name"`
					} `json:"profile"`
					WaID string `json:"wa_id"`
				} `json:"contacts"`
				Messages []struct {
					ID        string `json:"id"`
					From      string `json:"from"`
					Timestamp string `json:"timestamp"`
					Type      string `json:"type"`
					Context   *struct {
						Forwarded           bool   `json:"forwarded"`
						FrequentlyForwarded bool   `json:"frequently_forwarded"`
						From                string `json:"from"`
						ID                  string `json:"id"`
					} `json:"context"`
					Text struct {
						Body string `json:"body"`
					} `json:"text"`
					Image    *wacMedia   `json:"image"`
					Audio    *wacMedia   `json:"audio"`
					Video    *wacMedia   `json:"video"`
					Document *wacMedia   `json:"document"`
					Voice    *wacMedia   `json:"voice"`
					Sticker  *wacSticker `json:"sticker"`
					Location *struct {
						Latitude  float64 `json:"latitude"`
						Longitude float64 `json:"longitude"`
						Name      string  `json:"name"`
						Address   string  `json:"address"`
					} `json:"location"`
					Button *struct {
						Text    string `json:"text"`
						Payload string `json:"payload"`
					} `json:"button"`
					Interactive struct {
						Type        string `json:"type"`
						ButtonReply struct {
							ID    string `json:"id"`
							Title string `json:"title"`
						} `json:"button_reply,omitempty"`
						ListReply struct {
							ID    string `json:"id"`
							Title string `json:"title"`
						} `json:"list_reply,omitempty"`
						NFMReply struct {
							Name         string `json:"name,omitempty"`
							ResponseJSON string `json:"response_json"`
						} `json:"nfm_reply"`
					} `json:"interactive,omitempty"`
					Contacts []struct {
						Name struct {
							FirstName     string `json:"first_name"`
							LastName      string `json:"last_name"`
							FormattedName string `json:"formatted_name"`
						} `json:"name"`
						Phones []struct {
							Phone string `json:"phone"`
							WaID  string `json:"wa_id"`
							Type  string `json:"type"`
						} `json:"phones"`
					} `json:"contacts"`
					Referral struct {
						Headline   string    `json:"headline"`
						Body       string    `json:"body"`
						SourceType string    `json:"source_type"`
						SourceID   string    `json:"source_id"`
						SourceURL  string    `json:"source_url"`
						Image      *wacMedia `json:"image"`
						Video      *wacMedia `json:"video"`
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
					ID           string `json:"id"`
					RecipientID  string `json:"recipient_id"`
					Status       string `json:"status"`
					Timestamp    string `json:"timestamp"`
					Type         string `json:"type"`
					Conversation *struct {
						ID     string `json:"id"`
						Origin *struct {
							Type string `json:"type"`
						} `json:"origin"`
					} `json:"conversation"`
					Pricing *struct {
						PricingModel string `json:"pricing_model"`
						Billable     bool   `json:"billable"`
						Category     string `json:"category"`
					} `json:"pricing"`
				} `json:"statuses"`
				Errors []struct {
					Code  int    `json:"code"`
					Title string `json:"title"`
				} `json:"errors"`
				BanInfo struct {
					WabaBanState []string `json:"waba_ban_state"`
					WabaBanDate  string   `json:"waba_ban_date"`
				} `json:"ban_info"`
				CurrentLimit                 string `json:"current_limit"`
				Decision                     string `json:"decision"`
				DisplayPhoneNumber           string `json:"display_phone_number"`
				Event                        string `json:"event"`
				MaxDailyConversationPerPhone int    `json:"max_daily_conversation_per_phone"`
				MaxPhoneNumbersPerBusiness   int    `json:"max_phone_numbers_per_business"`
				MaxPhoneNumbersPerWaba       int    `json:"max_phone_numbers_per_waba"`
				Reason                       string `json:"reason"`
				RequestedVerifiedName        string `json:"requested_verified_name"`
				RestrictionInfo              []struct {
					RestrictionType string `json:"restriction_type"`
					Expiration      string `json:"expiration"`
				} `json:"restriction_info"`
				MessageTemplateID       int    `json:"message_template_id"`
				MessageTemplateName     string `json:"message_template_name"`
				MessageTemplateLanguage string `json:"message_template_language"`
			} `json:"value"`
		} `json:"changes"`
		Messaging []struct {
			Sender    Sender `json:"sender"`
			Recipient User   `json:"recipient"`
			Timestamp int64  `json:"timestamp"`

			OptIn *struct {
				Ref     string `json:"ref"`
				UserRef string `json:"user_ref"`
			} `json:"optin"`

			Referral *struct {
				Ref    string `json:"ref"`
				Source string `json:"source"`
				Type   string `json:"type"`
				AdID   string `json:"ad_id"`
			} `json:"referral"`

			Postback *struct {
				MID      string `json:"mid"`
				Title    string `json:"title"`
				Payload  string `json:"payload"`
				Referral struct {
					Ref    string `json:"ref"`
					Source string `json:"source"`
					Type   string `json:"type"`
					AdID   string `json:"ad_id"`
				} `json:"referral"`
			} `json:"postback"`

			Message *struct {
				IsEcho      bool   `json:"is_echo"`
				MID         string `json:"mid"`
				Text        string `json:"text"`
				IsDeleted   bool   `json:"is_deleted"`
				Attachments []struct {
					Type    string `json:"type"`
					Payload *struct {
						URL         string `json:"url"`
						StickerID   int64  `json:"sticker_id"`
						Coordinates *struct {
							Lat  float64 `json:"lat"`
							Long float64 `json:"long"`
						} `json:"coordinates"`
					}
				} `json:"attachments"`
			} `json:"message"`

			Delivery *struct {
				MIDs      []string `json:"mids"`
				Watermark int64    `json:"watermark"`
			} `json:"delivery"`

			MessagingFeedback *struct {
				FeedbackScreens []struct {
					ScreenID  int                         `json:"screen_id"`
					Questions map[string]FeedbackQuestion `json:"questions"`
				} `json:"feedback_screens"`
			} `json:"messaging_feedback"`
		} `json:"messaging"`
	} `json:"entry"`
}

type FeedbackQuestion struct {
	Type     string `json:"type"`
	Payload  string `json:"payload"`
	FollowUp *struct {
		Type    string `json:"type"`
		Payload string `json:"payload"`
	} `json:"follow_up"`
}

// GetChannel returns the channel
func (h *handler) GetChannel(ctx context.Context, r *http.Request) (courier.Channel, error) {
	if r.Method == http.MethodGet {
		return nil, nil
	}

	payload := &moPayload{}
	err := handlers.DecodeAndValidateJSON(payload, r)
	if err != nil {
		return nil, err
	}

	// is not a 'page' and 'instagram' object? ignore it
	if payload.Object != "page" && payload.Object != "instagram" && payload.Object != "whatsapp_business_account" {
		return nil, fmt.Errorf("object expected 'page', 'instagram' or 'whatsapp_business_account', found %s", payload.Object)
	}

	// no entries? ignore this request
	if len(payload.Entry) == 0 {
		return nil, fmt.Errorf("no entries found")
	}

	var channelAddress string

	//if object is 'page' returns type FBA, if object is 'instagram' returns type IG
	if payload.Object == "page" {
		channelAddress = payload.Entry[0].ID
		return h.Backend().GetChannelByAddress(ctx, courier.ChannelType("FBA"), courier.ChannelAddress(channelAddress))
	} else if payload.Object == "instagram" {
		channelAddress = payload.Entry[0].ID
		return h.Backend().GetChannelByAddress(ctx, courier.ChannelType("IG"), courier.ChannelAddress(channelAddress))
	} else {
		if len(payload.Entry[0].Changes) == 0 {
			return nil, fmt.Errorf("no changes found")
		}
		if payload.Entry[0].Changes[0].Field == "message_template_status_update" {
			channelAddress = payload.Entry[0].ID
			if channelAddress == "" {
				return nil, fmt.Errorf("no channel address found")
			}
			return h.Backend().GetChannelByAddress(ctx, courier.ChannelType("WAC"), courier.ChannelAddress(channelAddress))
		}
		channelAddress = payload.Entry[0].Changes[0].Value.Metadata.PhoneNumberID
		if channelAddress == "" {
			return nil, fmt.Errorf("no channel address found")
		}
		return h.Backend().GetChannelByAddress(ctx, courier.ChannelType("WAC"), courier.ChannelAddress(channelAddress))
	}
}

// receiveVerify handles Facebook's webhook verification callback
func (h *handler) receiveVerify(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {
	mode := r.URL.Query().Get("hub.mode")

	// this isn't a subscribe verification, that's an error
	if mode != "subscribe" {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, fmt.Errorf("unknown request"))
	}

	// verify the token against our server facebook webhook secret, if the same return the challenge FB sent us
	secret := r.URL.Query().Get("hub.verify_token")

	if fmt.Sprint(h.ChannelType()) == "FBA" || fmt.Sprint(h.ChannelType()) == "IG" {
		if secret != h.Server().Config().FacebookWebhookSecret {
			return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, fmt.Errorf("token does not match secret"))
		}
	} else {
		if secret != h.Server().Config().WhatsappCloudWebhookSecret {
			return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, fmt.Errorf("token does not match secret"))
		}
	}

	// and respond with the challenge token
	_, err := fmt.Fprint(w, r.URL.Query().Get("hub.challenge"))
	return nil, err
}

func resolveMediaURL(channel courier.Channel, mediaID string, token string) (string, error) {

	if token == "" {
		return "", fmt.Errorf("missing token for WAC channel")
	}

	base, _ := url.Parse(graphURL)
	path, _ := url.Parse(fmt.Sprintf("/%s", mediaID))
	retreiveURL := base.ResolveReference(path)

	// set the access token as the authorization header
	req, _ := http.NewRequest(http.MethodGet, retreiveURL.String(), nil)
	//req.Header.Set("User-Agent", utils.HTTPUserAgent)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	resp, err := utils.MakeHTTPRequest(req)
	if err != nil {
		return "", err
	}

	mediaURL, err := jsonparser.GetString(resp.Body, "url")
	return mediaURL, err
}

// receiveEvent is our HTTP handler function for incoming messages and status updates
func (h *handler) receiveEvent(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {
	err := h.validateSignature(r)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	payload := &moPayload{}
	err = handlers.DecodeAndValidateJSON(payload, r)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	var events []courier.Event
	var data []interface{}

	if channel.ChannelType() == "FBA" || channel.ChannelType() == "IG" {
		events, data, err = h.processFacebookInstagramPayload(ctx, channel, payload, w, r)
	} else {
		events, data, err = h.processCloudWhatsAppPayload(ctx, channel, payload, w, r)
		webhook := channel.ConfigForKey("webhook", nil)
		if webhook != nil {
			er := handlers.SendWebhooksExternal(r, webhook)
			if er != nil {
				courier.LogRequestError(r, channel, fmt.Errorf("could not send webhook: %s", er))
			}
		}
		er := handlers.SendWebhooksInternal(r, h.Server().Config().WhatsappCloudWebhooksEndpoint)
		if er != nil {
			courier.LogRequestError(r, channel, fmt.Errorf("could not send webhook: %s", er))
		}
	}

	if err != nil {
		return nil, err
	}

	return events, courier.WriteDataResponse(ctx, w, http.StatusOK, "Events Handled", data)
}

func (h *handler) processCloudWhatsAppPayload(ctx context.Context, channel courier.Channel, payload *moPayload, w http.ResponseWriter, r *http.Request) ([]courier.Event, []interface{}, error) {
	// the list of events we deal with
	events := make([]courier.Event, 0, 2)

	token := h.Server().Config().WhatsappAdminSystemUserToken

	// the list of data we will return in our response
	data := make([]interface{}, 0, 2)

	var contactNames = make(map[string]string)

	// for each entry
	for _, entry := range payload.Entry {
		if len(entry.Changes) == 0 {
			continue
		}

		for _, change := range entry.Changes {

			for _, contact := range change.Value.Contacts {
				contactNames[contact.WaID] = contact.Profile.Name
			}

			for _, msg := range change.Value.Messages {
				// create our date from the timestamp
				ts, err := strconv.ParseInt(msg.Timestamp, 10, 64)
				if err != nil {
					return nil, nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, fmt.Errorf("invalid timestamp: %s", msg.Timestamp))
				}
				date := time.Unix(ts, 0).UTC()

				urn, err := urns.NewWhatsAppURN(msg.From)
				if err != nil {
					return nil, nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
				}

				text := ""
				mediaURL := ""

				if msg.Type == "text" {
					text = msg.Text.Body
				} else if msg.Type == "audio" && msg.Audio != nil {
					text = msg.Audio.Caption
					mediaURL, err = resolveMediaURL(channel, msg.Audio.ID, token)
				} else if msg.Type == "voice" && msg.Voice != nil {
					text = msg.Voice.Caption
					mediaURL, err = resolveMediaURL(channel, msg.Voice.ID, token)
				} else if msg.Type == "button" && msg.Button != nil {
					text = msg.Button.Text
				} else if msg.Type == "document" && msg.Document != nil {
					text = msg.Document.Caption
					mediaURL, err = resolveMediaURL(channel, msg.Document.ID, token)
				} else if msg.Type == "image" && msg.Image != nil {
					text = msg.Image.Caption
					mediaURL, err = resolveMediaURL(channel, msg.Image.ID, token)
				} else if msg.Type == "sticker" && msg.Sticker != nil {
					mediaURL, err = resolveMediaURL(channel, msg.Sticker.ID, token)
				} else if msg.Type == "video" && msg.Video != nil {
					text = msg.Video.Caption
					mediaURL, err = resolveMediaURL(channel, msg.Video.ID, token)
				} else if msg.Type == "location" && msg.Location != nil {
					mediaURL = fmt.Sprintf("geo:%f,%f;name:%s;address:%s", msg.Location.Latitude, msg.Location.Longitude, msg.Location.Name, msg.Location.Address)
				} else if msg.Type == "interactive" && msg.Interactive.Type == "button_reply" {
					text = msg.Interactive.ButtonReply.Title
				} else if msg.Type == "interactive" && msg.Interactive.Type == "list_reply" {
					text = msg.Interactive.ListReply.Title
				} else if msg.Type == "order" {
					text = msg.Order.Text
				} else if msg.Type == "contacts" {

					if len(msg.Contacts) == 0 {
						return nil, nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, errors.New("no shared contact"))
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

				if msg.Interactive.Type == "nfm_reply" {
					nfmReply := map[string]interface{}{"nfm_reply": msg.Interactive.NFMReply}
					nfmReplyJSON, err := json.Marshal(nfmReply)
					if err != nil {
						courier.LogRequestError(r, channel, err)
					}
					metadata := json.RawMessage(nfmReplyJSON)
					event.WithMetadata(metadata)
				}

				if mediaURL != "" {
					event.WithAttachment(mediaURL)
				}

				err = h.Backend().WriteMsg(ctx, event)
				if err != nil {
					return nil, nil, err
				}

				h.Backend().WriteExternalIDSeen(event)

				events = append(events, event)
				data = append(data, courier.NewMsgReceiveData(event))

			}

			for _, status := range change.Value.Statuses {

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
					return nil, nil, err
				}

				if msgStatus == courier.MsgDelivered || msgStatus == courier.MsgRead {
					urn, err := urns.NewWhatsAppURN(status.RecipientID)
					if err != nil {
						handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
						continue
					}
					contactTo, err := h.Backend().GetContact(ctx, channel, urn, "", "")
					if err != nil {
						handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
						continue
					}
					err = h.Backend().UpdateContactLastSeenOn(ctx, contactTo.UUID(), time.Now())
					if err != nil {
						handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
						continue
					}
				}

				events = append(events, event)
				data = append(data, courier.NewStatusData(event))

			}

		}

	}
	return events, data, nil
}

func (h *handler) processFacebookInstagramPayload(ctx context.Context, channel courier.Channel, payload *moPayload, w http.ResponseWriter, r *http.Request) ([]courier.Event, []interface{}, error) {
	var err error

	// the list of events we deal with
	events := make([]courier.Event, 0, 2)

	// the list of data we will return in our response
	data := make([]interface{}, 0, 2)

	// for each entry
	for _, entry := range payload.Entry {
		// no entry, ignore
		if len(entry.Messaging) == 0 {
			continue
		}

		// grab our message, there is always a single one
		msg := entry.Messaging[0]

		// ignore this entry if it is to another page
		if channel.Address() != msg.Recipient.ID {
			continue
		}

		// create our date from the timestamp (they give us millis, arg is nanos)
		date := time.Unix(0, msg.Timestamp*1000000).UTC()

		sender := msg.Sender.UserRef
		if sender == "" {
			sender = msg.Sender.ID
		}

		var urn urns.URN

		// create our URN
		if payload.Object == "instagram" {
			urn, err = urns.NewInstagramURN(sender)
			if err != nil {
				return nil, nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
			}
		} else {
			urn, err = urns.NewFacebookURN(sender)
			if err != nil {
				return nil, nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
			}
		}

		if msg.OptIn != nil {
			// this is an opt in, if we have a user_ref, use that as our URN (this is a checkbox plugin)
			// TODO:
			//    We need to deal with the case of them responding and remapping the user_ref in that case:
			//    https://developers.facebook.com/docs/messenger-platform/discovery/checkbox-plugin
			//    Right now that we even support this isn't documented and I don't think anybody uses it, so leaving that out.
			//    (things will still work, we just will have dupe contacts, one with user_ref for the first contact, then with the real id when they reply)
			if msg.OptIn.UserRef != "" {
				urn, err = urns.NewFacebookURN(urns.FacebookRefPrefix + msg.OptIn.UserRef)
				if err != nil {
					return nil, nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
				}
			}

			event := h.Backend().NewChannelEvent(channel, courier.Referral, urn).WithOccurredOn(date)

			// build our extra
			extra := map[string]interface{}{
				referrerIDKey: msg.OptIn.Ref,
			}
			event = event.WithExtra(extra)

			err := h.Backend().WriteChannelEvent(ctx, event)
			if err != nil {
				return nil, nil, err
			}

			events = append(events, event)
			data = append(data, courier.NewEventReceiveData(event))

		} else if msg.Postback != nil {
			// by default postbacks are treated as new conversations, unless we have referral information
			eventType := courier.NewConversation
			if msg.Postback.Referral.Ref != "" {
				eventType = courier.Referral
			}
			event := h.Backend().NewChannelEvent(channel, eventType, urn).WithOccurredOn(date)

			// build our extra
			extra := map[string]interface{}{
				titleKey:   msg.Postback.Title,
				payloadKey: msg.Postback.Payload,
			}

			// add in referral information if we have it
			if eventType == courier.Referral {
				extra[referrerIDKey] = msg.Postback.Referral.Ref
				extra[sourceKey] = msg.Postback.Referral.Source
				extra[typeKey] = msg.Postback.Referral.Type

				if msg.Postback.Referral.AdID != "" {
					extra[adIDKey] = msg.Postback.Referral.AdID
				}
			}

			event = event.WithExtra(extra)

			err := h.Backend().WriteChannelEvent(ctx, event)
			if err != nil {
				return nil, nil, err
			}

			events = append(events, event)
			data = append(data, courier.NewEventReceiveData(event))

		} else if msg.Referral != nil {
			// this is an incoming referral
			event := h.Backend().NewChannelEvent(channel, courier.Referral, urn).WithOccurredOn(date)

			// build our extra
			extra := map[string]interface{}{
				sourceKey: msg.Referral.Source,
				typeKey:   msg.Referral.Type,
			}

			// add referrer id if present
			if msg.Referral.Ref != "" {
				extra[referrerIDKey] = msg.Referral.Ref
			}

			// add ad id if present
			if msg.Referral.AdID != "" {
				extra[adIDKey] = msg.Referral.AdID
			}
			event = event.WithExtra(extra)

			err := h.Backend().WriteChannelEvent(ctx, event)
			if err != nil {
				return nil, nil, err
			}

			events = append(events, event)
			data = append(data, courier.NewEventReceiveData(event))

		} else if msg.Message != nil {
			// this is an incoming message

			// ignore echos
			if msg.Message.IsEcho {
				data = append(data, courier.NewInfoData("ignoring echo"))
				continue
			}

			if msg.Message.IsDeleted {
				h.Backend().DeleteMsgWithExternalID(ctx, channel, msg.Message.MID)
				data = append(data, courier.NewInfoData("msg deleted"))
				continue
			}

			has_story_mentions := false

			text := msg.Message.Text

			attachmentURLs := make([]string, 0, 2)

			// if we have a sticker ID, use that as our text
			for _, att := range msg.Message.Attachments {
				if att.Type == "image" && att.Payload != nil && att.Payload.StickerID != 0 {
					text = stickerIDToEmoji[att.Payload.StickerID]
				}

				if att.Type == "location" {
					attachmentURLs = append(attachmentURLs, fmt.Sprintf("geo:%f,%f", att.Payload.Coordinates.Lat, att.Payload.Coordinates.Long))
				}

				if att.Type == "story_mention" {
					data = append(data, courier.NewInfoData("ignoring story_mention"))
					has_story_mentions = true
					continue
				}

				if att.Payload != nil && att.Payload.URL != "" {
					attachmentURLs = append(attachmentURLs, att.Payload.URL)
				}

			}

			// if we have a story mention, skip and do not save any message
			if has_story_mentions {
				continue
			}

			// create our message
			ev := h.Backend().NewIncomingMsg(channel, urn, text).WithExternalID(msg.Message.MID).WithReceivedOn(date)
			event := h.Backend().CheckExternalIDSeen(ev)

			// add any attachment URL found
			for _, attURL := range attachmentURLs {
				event.WithAttachment(attURL)
			}

			err := h.Backend().WriteMsg(ctx, event)
			if err != nil {
				return nil, nil, err
			}

			h.Backend().WriteExternalIDSeen(event)

			events = append(events, event)
			data = append(data, courier.NewMsgReceiveData(event))

		} else if msg.Delivery != nil {
			// this is a delivery report
			for _, mid := range msg.Delivery.MIDs {
				event := h.Backend().NewMsgStatusForExternalID(channel, mid, courier.MsgDelivered)
				err := h.Backend().WriteMsgStatus(ctx, event)

				// we don't know about this message, just tell them we ignored it
				if err == courier.ErrMsgNotFound {
					data = append(data, courier.NewInfoData("message not found, ignored"))
					continue
				}

				if err != nil {
					return nil, nil, err
				}

				events = append(events, event)
				data = append(data, courier.NewStatusData(event))
			}

		} else if msg.MessagingFeedback != nil {

			payloads := []string{}
			for _, v := range msg.MessagingFeedback.FeedbackScreens[0].Questions {
				payloads = append(payloads, v.Payload)
				if v.FollowUp != nil {
					payloads = append(payloads, v.FollowUp.Payload)
				}
			}

			text := strings.Join(payloads[:], "|")

			ev := h.Backend().NewIncomingMsg(channel, urn, text).WithReceivedOn(date)
			event := h.Backend().CheckExternalIDSeen(ev)

			err := h.Backend().WriteMsg(ctx, event)
			if err != nil {
				return nil, nil, err
			}

			h.Backend().WriteExternalIDSeen(event)
			events = append(events, event)
			data = append(data, courier.NewMsgReceiveData(event))

		} else {
			data = append(data, courier.NewInfoData("ignoring unknown entry type"))
		}
	}

	return events, data, nil
}

//	{
//	    "messaging_type": "<MESSAGING_TYPE>"
//	    "recipient":{
//	        "id":"<PSID>"
//	    },
//	    "message":{
//		       "text":"hello, world!"
//	        "attachment":{
//	            "type":"image",
//	            "payload":{
//	                "url":"http://www.messenger-rocks.com/image.jpg",
//	                "is_reusable":true
//	            }
//	        }
//	    }
//	}
type mtPayload struct {
	MessagingType string `json:"messaging_type"`
	Tag           string `json:"tag,omitempty"`
	Recipient     struct {
		UserRef string `json:"user_ref,omitempty"`
		ID      string `json:"id,omitempty"`
	} `json:"recipient"`
	Message struct {
		Text         string         `json:"text,omitempty"`
		QuickReplies []mtQuickReply `json:"quick_replies,omitempty"`
		Attachment   *mtAttachment  `json:"attachment,omitempty"`
	} `json:"message"`
}

type mtAttachment struct {
	Type    string `json:"type"`
	Payload struct {
		URL        string `json:"url"`
		IsReusable bool   `json:"is_reusable"`
	} `json:"payload"`
}

type mtQuickReply struct {
	Title       string `json:"title"`
	Payload     string `json:"payload"`
	ContentType string `json:"content_type"`
}

func (h *handler) SendMsg(ctx context.Context, msg courier.Msg) (courier.MsgStatus, error) {
	if msg.Channel().ChannelType() == "FBA" || msg.Channel().ChannelType() == "IG" {
		return h.sendFacebookInstagramMsg(ctx, msg)
	} else if msg.Channel().ChannelType() == "WAC" {
		return h.sendCloudAPIWhatsappMsg(ctx, msg)
	}

	return nil, fmt.Errorf("unssuported channel type")
}

func (h *handler) sendFacebookInstagramMsg(ctx context.Context, msg courier.Msg) (courier.MsgStatus, error) {
	// can't do anything without an access token
	accessToken := msg.Channel().StringConfigForKey(courier.ConfigAuthToken, "")
	if accessToken == "" {
		return nil, fmt.Errorf("missing access token")
	}

	topic := msg.Topic()
	payload := mtPayload{}

	// set our message type
	if msg.ResponseToExternalID() != "" {
		payload.MessagingType = "RESPONSE"
	} else if topic != "" {
		payload.MessagingType = "MESSAGE_TAG"
		payload.Tag = tagByTopic[topic]
	} else {
		payload.MessagingType = "UPDATE"
	}

	// build our recipient
	if msg.URN().IsFacebookRef() {
		payload.Recipient.UserRef = msg.URN().FacebookRef()
	} else {
		payload.Recipient.ID = msg.URN().Path()
	}

	msgURL, _ := url.Parse(sendURL)
	query := url.Values{}
	query.Set("access_token", accessToken)
	msgURL.RawQuery = query.Encode()

	status := h.Backend().NewMsgStatusForID(msg.Channel(), msg.ID(), courier.MsgErrored)

	isCustomerFeedbackTemplateMsg := strings.Contains(msg.Text(), "{customer_feedback_template}")

	if isCustomerFeedbackTemplateMsg {
		if msg.Text() != "" {
			text := strings.ReplaceAll(strings.ReplaceAll(msg.Text(), "\n", ""), "\t", "")

			splited := strings.Split(text, "|")
			templateMap := make(map[string]string)
			for i := 1; i < len(splited); i++ {
				field := strings.Split(splited[i], ":")
				templateMap[strings.TrimSpace(field[0])] = strings.TrimSpace(field[1])
			}

			var payloadMap map[string]interface{}

			if templateMap["follow_up"] != "" {
				payloadMap = map[string]interface{}{
					"recipient": map[string]string{
						"id": msg.URN().Path(),
					},
					"message": map[string]interface{}{
						"attachment": map[string]interface{}{
							"type": "template",
							"payload": map[string]interface{}{
								"template_type": "customer_feedback",
								"title":         templateMap["title"],
								"subtitle":      templateMap["subtitle"],
								"button_title":  templateMap["button_title"],
								"feedback_screens": []map[string]interface{}{
									{
										"questions": []map[string]interface{}{
											{
												"id":           templateMap["question_id"],
												"type":         templateMap["type"],
												"title":        templateMap["question_title"],
												"score_label":  templateMap["score_label"],
												"score_option": templateMap["score_option"],
												"follow_up": map[string]interface{}{
													"type":        "free_form",
													"placeholder": templateMap["follow_up_placeholder"],
												},
											},
										},
									},
								},
								"business_privacy": map[string]string{
									"url": templateMap["business_privacy"],
								},
							},
						},
					},
				}
			} else {
				payloadMap = map[string]interface{}{
					"recipient": map[string]string{
						"id": msg.URN().Path(),
					},
					"message": map[string]interface{}{
						"attachment": map[string]interface{}{
							"type": "template",
							"payload": map[string]interface{}{
								"template_type": "customer_feedback",
								"title":         templateMap["title"],
								"subtitle":      templateMap["subtitle"],
								"button_title":  templateMap["button_title"],
								"feedback_screens": []map[string]interface{}{
									{
										"questions": []map[string]interface{}{
											{
												"id":           templateMap["question_id"],
												"type":         templateMap["type"],
												"title":        templateMap["question_title"],
												"score_label":  templateMap["score_label"],
												"score_option": templateMap["score_option"],
											},
										},
									},
								},
								"business_privacy": map[string]string{
									"url": templateMap["business_privacy"],
								},
							},
						},
					},
				}
			}

			jsonBody, err := json.Marshal(payloadMap)
			if err != nil {
				return status, err
			}

			msgURL, _ := url.Parse("https://graph.facebook.com/v12.0/me/messages")
			query := url.Values{}
			query.Set("access_token", accessToken)
			msgURL.RawQuery = query.Encode()

			req, err := http.NewRequest(http.MethodPost, msgURL.String(), bytes.NewReader(jsonBody))
			if err != nil {
				return nil, err
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json")
			rr, err := utils.MakeHTTPRequest(req)

			log := courier.NewChannelLogFromRR("Message Sent", msg.Channel(), msg.ID(), rr).WithError("Message Send Error", err)
			status.AddLog(log)
			if err != nil {
				return status, nil
			}
			status.SetStatus(courier.MsgWired)
		}
		return status, nil
	}

	msgParts := make([]string, 0)
	if msg.Text() != "" {
		if msg.Channel().ChannelType() == "IG" {
			msgParts = handlers.SplitMsgByChannel(msg.Channel(), msg.Text(), maxMsgLengthIG)
		} else {
			msgParts = handlers.SplitMsgByChannel(msg.Channel(), msg.Text(), maxMsgLengthFBA)
		}

	}

	// send each part and each attachment separately. we send attachments first as otherwise quick replies
	// attached to text messages get hidden when images get delivered
	for i := 0; i < len(msgParts)+len(msg.Attachments()); i++ {
		if i < len(msg.Attachments()) {
			// this is an attachment
			payload.Message.Attachment = &mtAttachment{}
			attType, attURL := handlers.SplitAttachment(msg.Attachments()[i])
			attType = strings.Split(attType, "/")[0]
			if attType == "application" {
				attType = "file"
			}
			payload.Message.Attachment.Type = attType
			payload.Message.Attachment.Payload.URL = attURL
			payload.Message.Attachment.Payload.IsReusable = true
			payload.Message.Text = ""
		} else {
			// this is still a msg part
			payload.Message.Text = msgParts[i-len(msg.Attachments())]
			payload.Message.Attachment = nil
		}

		// include any quick replies on the last piece we send
		if i == (len(msgParts)+len(msg.Attachments()))-1 {
			for _, qr := range msg.QuickReplies() {
				payload.Message.QuickReplies = append(payload.Message.QuickReplies, mtQuickReply{qr, qr, "text"})
			}
		} else {
			payload.Message.QuickReplies = nil
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
		req.Header.Set("Accept", "application/json")

		rr, err := utils.MakeHTTPRequest(req)

		// record our status and log
		log := courier.NewChannelLogFromRR("Message Sent", msg.Channel(), msg.ID(), rr).WithError("Message Send Error", err)
		status.AddLog(log)
		if err != nil {
			return status, nil
		}

		externalID, err := jsonparser.GetString(rr.Body, "message_id")
		if err != nil {
			log.WithError("Message Send Error", errors.Errorf("unable to get message_id from body"))
			return status, nil
		}

		// if this is our first message, record the external id
		if i == 0 {
			status.SetExternalID(externalID)
			if msg.URN().IsFacebookRef() {
				recipientID, err := jsonparser.GetString(rr.Body, "recipient_id")
				if err != nil {
					log.WithError("Message Send Error", errors.Errorf("unable to get recipient_id from body"))
					return status, nil
				}

				referralID := msg.URN().FacebookRef()

				realIDURN, err := urns.NewFacebookURN(recipientID)
				if err != nil {
					log.WithError("Message Send Error", errors.Errorf("unable to make facebook urn from %s", recipientID))
				}

				contact, err := h.Backend().GetContact(ctx, msg.Channel(), msg.URN(), "", "")
				if err != nil {
					log.WithError("Message Send Error", errors.Errorf("unable to get contact for %s", msg.URN().String()))
				}
				realURN, err := h.Backend().AddURNtoContact(ctx, msg.Channel(), contact, realIDURN)
				if err != nil {
					log.WithError("Message Send Error", errors.Errorf("unable to add real facebook URN %s to contact with uuid %s", realURN.String(), contact.UUID()))
				}
				referralIDExtURN, err := urns.NewURNFromParts(urns.ExternalScheme, referralID, "", "")
				if err != nil {
					log.WithError("Message Send Error", errors.Errorf("unable to make ext urn from %s", referralID))
				}
				extURN, err := h.Backend().AddURNtoContact(ctx, msg.Channel(), contact, referralIDExtURN)
				if err != nil {
					log.WithError("Message Send Error", errors.Errorf("unable to add URN %s to contact with uuid %s", extURN.String(), contact.UUID()))
				}

				referralFacebookURN, err := h.Backend().RemoveURNfromContact(ctx, msg.Channel(), contact, msg.URN())
				if err != nil {
					log.WithError("Message Send Error", errors.Errorf("unable to remove referral facebook URN %s from contact with uuid %s", referralFacebookURN.String(), contact.UUID()))
				}

			}

		}

		// this was wired successfully
		status.SetStatus(courier.MsgWired)
	}

	return status, nil
}

type wacMTMedia struct {
	ID       string `json:"id,omitempty"`
	Link     string `json:"link,omitempty"`
	Caption  string `json:"caption,omitempty"`
	Filename string `json:"filename,omitempty"`
}

type wacMTSection struct {
	Title        string             `json:"title,omitempty"`
	Rows         []wacMTSectionRow  `json:"rows,omitempty"`
	ProductItems []wacMTProductItem `json:"product_items,omitempty"`
}

type wacMTSectionRow struct {
	ID          string `json:"id" validate:"required"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

type wacMTButton struct {
	Type  string `json:"type" validate:"required"`
	Reply struct {
		ID    string `json:"id" validate:"required"`
		Title string `json:"title" validate:"required"`
	} `json:"reply" validate:"required"`
}

type wacParam struct {
	Type     string      `json:"type"`
	Text     string      `json:"text,omitempty"`
	Image    *wacMTMedia `json:"image,omitempty"`
	Document *wacMTMedia `json:"document,omitempty"`
	Video    *wacMTMedia `json:"video,omitempty"`
}

type wacComponent struct {
	Type    string      `json:"type"`
	SubType string      `json:"sub_type,omitempty"`
	Index   string      `json:"index,omitempty"`
	Params  []*wacParam `json:"parameters"`
}

type wacText struct {
	Body       string `json:"body,omitempty"`
	PreviewURL bool   `json:"preview_url,omitempty"`
}

type wacLanguage struct {
	Policy string `json:"policy"`
	Code   string `json:"code"`
}

type wacTemplate struct {
	Name       string          `json:"name"`
	Language   *wacLanguage    `json:"language"`
	Components []*wacComponent `json:"components"`
}

type wacInteractive struct {
	Type   string `json:"type"`
	Header *struct {
		Type     string     `json:"type"`
		Text     string     `json:"text,omitempty"`
		Video    wacMTMedia `json:"video,omitempty"`
		Image    wacMTMedia `json:"image,omitempty"`
		Document wacMTMedia `json:"document,omitempty"`
	} `json:"header,omitempty"`
	Body struct {
		Text string `json:"text"`
	} `json:"body,omitempty"`
	Footer *struct {
		Text string `json:"text"`
	} `json:"footer,omitempty"`
	Action *struct {
		Button            string         `json:"button,omitempty"`
		Sections          []wacMTSection `json:"sections,omitempty"`
		Buttons           []wacMTButton  `json:"buttons,omitempty"`
		CatalogID         string         `json:"catalog_id,omitempty"`
		ProductRetailerID string         `json:"product_retailer_id,omitempty"`
		Name              string         `json:"name,omitempty"`
	} `json:"action,omitempty"`
}

type wacMTPayload struct {
	MessagingProduct string `json:"messaging_product"`
	RecipientType    string `json:"recipient_type"`
	To               string `json:"to"`
	Type             string `json:"type"`

	Text *wacText `json:"text,omitempty"`

	Document *wacMTMedia `json:"document,omitempty"`
	Image    *wacMTMedia `json:"image,omitempty"`
	Audio    *wacMTMedia `json:"audio,omitempty"`
	Video    *wacMTMedia `json:"video,omitempty"`
	Sticker  *wacMTMedia `json:"sticker,omitempty"`

	Interactive *wacInteractive `json:"interactive,omitempty"`

	Template *wacTemplate `json:"template,omitempty"`
}

type wacMTResponse struct {
	Messages []*struct {
		ID string `json:"id"`
	} `json:"messages"`
	Contacts []*struct {
		Input string `json:"input,omitempty"`
		WaID  string `json:"wa_id,omitempty"`
	} `json:"contacts,omitempty"`
}

type wacMTSectionProduct struct {
	Title string `json:"title,omitempty"`
}

type wacMTProductItem struct {
	ProductRetailerID string `json:"product_retailer_id" validate:"required"`
}

func (h *handler) sendCloudAPIWhatsappMsg(ctx context.Context, msg courier.Msg) (courier.MsgStatus, error) {
	// can't do anything without an access token
	accessToken := h.Server().Config().WhatsappAdminSystemUserToken
	userAccessToken := msg.Channel().StringConfigForKey(courier.ConfigUserToken, "")

	// check that userAccessToken is not empty
	token := accessToken
	if userAccessToken != "" {
		token = userAccessToken
	}

	start := time.Now()
	hasNewURN := false
	hasCaption := false

	base, _ := url.Parse(graphURL)
	path, _ := url.Parse(fmt.Sprintf("/%s/messages", msg.Channel().Address()))
	wacPhoneURL := base.ResolveReference(path)

	status := h.Backend().NewMsgStatusForID(msg.Channel(), msg.ID(), courier.MsgErrored)

	msgParts := make([]string, 0)
	if msg.Text() != "" {
		msgParts = handlers.SplitMsgByChannel(msg.Channel(), msg.Text(), maxMsgLengthWAC)
	}
	qrs := msg.QuickReplies()

	var payloadAudio wacMTPayload

	for i := 0; i < len(msgParts)+len(msg.Attachments()); i++ {
		payload := wacMTPayload{MessagingProduct: "whatsapp", RecipientType: "individual", To: msg.URN().Path()}

		// do we have a template?
		var templating *MsgTemplating
		templating, err := h.getTemplate(msg)
		if templating != nil || len(msg.Attachments()) == 0 {

			if err != nil {
				return nil, errors.Wrapf(err, "unable to decode template: %s for channel: %s", string(msg.Metadata()), msg.Channel().UUID())
			}
			if templating != nil {

				payload.Type = "template"

				template := wacTemplate{Name: templating.Template.Name, Language: &wacLanguage{Policy: "deterministic", Code: templating.Language}}
				payload.Template = &template

				if len(templating.Variables) > 0 {
					component := &wacComponent{Type: "body"}
					for _, v := range templating.Variables {
						component.Params = append(component.Params, &wacParam{Type: "text", Text: v})
					}
					template.Components = append(payload.Template.Components, component)
				}

				if len(msg.Attachments()) > 0 {

					header := &wacComponent{Type: "header"}

					attType, attURL := handlers.SplitAttachment(msg.Attachments()[0])
					mediaID, mediaLogs, err := h.fetchWACMediaID(msg, attType, attURL, accessToken)
					for _, log := range mediaLogs {
						status.AddLog(log)
					}
					if err != nil {
						status.AddLog(courier.NewChannelLogFromError("error on fetch media ID", msg.Channel(), msg.ID(), time.Since(start), err))
					} else if mediaID != "" {
						attURL = ""
					}
					attType = strings.Split(attType, "/")[0]

					parsedURL, err := url.Parse(attURL)
					if err != nil {
						return status, err
					}
					if attType == "application" {
						attType = "document"
					}

					media := wacMTMedia{ID: mediaID, Link: parsedURL.String()}
					if attType == "image" {
						header.Params = append(header.Params, &wacParam{Type: "image", Image: &media})
					} else if attType == "video" {
						header.Params = append(header.Params, &wacParam{Type: "video", Video: &media})
					} else if attType == "document" {
						media.Filename, err = utils.BasePathForURL(attURL)
						if err != nil {
							return nil, err
						}
						header.Params = append(header.Params, &wacParam{Type: "document", Document: &media})
					} else {
						return nil, fmt.Errorf("unknown attachment mime type: %s", attType)
					}
					payload.Template.Components = append(payload.Template.Components, header)
				}

			} else {
				if i < (len(msgParts) + len(msg.Attachments()) - 1) {
					// this is still a msg part
					text := &wacText{}
					payload.Type = "text"
					if strings.Contains(msgParts[i-len(msg.Attachments())], "https://") || strings.Contains(msgParts[i-len(msg.Attachments())], "http://") {
						text.PreviewURL = true
					}
					text.Body = msgParts[i-len(msg.Attachments())]
					payload.Text = text
				} else {
					if len(qrs) > 0 {
						payload.Type = "interactive"
						// We can use buttons
						if len(qrs) <= 3 {
							interactive := wacInteractive{Type: "button", Body: struct {
								Text string "json:\"text\""
							}{Text: msgParts[i-len(msg.Attachments())]}}

							btns := make([]wacMTButton, len(qrs))
							for i, qr := range qrs {
								btns[i] = wacMTButton{
									Type: "reply",
								}
								btns[i].Reply.ID = fmt.Sprint(i)
								var text string
								if strings.Contains(qr, "\\/") {
									text = strings.Replace(qr, "\\", "", -1)
								} else if strings.Contains(qr, "\\\\") {
									text = strings.Replace(qr, "\\\\", "\\", -1)
								} else {
									text = qr
								}
								btns[i].Reply.Title = text
							}
							interactive.Action = &struct {
								Button            string         "json:\"button,omitempty\""
								Sections          []wacMTSection "json:\"sections,omitempty\""
								Buttons           []wacMTButton  "json:\"buttons,omitempty\""
								CatalogID         string         "json:\"catalog_id,omitempty\""
								ProductRetailerID string         "json:\"product_retailer_id,omitempty\""
								Name              string         "json:\"name,omitempty\""
							}{Buttons: btns}
							payload.Interactive = &interactive
						} else if len(qrs) <= 10 {
							interactive := wacInteractive{Type: "list", Body: struct {
								Text string "json:\"text\""
							}{Text: msgParts[i-len(msg.Attachments())]}}

							section := wacMTSection{
								Rows: make([]wacMTSectionRow, len(qrs)),
							}
							for i, qr := range qrs {
								var text string
								if strings.Contains(qr, "\\/") {
									text = strings.Replace(qr, "\\", "", -1)
								} else if strings.Contains(qr, "\\\\") {
									text = strings.Replace(qr, "\\\\", "\\", -1)
								} else {
									text = qr
								}
								section.Rows[i] = wacMTSectionRow{
									ID:    fmt.Sprint(i),
									Title: text,
								}
							}

							interactive.Action = &struct {
								Button            string         "json:\"button,omitempty\""
								Sections          []wacMTSection "json:\"sections,omitempty\""
								Buttons           []wacMTButton  "json:\"buttons,omitempty\""
								CatalogID         string         "json:\"catalog_id,omitempty\""
								ProductRetailerID string         "json:\"product_retailer_id,omitempty\""
								Name              string         "json:\"name,omitempty\""
							}{Button: "Menu", Sections: []wacMTSection{
								section,
							}}

							if msg.TextLanguage() != "" {
								interactive.Action.Button = languageMenuMap[msg.TextLanguage()]
							}

							payload.Interactive = &interactive
						} else {
							return nil, fmt.Errorf("too many quick replies WAC supports only up to 10 quick replies")
						}
					} else {
						// this is still a msg part
						text := &wacText{}
						payload.Type = "text"
						if strings.Contains(msgParts[i-len(msg.Attachments())], "https://") || strings.Contains(msgParts[i-len(msg.Attachments())], "http://") {
							text.PreviewURL = true
						}
						text.Body = msgParts[i-len(msg.Attachments())]
						payload.Text = text
					}
				}
			}

		} else if i < len(msg.Attachments()) && len(qrs) == 0 || len(qrs) > 3 && i < len(msg.Attachments()) {
			attType, attURL := handlers.SplitAttachment(msg.Attachments()[i])
			fileURL := attURL

			splitedAttType := strings.Split(attType, "/")
			attType = splitedAttType[0]
			attFormat := ""
			if len(splitedAttType) > 1 {
				attFormat = splitedAttType[1]
			}

			mediaID, mediaLogs, err := h.fetchWACMediaID(msg, attType, attURL, accessToken)
			for _, log := range mediaLogs {
				status.AddLog(log)
			}
			if err != nil {
				status.AddLog(courier.NewChannelLogFromError("error on fetch media ID", msg.Channel(), msg.ID(), time.Since(start), err))
			} else if mediaID != "" {
				attURL = ""
			}
			parsedURL, err := url.Parse(attURL)
			if err != nil {
				return status, err
			}

			if attType == "application" {
				attType = "document"
			}
			payload.Type = attType
			media := wacMTMedia{ID: mediaID, Link: parsedURL.String()}
			if len(msgParts) == 1 && (attType != "audio" && attFormat != "webp") && len(msg.Attachments()) == 1 && len(msg.QuickReplies()) == 0 {
				media.Caption = msgParts[i]
				hasCaption = true
			}

			switch attType {
			case "image":
				if attFormat == "webp" {
					payload.Sticker = &media
					payload.Type = "sticker"
				} else {
					payload.Image = &media
				}
			case "audio":
				payload.Audio = &media
			case "video":
				payload.Video = &media
			case "document":
				media.Filename, err = utils.BasePathForURL(fileURL)
				if err != nil {
					return nil, err
				}
				payload.Document = &media
			}
			//end
		} else {
			if len(qrs) > 0 {
				payload.Type = "interactive"
				// We can use buttons
				if len(qrs) <= 3 {
					hasCaption = true
					interactive := wacInteractive{Type: "button", Body: struct {
						Text string "json:\"text\""
					}{Text: msgParts[i]}}

					if len(msg.Attachments()) > 0 {
						attType, attURL := handlers.SplitAttachment(msg.Attachments()[i])
						mediaID, mediaLogs, err := h.fetchWACMediaID(msg, attType, attURL, accessToken)
						for _, log := range mediaLogs {
							status.AddLog(log)
						}
						if err != nil {
							status.AddLog(courier.NewChannelLogFromError("error on fetch media ID", msg.Channel(), msg.ID(), time.Since(start), err))
						} else if mediaID != "" {
							attURL = ""
						}
						attType = strings.Split(attType, "/")[0]
						if attType == "application" {
							attType = "document"
						}
						fileURL := attURL
						media := wacMTMedia{ID: mediaID, Link: attURL}

						if attType == "image" {
							interactive.Header = &struct {
								Type     string     "json:\"type\""
								Text     string     "json:\"text,omitempty\""
								Video    wacMTMedia "json:\"video,omitempty\""
								Image    wacMTMedia "json:\"image,omitempty\""
								Document wacMTMedia "json:\"document,omitempty\""
							}{Type: "image", Image: media}
						} else if attType == "video" {
							interactive.Header = &struct {
								Type     string     "json:\"type\""
								Text     string     "json:\"text,omitempty\""
								Video    wacMTMedia "json:\"video,omitempty\""
								Image    wacMTMedia "json:\"image,omitempty\""
								Document wacMTMedia "json:\"document,omitempty\""
							}{Type: "video", Video: media}
						} else if attType == "document" {
							filename, err := utils.BasePathForURL(fileURL)
							if err != nil {
								return nil, err
							}
							media.Filename = filename
							interactive.Header = &struct {
								Type     string     "json:\"type\""
								Text     string     "json:\"text,omitempty\""
								Video    wacMTMedia "json:\"video,omitempty\""
								Image    wacMTMedia "json:\"image,omitempty\""
								Document wacMTMedia "json:\"document,omitempty\""
							}{Type: "document", Document: media}
						} else if attType == "audio" {
							var zeroIndex bool
							if i == 0 {
								zeroIndex = true
							}
							payloadAudio = wacMTPayload{MessagingProduct: "whatsapp", RecipientType: "individual", To: msg.URN().Path(), Type: "audio", Audio: &wacMTMedia{ID: mediaID, Link: attURL}}
							status, _, err := requestWAC(payloadAudio, token, msg, status, wacPhoneURL, zeroIndex)
							if err != nil {
								return status, nil
							}
						} else {
							interactive.Type = "button"
							interactive.Body.Text = msgParts[i]
						}
					}

					btns := make([]wacMTButton, len(qrs))
					for i, qr := range qrs {
						btns[i] = wacMTButton{
							Type: "reply",
						}
						btns[i].Reply.ID = fmt.Sprint(i)
						var text string
						if strings.Contains(qr, "\\/") {
							text = strings.Replace(qr, "\\", "", -1)
						} else if strings.Contains(qr, "\\\\") {
							text = strings.Replace(qr, "\\\\", "\\", -1)
						} else {
							text = qr
						}
						btns[i].Reply.Title = text
					}
					interactive.Action = &struct {
						Button            string         "json:\"button,omitempty\""
						Sections          []wacMTSection "json:\"sections,omitempty\""
						Buttons           []wacMTButton  "json:\"buttons,omitempty\""
						CatalogID         string         "json:\"catalog_id,omitempty\""
						ProductRetailerID string         "json:\"product_retailer_id,omitempty\""
						Name              string         "json:\"name,omitempty\""
					}{Buttons: btns}
					payload.Interactive = &interactive

				} else if len(qrs) <= 10 {
					interactive := wacInteractive{Type: "list", Body: struct {
						Text string "json:\"text\""
					}{Text: msgParts[i-len(msg.Attachments())]}}

					section := wacMTSection{
						Rows: make([]wacMTSectionRow, len(qrs)),
					}
					for i, qr := range qrs {
						var text string
						if strings.Contains(qr, "\\/") {
							text = strings.Replace(qr, "\\", "", -1)
						} else if strings.Contains(qr, "\\\\") {
							text = strings.Replace(qr, "\\\\", "\\", -1)
						} else {
							text = qr
						}
						section.Rows[i] = wacMTSectionRow{
							ID:    fmt.Sprint(i),
							Title: text,
						}
					}

					interactive.Action = &struct {
						Button            string         "json:\"button,omitempty\""
						Sections          []wacMTSection "json:\"sections,omitempty\""
						Buttons           []wacMTButton  "json:\"buttons,omitempty\""
						CatalogID         string         "json:\"catalog_id,omitempty\""
						ProductRetailerID string         "json:\"product_retailer_id,omitempty\""
						Name              string         "json:\"name,omitempty\""
					}{Button: "Menu", Sections: []wacMTSection{
						section,
					}}

					payload.Interactive = &interactive
				} else {
					return nil, fmt.Errorf("too many quick replies WAC supports only up to 10 quick replies")
				}
			} else {
				// this is still a msg part
				text := &wacText{}
				payload.Type = "text"
				if strings.Contains(msgParts[i-len(msg.Attachments())], "https://") || strings.Contains(msgParts[i-len(msg.Attachments())], "http://") {
					text.PreviewURL = true
				}
				text.Body = msgParts[i-len(msg.Attachments())]
				payload.Text = text
			}
		}
		var zeroIndex bool
		if i == 0 {
			zeroIndex = true
		}

		status, respPayload, err := requestWAC(payload, token, msg, status, wacPhoneURL, zeroIndex)
		if err != nil {
			return status, err
		}

		// if payload.contacts[0].wa_id != payload.contacts[0].input | to fix cases with 9 extra
		if len(respPayload.Contacts) > 0 && respPayload.Contacts[0].WaID != msg.URN().Path() {
			if !hasNewURN {
				toUpdateURN, err := urns.NewWhatsAppURN(respPayload.Contacts[0].WaID)
				if err != nil {
					return status, nil
				}
				err = status.SetUpdatedURN(msg.URN(), toUpdateURN)
				if err != nil {
					log := courier.NewChannelLogFromError("unable to update contact URN for a new based on  wa_id", msg.Channel(), msg.ID(), time.Since(start), err)
					status.AddLog(log)
				}
				hasNewURN = true
			}
		}
		if templating != nil && len(msg.Attachments()) > 0 || hasCaption {
			break
		}

	}

	if len(msg.Products()) > 0 || msg.SendCatalog() {

		catalogID := msg.Channel().StringConfigForKey("catalog_id", "")
		if catalogID == "" {
			return status, errors.New("Catalog ID not found in channel config")
		}

		payload := wacMTPayload{MessagingProduct: "whatsapp", RecipientType: "individual", To: msg.URN().Path()}

		payload.Type = "interactive"

		products := msg.Products()

		isUnitaryProduct := true
		var unitaryProduct string
		for _, values := range products {
			if len(values) > 1 || len(products) > 1 {
				isUnitaryProduct = false
			} else {
				unitaryProduct = values[0]
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

		interactive := wacInteractive{
			Type: interactiveType,
		}

		interactive.Body = struct {
			Text string `json:"text"`
		}{
			Text: msg.Body(),
		}

		if msg.Header() != "" && !isUnitaryProduct && !msg.SendCatalog() {
			interactive.Header = &struct {
				Type     string     `json:"type"`
				Text     string     `json:"text,omitempty"`
				Video    wacMTMedia `json:"video,omitempty"`
				Image    wacMTMedia `json:"image,omitempty"`
				Document wacMTMedia `json:"document,omitempty"`
			}{
				Type: "text",
				Text: msg.Header(),
			}
		}

		if msg.Footer() != "" {
			interactive.Footer = &struct {
				Text string "json:\"text\""
			}{
				Text: msg.Footer(),
			}
		}

		if msg.SendCatalog() {
			interactive.Action = &struct {
				Button            string         `json:"button,omitempty"`
				Sections          []wacMTSection `json:"sections,omitempty"`
				Buttons           []wacMTButton  `json:"buttons,omitempty"`
				CatalogID         string         `json:"catalog_id,omitempty"`
				ProductRetailerID string         `json:"product_retailer_id,omitempty"`
				Name              string         `json:"name,omitempty"`
			}{
				Name: "catalog_message",
			}
			payload.Interactive = &interactive
			status, _, err := requestWAC(payload, accessToken, msg, status, wacPhoneURL, true)
			if err != nil {
				return status, err
			}
		} else if len(products) > 0 {
			if !isUnitaryProduct {
				actions := [][]wacMTSection{}
				sections := []wacMTSection{}
				i := 0

				for key, values := range products {
					i++
					sproducts := []wacMTProductItem{}

					for _, p := range values {
						sproducts = append(sproducts, wacMTProductItem{
							ProductRetailerID: p,
						})
					}

					if key == "product_retailer_id" {
						key = "items"
					}

					sections = append(sections, wacMTSection{Title: key, ProductItems: sproducts})

					if len(sections) == 6 || i == len(products) {
						actions = append(actions, sections)
						sections = []wacMTSection{}
					}
				}

				for _, sections := range actions {
					interactive.Action = &struct {
						Button            string         `json:"button,omitempty"`
						Sections          []wacMTSection `json:"sections,omitempty"`
						Buttons           []wacMTButton  `json:"buttons,omitempty"`
						CatalogID         string         `json:"catalog_id,omitempty"`
						ProductRetailerID string         `json:"product_retailer_id,omitempty"`
						Name              string         `json:"name,omitempty"`
					}{
						CatalogID: catalogID,
						Sections:  sections,
						Name:      msg.Action(),
					}

					payload.Interactive = &interactive
					status, _, err := requestWAC(payload, accessToken, msg, status, wacPhoneURL, true)
					if err != nil {
						return status, err
					}
				}

			} else {
				interactive.Action = &struct {
					Button            string         `json:"button,omitempty"`
					Sections          []wacMTSection `json:"sections,omitempty"`
					Buttons           []wacMTButton  `json:"buttons,omitempty"`
					CatalogID         string         `json:"catalog_id,omitempty"`
					ProductRetailerID string         `json:"product_retailer_id,omitempty"`
					Name              string         `json:"name,omitempty"`
				}{
					CatalogID:         catalogID,
					Name:              msg.Action(),
					ProductRetailerID: unitaryProduct,
				}
				payload.Interactive = &interactive
				status, _, err := requestWAC(payload, accessToken, msg, status, wacPhoneURL, true)
				if err != nil {
					return status, err
				}
			}
		}
	}

	return status, nil
}

func requestWAC(payload wacMTPayload, accessToken string, msg courier.Msg, status courier.MsgStatus, wacPhoneURL *url.URL, zeroIndex bool) (courier.MsgStatus, *wacMTResponse, error) {
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return status, &wacMTResponse{}, err
	}

	req, err := http.NewRequest(http.MethodPost, wacPhoneURL.String(), bytes.NewReader(jsonBody))
	if err != nil {
		return status, &wacMTResponse{}, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	rr, err := utils.MakeHTTPRequest(req)

	// record our status and log
	log := courier.NewChannelLogFromRR("Message Sent", msg.Channel(), msg.ID(), rr).WithError("Message Send Error", err)
	status.AddLog(log)
	if err != nil {
		return status, &wacMTResponse{}, nil
	}

	respPayload := &wacMTResponse{}
	err = json.Unmarshal(rr.Body, respPayload)
	if err != nil {
		log.WithError("Message Send Error", errors.Errorf("unable to unmarshal response body"))
		return status, respPayload, nil
	}
	externalID := respPayload.Messages[0].ID
	if zeroIndex && externalID != "" {
		status.SetExternalID(externalID)
	}
	// this was wired successfully
	status.SetStatus(courier.MsgWired)

	return status, respPayload, nil
}

// DescribeURN looks up URN metadata for new contacts
func (h *handler) DescribeURN(ctx context.Context, channel courier.Channel, urn urns.URN) (map[string]string, error) {
	if channel.ChannelType() == "WAC" {
		return map[string]string{}, nil

	}

	// can't do anything with facebook refs, ignore them
	if urn.IsFacebookRef() {
		return map[string]string{}, nil
	}

	accessToken := channel.StringConfigForKey(courier.ConfigAuthToken, "")
	if accessToken == "" {
		return nil, fmt.Errorf("missing access token")
	}

	// build a request to lookup the stats for this contact
	base, _ := url.Parse(graphURL)
	path, _ := url.Parse(fmt.Sprintf("/%s", urn.Path()))
	u := base.ResolveReference(path)
	query := url.Values{}

	if fmt.Sprint(channel.ChannelType()) == "FBA" {
		query.Set("fields", "first_name,last_name")
		query.Set("access_token", accessToken)

		u.RawQuery = query.Encode()
		req, _ := http.NewRequest(http.MethodGet, u.String(), nil)
		rr, err := utils.MakeHTTPRequest(req)
		if err != nil {
			return nil, fmt.Errorf("unable to look up contact data:%s\n%s", err, rr.Response)
		}
		// read our first and last name
		firstName, _ := jsonparser.GetString(rr.Body, "first_name")
		lastName, _ := jsonparser.GetString(rr.Body, "last_name")
		return map[string]string{"name": utils.JoinNonEmpty(" ", firstName, lastName)}, nil
	} else {
		query.Set("access_token", accessToken)
		u.RawQuery = query.Encode()
		req, _ := http.NewRequest(http.MethodGet, u.String(), nil)
		rr, err := utils.MakeHTTPRequest(req)
		if err != nil {
			return nil, fmt.Errorf("unable to look up contact data:%s\n%s", err, rr.Response)
		}
		// read our name
		name, _ := jsonparser.GetString(rr.Body, "name")
		return map[string]string{"name": name}, nil
	}
}

// see https://developers.facebook.com/docs/messenger-platform/webhook#security
func (h *handler) validateSignature(r *http.Request) error {
	headerSignature := r.Header.Get(signatureHeader)
	if headerSignature == "" {
		return fmt.Errorf("missing request signature")
	}

	var appSecret string

	if fmt.Sprint(h.ChannelType()) == "FBA" || fmt.Sprint(h.ChannelType()) == "IG" {
		appSecret = h.Server().Config().FacebookApplicationSecret
	} else {
		appSecret = h.Server().Config().WhatsappCloudApplicationSecret
	}

	body, err := handlers.ReadBody(r, 100000)
	if err != nil {
		return fmt.Errorf("unable to read request body: %s", err)
	}

	expectedSignature, err := fbCalculateSignature(appSecret, body)
	if err != nil {
		return err
	}

	signature := ""
	if len(headerSignature) == 45 && strings.HasPrefix(headerSignature, "sha1=") {
		signature = strings.TrimPrefix(headerSignature, "sha1=")
	}

	// compare signatures in way that isn't sensitive to a timing attack
	if !hmac.Equal([]byte(expectedSignature), []byte(signature)) {
		return fmt.Errorf("invalid request signature, expected: %s got: %s for body: '%s'", expectedSignature, signature, string(body))
	}

	return nil
}

func fbCalculateSignature(appSecret string, body []byte) (string, error) {
	var buffer bytes.Buffer
	buffer.Write(body)

	// hash with SHA1
	mac := hmac.New(sha1.New, []byte(appSecret))
	mac.Write(buffer.Bytes())

	return hex.EncodeToString(mac.Sum(nil)), nil
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
	"kat":    "ka",    // Georgian
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
	"kin":    "rw_RW", // Kinyarwanda
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

func (h *handler) fetchWACMediaID(msg courier.Msg, mimeType, mediaURL string, accessToken string) (string, []*courier.ChannelLog, error) {
	var logs []*courier.ChannelLog

	rc := h.Backend().RedisPool().Get()
	defer rc.Close()

	cacheKey := fmt.Sprintf(mediaCacheKeyPatternWhatsapp, msg.Channel().UUID().String())
	mediaID, err := rcache.Get(rc, cacheKey, mediaURL)
	if err != nil {
		return "", logs, errors.Wrapf(err, "error reading media id from redis: %s : %s", cacheKey, mediaURL)
	} else if mediaID != "" {
		return mediaID, logs, nil
	}

	failKey := fmt.Sprintf("%s-%s", msg.Channel().UUID().String(), mediaURL)
	found, _ := failedMediaCache.Get(failKey)

	if found != nil {
		return "", logs, nil
	}

	// request to download media
	req, err := http.NewRequest("GET", mediaURL, nil)
	if err != nil {
		return "", logs, errors.Wrapf(err, "error builing media request")
	}
	rr, err := utils.MakeHTTPRequest(req)
	log := courier.NewChannelLogFromRR("Fetching media", msg.Channel(), msg.ID(), rr).WithError("error fetching media", err)
	logs = append(logs, log)
	if err != nil {
		failedMediaCache.Set(failKey, true, cache.DefaultExpiration)
		return "", logs, nil
	}

	// upload media to WhatsAppCloud
	base, _ := url.Parse(graphURL)
	path, _ := url.Parse(fmt.Sprintf("/%s/media", msg.Channel().Address()))
	wacPhoneURLMedia := base.ResolveReference(path)
	mediaID, logs, err = requestWACMediaUpload(rr.Body, mediaURL, wacPhoneURLMedia.String(), mimeType, msg, accessToken)
	if err != nil {
		return "", logs, err
	}

	// put in cache
	err = rcache.Set(rc, cacheKey, mediaURL, mediaID)
	if err != nil {
		return "", logs, errors.Wrapf(err, "error setting media id in cache")
	}

	return mediaID, logs, nil
}

func requestWACMediaUpload(file []byte, mediaURL string, requestUrl string, mimeType string, msg courier.Msg, accessToken string) (string, []*courier.ChannelLog, error) {
	var logs []*courier.ChannelLog

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	fileType := http.DetectContentType(file)
	fileName := filepath.Base(mediaURL)

	if fileType != mimeType || fileType == "application/octet-stream" || fileType == "application/zip" {
		fileType = mimetype.Detect(file).String()
	}

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name="%s"; filename="%s"`,
			"file", fileName))
	h.Set("Content-Type", fileType)
	fileField, err := writer.CreatePart(h)
	if err != nil {
		return "", logs, errors.Wrapf(err, "failed to create form field:")
	}

	fileReader := bytes.NewReader(file)
	_, err = io.Copy(fileField, fileReader)
	if err != nil {
		return "", logs, errors.Wrapf(err, "failed to copy file to form field")
	}

	err = writer.WriteField("messaging_product", "whatsapp")
	if err != nil {
		return "", logs, errors.Wrapf(err, "failed to add field")
	}

	err = writer.Close()
	if err != nil {
		return "", logs, errors.Wrapf(err, "failed to close multipart writer")
	}

	req, err := http.NewRequest("POST", requestUrl, body)
	if err != nil {
		return "", logs, errors.Wrapf(err, "failed to create request")
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))

	resp, err := utils.MakeHTTPRequest(req)
	log := courier.NewChannelLogFromRR("Uploading media to WhatsApp Cloud", msg.Channel(), msg.ID(), resp).WithError("Error uploading media to WhatsApp Cloud", err)
	logs = append(logs, log)
	if err != nil {
		return "", logs, errors.Wrapf(err, "request failed")
	}

	id, err := jsonparser.GetString(resp.Body, "id")
	if err != nil {
		return "", logs, errors.Wrapf(err, "error parsing media id")
	}
	return id, logs, nil
}
