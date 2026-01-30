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
	"log"
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
	"github.com/nyaruka/courier/billing"
	"github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/courier/templates"
	"github.com/nyaruka/courier/utils"
	"github.com/nyaruka/gocommon/rcache"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/patrickmn/go-cache"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// Endpoints we hit
var (
	sendURL  = "https://graph.facebook.com/v12.0/me/messages"
	graphURL = "https://graph.facebook.com/v22.0/"

	signatureHeader = "X-Hub-Signature"

	// max for the body
	maxMsgLengthIG             = 1000
	maxMsgLengthFBA            = 2000
	maxMsgLengthWAC            = 4096
	maxMsgLengthInteractiveWAC = 1024

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

var waTemplateTypeMapping = map[string]string{
	"authentication": "authentication",
	"marketing":      "marketing",
	"utility":        "utility",
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

// Adicionar constante para o tipo de evento
const (
	ContactUpdate = "contact_update"
)

// Add this map before the handler struct declaration (around line 110)
var integrationWebhookFields = map[string]bool{
	"message_template_status_update":  true,
	"template_category_update":        true,
	"message_template_quality_update": true,
	"account_update":                  true,
}

func newHandler(channelType courier.ChannelType, name string, useUUIDRoutes bool) courier.ChannelHandler {
	return &handler{handlers.NewBaseHandlerWithParams(channelType, name, useUUIDRoutes)}
}

func newWACDemoHandler(channelType courier.ChannelType, name string) courier.ChannelHandler {
	return &handler{handlers.NewBaseHandler(channelType, name)}
}

func init() {
	courier.RegisterHandler(newHandler("IG", "Instagram", false))
	courier.RegisterHandler(newHandler("FBA", "Facebook", false))
	courier.RegisterHandler(newHandler("WAC", "WhatsApp Cloud", false))
	courier.RegisterHandler(newWACDemoHandler("WCD", "WhatsApp Cloud Demo"))

	failedMediaCache = cache.New(15*time.Minute, 15*time.Minute)
}

type handler struct {
	handlers.BaseHandler
}

// Initialize is called by the engine once everything is loaded
func (h *handler) Initialize(s courier.Server) error {
	h.SetServer(s)
	if h.ChannelName() == "WhatsApp Cloud Demo" {
		s.AddHandlerRoute(h, http.MethodPost, "receive", h.receiveDemoEvent)
	} else {
		s.AddHandlerRoute(h, http.MethodGet, "receive", h.receiveVerify)
		s.AddHandlerRoute(h, http.MethodPost, "receive", h.receiveEvent)
	}
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
				From struct {
					ID       string `json:"id"`
					Username string `json:"username"`
				} `json:"from"`
				ID    string `json:"id"`
				Media struct {
					AdID             string `json:"ad_id"`
					ID               string `json:"id"`
					MediaProductType string `json:"media_product_type"`
					OriginalMediaID  string `json:"original_media_id"`
				}
				Text             string `json:"text"`
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
						PaymentMethod struct {
							PaymentMethod    string `json:"payment_method"`
							PaymentTimestamp int64  `json:"payment_timestamp"`
							ReferenceID      string `json:"reference_id"`
							LastFourDigits   string `json:"last_four_digits"`
							CredentialID     string `json:"credential_id"`
						} `json:"payment_method,omitempty"`
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
						CtwaClid   string    `json:"ctwa_clid"`
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
					Errors []struct {
						Code      int    `json:"code"`
						Title     string `json:"title"`
						Message   string `json:"message"`
						ErrorData struct {
							Details string `json:"details"`
						} `json:"error_data"`
						Type string `json:"type"`
					} `json:"errors"`
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
				MessageEchoes []struct {
					From      string `json:"from"`
					To        string `json:"to"`
					ID        string `json:"id"`
					Timestamp string `json:"timestamp"`
					Text      struct {
						Body string `json:"body"`
					} `json:"text"`
					Type string `json:"type"`
				} `json:"message_echoes"`
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
				StateSync               []struct {
					Type    string `json:"type"`
					Contact struct {
						FullName    string `json:"full_name"`
						FirstName   string `json:"first_name"`
						PhoneNumber string `json:"phone_number"`
					} `json:"contact"`
					Action   string `json:"action"`
					Metadata struct {
						Timestamp string `json:"timestamp"`
					} `json:"metadata"`
				} `json:"state_sync"`
				WabaInfo *struct {
					WabaID          string `json:"waba_id"`
					OwnerBusinessID string `json:"owner_business_id"`
				} `json:"waba_info"`
				Calls []struct {
					ID        string `json:"id"`
					To        string `json:"to"`
					From      string `json:"from"`
					Event     string `json:"event"`
					Timestamp string `json:"timestamp"`
					Direction string `json:"direction"`
					Session   struct {
						SdpType string `json:"sdp_type"`
						Sdp     string `json:"sdp"`
					} `json:"session"`
				} `json:"calls"`
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

type Flow struct {
	NFMReply NFMReply `json:"nfm_reply"`
}

type NFMReply struct {
	Name         string                 `json:"name,omitempty"`
	ResponseJSON map[string]interface{} `json:"response_json"`
}

// wacMessageMetadata holds the metadata extracted from a WhatsApp Cloud API message
type wacMessageMetadata struct {
	Key   string
	Value interface{}
}

// processWACOrderMetadata extracts order metadata from a WAC message
func processWACOrderMetadata(order interface{}) *wacMessageMetadata {
	return &wacMessageMetadata{
		Key:   "order",
		Value: order,
	}
}

// processWACNFMReplyMetadata extracts nfm_reply metadata from a WAC message
func processWACNFMReplyMetadata(name string, responseJSONStr string) (*wacMessageMetadata, error) {
	var responseJSON map[string]interface{}
	if err := json.Unmarshal([]byte(responseJSONStr), &responseJSON); err != nil {
		return nil, err
	}

	nfmReply := NFMReply{
		Name:         name,
		ResponseJSON: responseJSON,
	}

	return &wacMessageMetadata{
		Key:   "nfm_reply",
		Value: nfmReply,
	}, nil
}

// processWACPaymentMethodMetadata extracts payment_method metadata from a WAC message
func processWACPaymentMethodMetadata(paymentMethod interface{}) *wacMessageMetadata {
	return &wacMessageMetadata{
		Key:   "payment_method",
		Value: paymentMethod,
	}
}

// addMetadataWithOverwrite adds metadata to both the root level and overwrite_message field
// This function ensures all metadata for FBA, IG, and WAC channels are available in both places
// Note: "context" is excluded from overwrite_message and only added to root level
func addMetadataWithOverwrite(event courier.Msg, metadataToAdd map[string]interface{}) error {
	// Get existing metadata
	existingMetadata := event.Metadata()
	newMetadata := make(map[string]interface{})

	if existingMetadata != nil {
		if err := json.Unmarshal(existingMetadata, &newMetadata); err != nil {
			return err
		}
	}

	// Add metadata to root level
	for key, value := range metadataToAdd {
		newMetadata[key] = value
	}

	// Build or update overwrite_message
	overwriteMessage := make(map[string]interface{})
	if existing, ok := newMetadata["overwrite_message"].(map[string]interface{}); ok {
		overwriteMessage = existing
	}

	// Add all metadata to overwrite_message as well, except "context"
	for key, value := range metadataToAdd {
		if key != "context" {
			overwriteMessage[key] = value
		}
	}

	// Only add overwrite_message if it has at least one field
	if len(overwriteMessage) > 0 {
		newMetadata["overwrite_message"] = overwriteMessage
	} else {
		// Remove overwrite_message if it's empty
		delete(newMetadata, "overwrite_message")
	}

	// Marshal and set the new metadata
	metadataJSON, err := json.Marshal(newMetadata)
	if err != nil {
		return err
	}

	event.WithMetadata(json.RawMessage(metadataJSON))
	return nil
}

type IGComment struct {
	Text string `json:"text,omitempty"`
	From struct {
		ID       string `json:"id,omitempty"`
		Username string `json:"username,omitempty"`
	} `json:"from,omitempty"`
	Media struct {
		AdID             string `json:"ad_id,omitempty"`
		ID               string `json:"id,omitempty"`
		MediaProductType string `json:"media_product_type,omitempty"`
		OriginalMediaID  string `json:"original_media_id,omitempty"`
	} `json:"media,omitempty"`
	Time int64  `json:"time,omitempty"`
	ID   string `json:"id,omitempty"`
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
		if integrationWebhookFields[payload.Entry[0].Changes[0].Field] {
			logrus.WithField("field", payload.Entry[0].Changes[0].Field).Info("[integration_webhook] receiving integration webhook")
			er := handlers.SendWebhooks(r, h.Server().Config().WhatsappCloudWebhooksUrl, "", true)
			if er != nil {
				courier.LogRequestError(r, nil, fmt.Errorf("could not send template webhook: %s", er))
			}

			if payload.Entry[0].Changes[0].Field == "account_update" {
				logrus.WithField("event", payload.Entry[0].Changes[0].Value.Event).WithField("waba_info", payload.Entry[0].Changes[0].Value.WabaInfo).Info("[account_update] receiving account_update webhook")
				// Handle account_update webhook type
				if payload.Entry[0].Changes[0].Value.Event == "MM_LITE_TERMS_SIGNED" && payload.Entry[0].Changes[0].Value.WabaInfo != nil {
					logrus.WithField("waba_id", payload.Entry[0].Changes[0].Value.WabaInfo.WabaID).Info("[mmlite] MM_LITE_TERMS_SIGNED event detected for waba_id")
					wabaID := payload.Entry[0].Changes[0].Value.WabaInfo.WabaID

					// Update channel config with ad_account_id and mmlite for all channels with matching waba_id
					err := h.Backend().UpdateChannelConfigByWabaID(ctx, wabaID, map[string]interface{}{
						"mmlite": true,
					})
					if err != nil {
						logrus.WithError(err).WithField("waba_id", wabaID).Error("[mmlite] error updating channel config with waba_id")
						return nil, fmt.Errorf("error updating channel config with waba_id %s: %v", wabaID, err)
					}
					logrus.WithField("waba_id", wabaID).Info("[mmlite] channel config updated with waba_id")
				}
			}

			return nil, fmt.Errorf("template update, so ignore")
		} else if payload.Entry[0].Changes[0].Field == "flows" {
			er := handlers.SendWebhooks(r, h.Server().Config().WhatsappCloudWebhooksUrlFlows, h.Server().Config().WhatsappCloudWebhooksTokenFlows, false)
			if er != nil {
				courier.LogRequestError(r, nil, fmt.Errorf("could not send template webhook: %s", er))
			}
			return nil, fmt.Errorf("template update, so ignore")
		}
		channelAddress = payload.Entry[0].Changes[0].Value.Metadata.PhoneNumberID
		if channelAddress == "" {
			return nil, fmt.Errorf("no channel address found")
		}

		// get a value if exists from request header to a variable routerToken
		routerToken := r.Header.Get("X-Router-Token")
		if routerToken != "" {
			return h.Backend().GetChannelByAddressWithRouterToken(ctx, courier.ChannelType("WAC"), courier.ChannelAddress(channelAddress), routerToken)
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

func (h *handler) receiveDemoEvent(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {
	payload := &moPayload{}
	err := handlers.DecodeAndValidateJSON(payload, r)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	events, data, err := h.processCloudWhatsAppPayload(ctx, channel, payload, w, r)
	if err != nil {
		return nil, err
	}

	return events, courier.WriteDataResponse(ctx, w, http.StatusOK, "Events Handled", data)
}

// receiveEvent is our HTTP handler function for incoming messages and status updates
func (h *handler) receiveEvent(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {
	if h.ChannelType() == "WAC" {
		routerToken := r.Header.Get("X-Router-Token")
		if routerToken != "" {
			payload := &moPayload{}
			err := handlers.DecodeAndValidateJSON(payload, r)
			if err != nil {
				return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
			}
			events, data, err := h.processCloudWhatsAppPayload(ctx, channel, payload, w, r)
			if err != nil {
				return nil, err
			}
			return events, courier.WriteDataResponse(ctx, w, http.StatusOK, "Events Handled", data)
		}
	}

	err := h.validateSignature(r)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	// if the channel has the address equals to Config().DemoAddress, then we need to proxy the request to the demo url
	log.Println("WHATSAPP ADDRESSES")
	log.Println("channel.Address(): ", channel.Address())
	log.Println("h.Server().Config().WhatsappCloudDemoAddress: ", h.Server().Config().WhatsappCloudDemoAddress)
	if channel.Address() == h.Server().Config().WhatsappCloudDemoAddress {
		log.Println("PROXIING TO DEMO URL")
		demoURL := h.Server().Config().WhatsappCloudDemoURL
		proxyReq, err := http.NewRequest(r.Method, demoURL, r.Body)
		if err != nil {
			return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
		}
		proxyReq.Header = r.Header
		rr, err := utils.MakeHTTPRequest(proxyReq)
		if err != nil {
			return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
		}
		// print the response body
		log.Println("rr.Body: ", string(rr.Body))
		return nil, nil // must return events of proxied to demo?
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

		wabaID := channel.StringConfigForKey(courier.ConfigWabaID, "")
		if wabaID != "" && wabaID != payload.Entry[0].ID {
			data = append(data, courier.NewInfoData(fmt.Sprintf("ignoring messages from different waba id: %s, %s", payload.Entry[0].ID, wabaID)))
			return nil, courier.WriteDataResponse(ctx, w, http.StatusOK, "Events Handled", data)
		}

		events, data, err = h.processCloudWhatsAppPayload(ctx, channel, payload, w, r)
		webhook := channel.ConfigForKey("webhook", nil)
		if webhook != nil {
			er := handlers.SendWebhooksExternal(r, webhook)
			if er != nil {
				courier.LogRequestError(r, channel, fmt.Errorf("could not send webhook: %s", er))
			}
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
				} else if msg.Type == "unsupported" {
					courier.LogRequestError(r, channel, fmt.Errorf("unsupported message type %s", msg.Type))
					data = append(data, courier.NewInfoData(fmt.Sprintf("unsupported message type %s", msg.Type)))
					continue
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
				} else if msg.Type == "interactive" && msg.Interactive.Type == "payment_method" {
					text = ""
				} else if msg.Type == "reaction" {
					data = append(data, courier.NewInfoData("ignoring echo reaction message"))
					continue
				} else {
					// we received a message type we do not support.
					courier.LogRequestError(r, channel, fmt.Errorf("unsupported message type %s", msg.Type))
				}
				//check if the message is unavailable
				if msg.Errors != nil {
					for _, error := range msg.Errors {
						if error.Code == 1060 {
							fmt.Println("Message unavailable")
							text = "This message is currently unavailable. Please check your phone for the message."
						}
					}
				}

				// create our message
				ev := h.Backend().NewIncomingMsg(channel, urn, text).WithReceivedOn(date).WithExternalID(msg.ID).WithContactName(contactNames[msg.From])
				event := h.Backend().CheckExternalIDSeen(ev)

				// write the contact last seen
				h.Backend().WriteContactLastSeen(ctx, ev, date)

				// we had an error downloading media
				if err != nil {
					courier.LogRequestError(r, channel, err)
				}

				// Process WhatsApp metadata (order, nfm_reply, payment_method) with overwrite_message
				var wacMeta *wacMessageMetadata

				if msg.Type == "order" {
					wacMeta = processWACOrderMetadata(msg.Order)
				} else if msg.Interactive.Type == "nfm_reply" {
					var err error
					wacMeta, err = processWACNFMReplyMetadata(msg.Interactive.NFMReply.Name, msg.Interactive.NFMReply.ResponseJSON)
					if err != nil {
						courier.LogRequestError(r, channel, err)
					}
				} else if msg.Interactive.Type == "payment_method" {
					wacMeta = processWACPaymentMethodMetadata(msg.Interactive.PaymentMethod)
				}

				if wacMeta != nil {
					metadataMap := map[string]interface{}{
						wacMeta.Key: wacMeta.Value,
					}
					if err := addMetadataWithOverwrite(event, metadataMap); err != nil {
						courier.LogRequestError(r, channel, err)
					}
				}

				if msg.Referral.Headline != "" {
					// Add referral metadata to both root and overwrite_message
					referralMetadata := map[string]interface{}{
						"referral": msg.Referral,
					}
					if err := addMetadataWithOverwrite(event, referralMetadata); err != nil {
						courier.LogRequestError(r, channel, err)
					}

					if msg.Referral.CtwaClid != "" {
						// Write ctwa data to database
						err := h.Backend().WriteCtwaToDB(ctx, msg.Referral.CtwaClid, urn, date, channel.UUID(), entry.ID)
						if err != nil {
							courier.LogRequestError(r, channel, fmt.Errorf("error writing ctwa data: %v", err))
						}
					}
				}

				if mediaURL != "" {
					event.WithAttachment(mediaURL)
				}

				// Add to the existing metadata, the message context
				if msg.Context != nil {
					// Add context metadata to both root and overwrite_message
					contextMetadata := map[string]interface{}{
						"context": msg.Context,
					}
					if err := addMetadataWithOverwrite(event, contextMetadata); err != nil {
						courier.LogRequestError(r, channel, err)
					}
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

				if (msgStatus == courier.MsgDelivered || msgStatus == courier.MsgRead) &&
					channel.Address() != h.Server().Config().WhatsappCloudDemoAddress {
					// if the channel is the demo channel, we don't need to send the message to the billing system
					urn, err := urns.NewWhatsAppURN(status.RecipientID)
					if err != nil {
						handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
					} else {
						if h.Server().Billing() != nil {
							billingMsg := billing.NewMessage(
								string(urn.Identity()),
								"",
								contactNames[status.RecipientID],
								channel.UUID().String(),
								status.ID,
								time.Now().Format(time.RFC3339),
								"",
								channel.ChannelType().String(),
								"",
								nil,
								nil,
								false,
								"",
								string(msgStatus),
							)
							h.Server().Billing().SendAsync(billingMsg, billing.RoutingKeyUpdate, nil, nil)
						}
					}
				}

				if status.Conversation != nil {
					templateType, isTemplateMessage := waTemplateTypeMapping[status.Conversation.Origin.Type]
					if isTemplateMessage && h.Server().Templates() != nil {
						urn, err := urns.NewWhatsAppURN(status.RecipientID)
						if err != nil {
							handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
						} else {
							statusMsg := templates.NewTemplateStatusMessage(
								string(urn.Identity()),
								channel.UUID().String(),
								status.ID,
								string(msgStatus),
								templateType,
							)
							h.Server().Templates().SendAsync(statusMsg, templates.RoutingKeyStatus, nil, nil)
						}
					}
				}
				events = append(events, event)
				data = append(data, courier.NewStatusData(event))

			}

			for _, call := range change.Value.Calls {
				callsWebhookURL := h.Server().Config().CallsWebhookURL
				callsWebhookToken := h.Server().Config().CallsWebhookToken
				if callsWebhookURL == "" {
					return nil, nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, errors.New("calls webhook url is not set"))
				}

				projectUUID, err := h.Backend().GetProjectUUIDFromChannelUUID(ctx, channel.UUID())
				if err != nil {
					return nil, nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
				}

				contactName := ""
				if len(change.Value.Contacts) > 0 {
					contactName = change.Value.Contacts[0].Profile.Name
				}

				callData := map[string]interface{}{
					"call":            call,
					"project_uuid":    projectUUID,
					"channel_uuid":    channel.UUID().String(),
					"phone_number_id": change.Value.Metadata.PhoneNumberID,
					"name":            contactName,
				}

				callJSON, err := json.Marshal(callData)
				if err != nil {
					return nil, nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
				}
				req, err := http.NewRequest("POST", callsWebhookURL, bytes.NewBuffer(callJSON))
				if err != nil {
					return nil, nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
				}
				if callsWebhookToken != "" {
					req.Header.Set("Authorization", "Bearer "+callsWebhookToken)
				}
				req.Header.Set("Content-Type", "application/json")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					return nil, nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					return nil, nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, errors.New("failed to send calls webhook"))
				}
				data = append(data, courier.NewInfoData(fmt.Sprintf("New whatsapp call received: %s", call.ID)))
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

		if len(entry.Messaging) == 0 {
			if len(entry.Changes) > 0 && entry.Changes[0].Field == "comments" {

				// Check if the comment is from our own channel to prevent loops
				// When we reply to a comment, Instagram sends a webhook about our own reply
				if entry.Changes[0].Value.From.ID == channel.Address() {
					data = append(data, courier.NewInfoData(fmt.Sprintf("ignoring comment from our own channel: %s", entry.Changes[0].Value.From.ID)))
					continue
				}

				// Build IGComment struct and wrapper
				wrapper := struct {
					IGComment IGComment `json:"ig_comment"`
				}{
					IGComment: IGComment{
						Text: entry.Changes[0].Value.Text,
						From: struct {
							ID       string `json:"id,omitempty"`
							Username string `json:"username,omitempty"`
						}{
							ID:       entry.Changes[0].Value.From.ID,
							Username: entry.Changes[0].Value.From.Username,
						},
						Media: struct {
							AdID             string `json:"ad_id,omitempty"`
							ID               string `json:"id,omitempty"`
							MediaProductType string `json:"media_product_type,omitempty"`
							OriginalMediaID  string `json:"original_media_id,omitempty"`
						}{
							ID:               entry.Changes[0].Value.Media.ID,
							AdID:             entry.Changes[0].Value.Media.AdID,
							MediaProductType: entry.Changes[0].Value.Media.MediaProductType,
							OriginalMediaID:  entry.Changes[0].Value.Media.OriginalMediaID,
						},
						Time: entry.Time,
						ID:   entry.Changes[0].Value.ID,
					},
				}

				// Create message from comment
				text := entry.Changes[0].Value.Text
				urn, err := urns.NewInstagramURN(entry.Changes[0].Value.From.ID)
				if err != nil {
					return nil, nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
				}

				ev := h.Backend().NewIncomingMsg(channel, urn, text).WithExternalID(entry.Changes[0].Value.ID).WithReceivedOn(time.Unix(0, entry.Time*1000000).UTC())
				event := h.Backend().CheckExternalIDSeen(ev)

				// Add IG comment metadata to both root and overwrite_message
				igCommentMetadata := map[string]interface{}{
					"ig_comment": wrapper.IGComment,
				}
				if err := addMetadataWithOverwrite(event, igCommentMetadata); err != nil {
					courier.LogRequestError(r, channel, err)
				}
				err = h.Backend().WriteMsg(ctx, event)
				if err != nil {
					return nil, nil, err
				}

				h.Backend().WriteExternalIDSeen(event)
				events = append(events, event)
				data = append(data, courier.NewMsgReceiveData(event))

				// return events, data, nil

			}
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
	} else if msg.Channel().ChannelType() == "WCD" {
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
	if msg.ResponseToExternalID() != "" && msg.IGCommentID() == "" {
		payload.MessagingType = "RESPONSE"
	} else if topic != "" || msg.IGTag() != "" {
		payload.MessagingType = "MESSAGE_TAG"
		if topic != "" {
			payload.Tag = tagByTopic[topic]
		} else {
			payload.Tag = msg.IGTag()
		}
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

	} else if msg.IGCommentID() != "" && msg.Text() != "" {
		var baseURL *url.URL
		form := url.Values{}

		commentID := msg.IGCommentID()
		if msg.IGResponseType() == "comment" {
			baseURL, _ = url.Parse(fmt.Sprintf(graphURL+"%s/replies", commentID))
			form.Set("message", msg.Text())
		} else if msg.IGResponseType() == "dm_comment" {
			pageID := strconv.Itoa(msg.Channel().IntConfigForKey(courier.ConfigPageID, 0))
			baseURL, _ = url.Parse(fmt.Sprintf(graphURL+"%s/messages", pageID))
			query := baseURL.Query()
			query.Set("recipient", fmt.Sprintf("{comment_id:%s}", commentID))
			query.Set("message", fmt.Sprintf("{\"text\":\"%s\"}", strings.TrimSpace(msg.Text())))
			baseURL.RawQuery = query.Encode()
		}

		query := baseURL.Query()
		query.Set("access_token", accessToken)
		baseURL.RawQuery = query.Encode()

		req, _ := http.NewRequest(http.MethodPost, baseURL.String(), strings.NewReader(form.Encode()))
		req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

		rr, err := utils.MakeHTTPRequest(req)

		log := courier.NewChannelLogFromRR("Instagram Comment Reply", msg.Channel(), msg.ID(), rr)
		if err != nil {
			log = log.WithError("Instagram Comment Reply Error", err)
			status.AddLog(log)
			return status, err
		}
		status.AddLog(log)

		externalID, err := jsonparser.GetString(rr.Body, "id")
		if err != nil {
			// ID doesn't exist, let's try message_id
			externalID, err = jsonparser.GetString(rr.Body, "message_id")
			if err != nil {
				log.WithError("Message Send Error", errors.Errorf("unable to get id or message_id from body"))
				return status, nil
			}
		}

		status.SetStatus(courier.MsgWired)
		status.SetExternalID(externalID)

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

type wacMTAction struct {
	OrderDetails *wacOrderDetails `json:"order_details,omitempty"`
}

type wacParam struct {
	Type        string       `json:"type"`
	Text        string       `json:"text,omitempty"`
	Payload     string       `json:"payload,omitempty"`
	PhoneNumber string       `json:"phone_number,omitempty"`
	URL         string       `json:"url,omitempty"`
	Image       *wacMTMedia  `json:"image,omitempty"`
	Document    *wacMTMedia  `json:"document,omitempty"`
	Video       *wacMTMedia  `json:"video,omitempty"`
	Action      *wacMTAction `json:"action,omitempty"`
}

type wacComponent struct {
	Type    string             `json:"type"`
	SubType string             `json:"sub_type,omitempty"`
	Index   *int               `json:"index,omitempty"`
	Params  []*wacParam        `json:"parameters,omitempty"`
	Cards   []*wacCarouselCard `json:"cards,omitempty"`
}

// wacCarouselCard represents a card in a carousel template
type wacCarouselCard struct {
	CardIndex  int             `json:"card_index"`
	Components []*wacComponent `json:"components"`
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

type wacInteractiveActionParams interface {
	~map[string]any | wacOrderDetails
}

type wacInteractive[P wacInteractiveActionParams] struct {
	Type   string `json:"type"`
	Header *struct {
		Type     string      `json:"type"`
		Text     string      `json:"text,omitempty"`
		Video    *wacMTMedia `json:"video,omitempty"`
		Image    *wacMTMedia `json:"image,omitempty"`
		Document *wacMTMedia `json:"document,omitempty"`
	} `json:"header,omitempty"`
	Body struct {
		Text string `json:"text"`
	} `json:"body,omitempty"`
	Footer *struct {
		Text string `json:"text,omitempty"`
	} `json:"footer,omitempty"`
	Action *struct {
		Button            string         `json:"button,omitempty"`
		Sections          []wacMTSection `json:"sections,omitempty"`
		Buttons           []wacMTButton  `json:"buttons,omitempty"`
		CatalogID         string         `json:"catalog_id,omitempty"`
		ProductRetailerID string         `json:"product_retailer_id,omitempty"`
		Name              string         `json:"name,omitempty"`
		Parameters        P              `json:"parameters,omitempty"`
	} `json:"action,omitempty"`
}

type wacMTPayload[P wacInteractiveActionParams] struct {
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

	Interactive *wacInteractive[P] `json:"interactive,omitempty"`

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
type wacMTProductItem struct {
	ProductRetailerID string `json:"product_retailer_id" validate:"required"`
}

type wacOrderDetailsPixDynamicCode struct {
	Code         string `json:"code" validate:"required"`
	MerchantName string `json:"merchant_name" validate:"required"`
	Key          string `json:"key" validate:"required"`
	KeyType      string `json:"key_type" validate:"required"`
}

type wacOrderDetailsPaymentLink struct {
	URI string `json:"uri" validate:"required"`
}

type wacOrderDetailsOffsiteCardPay struct {
	LastFourDigits string `json:"last_four_digits" validate:"required"`
	CredentialID   string `json:"credential_id" validate:"required"`
}

type wacOrderDetailsPaymentSetting struct {
	Type           string                         `json:"type" validate:"required"`
	PaymentLink    *wacOrderDetailsPaymentLink    `json:"payment_link,omitempty"`
	PixDynamicCode *wacOrderDetailsPixDynamicCode `json:"pix_dynamic_code,omitempty"`
	OffsiteCardPay *wacOrderDetailsOffsiteCardPay `json:"offsite_card_pay,omitempty"`
}

type wacOrderDetails struct {
	ReferenceID     string                          `json:"reference_id" validate:"required"`
	Type            string                          `json:"type" validate:"required"`
	PaymentType     string                          `json:"payment_type" validate:"required"`
	PaymentSettings []wacOrderDetailsPaymentSetting `json:"payment_settings" validate:"required"`
	Currency        string                          `json:"currency" validate:"required"`
	TotalAmount     wacAmountWithOffset             `json:"total_amount" validate:"required"`
	Order           wacOrder                        `json:"order" validate:"required"`
}

type wacOrder struct {
	Status    string               `json:"status" validate:"required"`
	CatalogID string               `json:"catalog_id,omitempty"`
	Items     []courier.OrderItem  `json:"items" validate:"required"`
	Subtotal  wacAmountWithOffset  `json:"subtotal" validate:"required"`
	Tax       wacAmountWithOffset  `json:"tax" validate:"required"`
	Shipping  *wacAmountWithOffset `json:"shipping,omitempty"`
	Discount  *wacAmountWithOffset `json:"discount,omitempty"`
}

type wacAmountWithOffset struct {
	Value               int    `json:"value"`
	Offset              int    `json:"offset"`
	Description         string `json:"description,omitempty"`
	DiscountProgramName string `json:"discount_program_name,omitempty"`
}

type wacFlowActionPayload struct {
	Data   map[string]interface{} `json:"data,omitempty"`
	Screen string                 `json:"screen"`
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

	// Set the base URL and path based on whether we're using marketing messages or not
	demoURL := msg.Channel().StringConfigForKey("demo_url", "")

	if demoURL != "" {
		graphURL = demoURL
	}

	// Check if we should use marketing messages
	mmliteEnabled := msg.Channel().BoolConfigForKey("mmlite", false)

	// Check if the message is a marketing template by examining metadata
	isMarketingTemplate := false
	var err error
	templating, err := h.getTemplate(msg)
	if err == nil && templating != nil {
		// Check if template category is "MARKETING" in the template category
		// This is how Meta identifies marketing templates
		isMarketingTemplate = strings.ToUpper(templating.Template.Category) == "MARKETING"
	} else if err != nil {
		return nil, errors.Wrapf(err, "unable to decode template: %s for channel: %s", string(msg.Metadata()), msg.Channel().UUID())
	}

	// Only use marketing messages endpoint if mmlite is enabled AND it's a marketing template
	useMarketingMessages := mmliteEnabled && isMarketingTemplate

	base, _ := url.Parse(graphURL)
	var path *url.URL
	if useMarketingMessages {
		path, _ = url.Parse(fmt.Sprintf("/%s/marketing_messages", msg.Channel().Address()))
	} else {
		path, _ = url.Parse(fmt.Sprintf("/%s/messages", msg.Channel().Address()))
	}
	wacPhoneURL := base.ResolveReference(path)

	status := h.Backend().NewMsgStatusForID(msg.Channel(), msg.ID(), courier.MsgErrored)

	msgParts := make([]string, 0)
	if msg.Text() != "" {
		if len(msg.ListMessage().ListItems) > 0 || len(msg.QuickReplies()) > 0 || msg.InteractionType() == "location" {
			msgParts = handlers.SplitMsgByChannel(msg.Channel(), msg.Text(), maxMsgLengthInteractiveWAC)
		} else {
			msgParts = handlers.SplitMsgByChannel(msg.Channel(), msg.Text(), maxMsgLengthWAC)
		}
	}
	qrs := msg.QuickReplies()

	var payloadAudio wacMTPayload[map[string]any]

	for i := 0; i < len(msgParts)+len(msg.Attachments()); i++ {
		payload := wacMTPayload[map[string]any]{MessagingProduct: "whatsapp", RecipientType: "individual", To: msg.URN().Path()}

		// do we have a template?
		if templating != nil || len(msg.Attachments()) == 0 {
			if templating != nil {
				payload.Type = "template"
				template := wacTemplate{Name: templating.Template.Name, Language: &wacLanguage{Policy: "deterministic", Code: templating.Language}}
				payload.Template = &template

				// Build all template components
				components, err := h.buildTemplateComponents(msg, templating, accessToken, status, start)
				if err != nil {
					return status, err
				}
				template.Components = components

			} else {
				if i < (len(msgParts) + len(msg.Attachments()) - 1) { // TODO: verify if this is correct. If any case makes the "if be true"
					payload.Type = "text"
					if strings.Contains(msgParts[i-len(msg.Attachments())], "https://") || strings.Contains(msgParts[i-len(msg.Attachments())], "http://") {
						text := wacText{}
						text.PreviewURL = true
						text.Body = msgParts[i-len(msg.Attachments())]
						payload.Text = &text
					} else {
						payload.Text = &wacText{Body: msgParts[i-len(msg.Attachments())]}
					}
				} else {
					if len(qrs) > 0 || len(msg.ListMessage().ListItems) > 0 {
						payload.Type = "interactive"
						// We can use buttons
						if len(qrs) > 0 && len(qrs) <= 3 {
							interactive := wacInteractive[map[string]any]{
								Type: "button",
								Body: struct {
									Text string "json:\"text\""
								}{Text: msgParts[i-len(msg.Attachments())]},
							}

							if msg.Footer() != "" {
								interactive.Footer = &struct {
									Text string "json:\"text,omitempty\""
								}{Text: parseBacklashes(msg.Footer())}
							}

							if msg.HeaderText() != "" {
								interactive.Header = &struct {
									Type     string      "json:\"type\""
									Text     string      "json:\"text,omitempty\""
									Video    *wacMTMedia "json:\"video,omitempty\""
									Image    *wacMTMedia "json:\"image,omitempty\""
									Document *wacMTMedia "json:\"document,omitempty\""
								}{Type: "text", Text: parseBacklashes(msg.HeaderText())}
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
								Button            string                 "json:\"button,omitempty\""
								Sections          []wacMTSection         "json:\"sections,omitempty\""
								Buttons           []wacMTButton          "json:\"buttons,omitempty\""
								CatalogID         string                 "json:\"catalog_id,omitempty\""
								ProductRetailerID string                 "json:\"product_retailer_id,omitempty\""
								Name              string                 "json:\"name,omitempty\""
								Parameters        map[string]interface{} "json:\"parameters,omitempty\""
							}{Buttons: btns}
							payload.Interactive = &interactive
						} else if len(qrs) <= 10 || len(msg.ListMessage().ListItems) > 0 {
							interactive := wacInteractive[map[string]any]{
								Type: "list",
								Body: struct {
									Text string "json:\"text\""
								}{Text: msgParts[i-len(msg.Attachments())]},
							}

							var section wacMTSection

							if len(qrs) > 0 {
								section = wacMTSection{
									Rows: make([]wacMTSectionRow, len(qrs)),
								}
								for i, qr := range qrs {
									text := parseBacklashes(qr)
									section.Rows[i] = wacMTSectionRow{
										ID:    fmt.Sprint(i),
										Title: text,
									}
								}
							} else if len(msg.ListMessage().ListItems) > 0 {
								section = wacMTSection{
									Rows: make([]wacMTSectionRow, len(msg.ListMessage().ListItems)),
								}
								for i, listItem := range msg.ListMessage().ListItems {
									titleText := parseBacklashes(listItem.Title)
									descriptionText := parseBacklashes(listItem.Description)
									section.Rows[i] = wacMTSectionRow{
										ID:          listItem.UUID,
										Title:       titleText,
										Description: descriptionText,
									}
								}
								if msg.Footer() != "" {
									interactive.Footer = &struct {
										Text string "json:\"text,omitempty\""
									}{Text: parseBacklashes(msg.Footer())}
								}

								if msg.HeaderText() != "" {
									interactive.Header = &struct {
										Type     string      "json:\"type\""
										Text     string      "json:\"text,omitempty\""
										Video    *wacMTMedia "json:\"video,omitempty\""
										Image    *wacMTMedia "json:\"image,omitempty\""
										Document *wacMTMedia "json:\"document,omitempty\""
									}{Type: "text", Text: parseBacklashes(msg.HeaderText())}
								}
							}

							interactive.Action = &struct {
								Button            string                 "json:\"button,omitempty\""
								Sections          []wacMTSection         "json:\"sections,omitempty\""
								Buttons           []wacMTButton          "json:\"buttons,omitempty\""
								CatalogID         string                 "json:\"catalog_id,omitempty\""
								ProductRetailerID string                 "json:\"product_retailer_id,omitempty\""
								Name              string                 "json:\"name,omitempty\""
								Parameters        map[string]interface{} "json:\"parameters,omitempty\""
							}{Button: "Menu", Sections: []wacMTSection{
								section,
							}}

							if msg.ListMessage().ButtonText != "" {
								interactive.Action.Button = msg.ListMessage().ButtonText
							} else if msg.TextLanguage() != "" {
								interactive.Action.Button = languageMenuMap[msg.TextLanguage()]
							}

							payload.Interactive = &interactive
						} else {
							return nil, fmt.Errorf("too many quick replies WAC supports only up to 10 quick replies")
						}
					} else if msg.InteractionType() == "location" {
						payload.Type = "interactive"
						interactive := wacInteractive[map[string]any]{
							Type: "location_request_message",
							Body: struct {
								Text string "json:\"text\""
							}{Text: msgParts[i-len(msg.Attachments())]},
							Action: &struct {
								Button            string                 "json:\"button,omitempty\""
								Sections          []wacMTSection         "json:\"sections,omitempty\""
								Buttons           []wacMTButton          "json:\"buttons,omitempty\""
								CatalogID         string                 "json:\"catalog_id,omitempty\""
								ProductRetailerID string                 "json:\"product_retailer_id,omitempty\""
								Name              string                 "json:\"name,omitempty\""
								Parameters        map[string]interface{} "json:\"parameters,omitempty\""
							}{Name: "send_location"},
						}

						payload.Interactive = &interactive
					} else if msg.InteractionType() == "cta_url" {
						if ctaMessage := msg.CTAMessage(); ctaMessage != nil {
							payload.Type = "interactive"
							interactive := wacInteractive[map[string]any]{
								Type: "cta_url",
								Body: struct {
									Text string "json:\"text\""
								}{Text: msgParts[i-len(msg.Attachments())]},
								Action: &struct {
									Button            string                 "json:\"button,omitempty\""
									Sections          []wacMTSection         "json:\"sections,omitempty\""
									Buttons           []wacMTButton          "json:\"buttons,omitempty\""
									CatalogID         string                 "json:\"catalog_id,omitempty\""
									ProductRetailerID string                 "json:\"product_retailer_id,omitempty\""
									Name              string                 "json:\"name,omitempty\""
									Parameters        map[string]interface{} "json:\"parameters,omitempty\""
								}{
									Name: "cta_url",
									Parameters: map[string]interface{}{
										"display_text": parseBacklashes(ctaMessage.DisplayText),
										"url":          ctaMessage.URL,
									},
								},
							}
							if msg.Footer() != "" {
								interactive.Footer = &struct {
									Text string "json:\"text,omitempty\""
								}{Text: parseBacklashes(msg.Footer())}
							}

							if msg.HeaderText() != "" {
								interactive.Header = &struct {
									Type     string      "json:\"type\""
									Text     string      "json:\"text,omitempty\""
									Video    *wacMTMedia "json:\"video,omitempty\""
									Image    *wacMTMedia "json:\"image,omitempty\""
									Document *wacMTMedia "json:\"document,omitempty\""
								}{Type: "text", Text: parseBacklashes(msg.HeaderText())}
							}
							payload.Interactive = &interactive
						}
					} else if msg.InteractionType() == "flow_msg" {
						if flowMessage := msg.FlowMessage(); flowMessage != nil {
							payload.Type = "interactive"
							interactive := wacInteractive[map[string]any]{
								Type: "flow",
								Body: struct {
									Text string "json:\"text\""
								}{Text: msgParts[i-len(msg.Attachments())]},
								Action: &struct {
									Button            string                 "json:\"button,omitempty\""
									Sections          []wacMTSection         "json:\"sections,omitempty\""
									Buttons           []wacMTButton          "json:\"buttons,omitempty\""
									CatalogID         string                 "json:\"catalog_id,omitempty\""
									ProductRetailerID string                 "json:\"product_retailer_id,omitempty\""
									Name              string                 "json:\"name,omitempty\""
									Parameters        map[string]interface{} "json:\"parameters,omitempty\""
								}{
									Name: "flow",
									Parameters: map[string]interface{}{
										"mode":                 flowMessage.FlowMode,
										"flow_message_version": "3",
										"flow_token":           uuids.New(),
										"flow_id":              flowMessage.FlowID,
										"flow_cta":             flowMessage.FlowCTA,
										"flow_action":          "navigate",
										"flow_action_payload": wacFlowActionPayload{
											Screen: flowMessage.FlowScreen,
											Data:   flowMessage.FlowData,
										},
									},
								},
							}
							if msg.Footer() != "" {
								interactive.Footer = &struct {
									Text string "json:\"text,omitempty\""
								}{Text: parseBacklashes(msg.Footer())}
							}

							if msg.HeaderText() != "" {
								interactive.Header = &struct {
									Type     string      "json:\"type\""
									Text     string      "json:\"text,omitempty\""
									Video    *wacMTMedia "json:\"video,omitempty\""
									Image    *wacMTMedia "json:\"image,omitempty\""
									Document *wacMTMedia "json:\"document,omitempty\""
								}{Type: "text", Text: parseBacklashes(msg.HeaderText())}
							}
							payload.Interactive = &interactive
						}
					} else if msg.InteractionType() == "order_details" {
						fmt.Println("facebookapp.go - msg.InteractionType() == 'order_details'")
						if orderDetails := msg.OrderDetailsMessage(); orderDetails != nil {
							fmt.Println("facebookapp.go - orderDetails != nil")
							payload.Type = "interactive"

							paymentSettings, catalogID, orderTax, orderShipping, orderDiscount := mountOrderInfo(msg)

							mountedOrderDetails := mountOrderDetails(msg, paymentSettings, catalogID, orderTax, orderShipping, orderDiscount)
							if mountedOrderDetails == nil {
								return status, fmt.Errorf("failed to mount order details")
							}

							interactive := wacInteractive[wacOrderDetails]{
								Type: "order_details",
								Body: struct {
									Text string "json:\"text\""
								}{Text: msgParts[i-len(msg.Attachments())]},
								Action: &struct {
									Button            string          "json:\"button,omitempty\""
									Sections          []wacMTSection  "json:\"sections,omitempty\""
									Buttons           []wacMTButton   "json:\"buttons,omitempty\""
									CatalogID         string          "json:\"catalog_id,omitempty\""
									ProductRetailerID string          "json:\"product_retailer_id,omitempty\""
									Name              string          "json:\"name,omitempty\""
									Parameters        wacOrderDetails "json:\"parameters,omitempty\""
								}{
									Name:       "review_and_pay",
									Parameters: *mountedOrderDetails,
								},
							}
							if msg.Footer() != "" {
								interactive.Footer = &struct {
									Text string "json:\"text,omitempty\""
								}{Text: parseBacklashes(msg.Footer())}
							}

							payload.Interactive = castInteractive[wacOrderDetails, map[string]any](interactive)
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

		} else if (i < len(msg.Attachments()) && len(qrs) == 0 && len(msg.ListMessage().ListItems) == 0 && msg.InteractionType() != "order_details") ||
			len(qrs) > 3 && i < len(msg.Attachments()) ||
			len(msg.ListMessage().ListItems) > 0 && i < len(msg.Attachments()) {
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
			if len(msgParts) == 1 && (attType != "audio" && attFormat != "webp") && len(msg.Attachments()) == 1 && len(msg.QuickReplies()) == 0 && len(msg.ListMessage().ListItems) == 0 {
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
		} else { // have attachment
			if len(qrs) > 0 || len(msg.ListMessage().ListItems) > 0 {
				payload.Type = "interactive"
				// We can use buttons
				if len(qrs) <= 3 && len(msg.ListMessage().ListItems) == 0 {
					hasCaption = true

					if len(msgParts) == 0 {
						return nil, fmt.Errorf("message body cannot be empty")
					}

					interactive := wacInteractive[map[string]any]{
						Type: "button",
						Body: struct {
							Text string "json:\"text\""
						}{Text: msgParts[i]},
					}

					if len(msg.Attachments()) > 0 {
						attType, attURL := handlers.SplitAttachment(msg.Attachments()[i])
						fileURL := attURL
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
						media := wacMTMedia{ID: mediaID, Link: attURL}
						switch attType {
						case "image":
							interactive.Header = &struct {
								Type     string      "json:\"type\""
								Text     string      "json:\"text,omitempty\""
								Video    *wacMTMedia "json:\"video,omitempty\""
								Image    *wacMTMedia "json:\"image,omitempty\""
								Document *wacMTMedia "json:\"document,omitempty\""
							}{Type: "image", Image: &media}
						case "video":
							interactive.Header = &struct {
								Type     string      "json:\"type\""
								Text     string      "json:\"text,omitempty\""
								Video    *wacMTMedia "json:\"video,omitempty\""
								Image    *wacMTMedia "json:\"image,omitempty\""
								Document *wacMTMedia "json:\"document,omitempty\""
							}{Type: "video", Video: &media}
						case "document":
							filename, err := utils.BasePathForURL(fileURL)
							if err != nil {
								return nil, err
							}
							media.Filename = filename
							interactive.Header = &struct {
								Type     string      "json:\"type\""
								Text     string      "json:\"text,omitempty\""
								Video    *wacMTMedia "json:\"video,omitempty\""
								Image    *wacMTMedia "json:\"image,omitempty\""
								Document *wacMTMedia "json:\"document,omitempty\""
							}{Type: "document", Document: &media}
						case "audio":
							var zeroIndex bool
							if i == 0 {
								zeroIndex = true
							}
							payloadAudio = wacMTPayload[map[string]any]{MessagingProduct: "whatsapp", RecipientType: "individual", To: msg.URN().Path(), Type: "audio", Audio: &wacMTMedia{ID: mediaID, Link: attURL}}
							status, _, err := requestWAC(payloadAudio, token, msg, status, wacPhoneURL, zeroIndex, useMarketingMessages)
							if err != nil {
								return status, nil
							}
						default:
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
						text := parseBacklashes(qr)
						btns[i].Reply.Title = text
					}
					interactive.Action = &struct {
						Button            string                 "json:\"button,omitempty\""
						Sections          []wacMTSection         "json:\"sections,omitempty\""
						Buttons           []wacMTButton          "json:\"buttons,omitempty\""
						CatalogID         string                 "json:\"catalog_id,omitempty\""
						ProductRetailerID string                 "json:\"product_retailer_id,omitempty\""
						Name              string                 "json:\"name,omitempty\""
						Parameters        map[string]interface{} "json:\"parameters,omitempty\""
					}{Buttons: btns}
					payload.Interactive = &interactive
					if msg.Footer() != "" {
						payload.Interactive.Footer = &struct {
							Text string "json:\"text,omitempty\""
						}{Text: parseBacklashes(msg.Footer())}
					}
				} else if len(qrs) <= 10 || len(msg.ListMessage().ListItems) > 0 {
					interactive := wacInteractive[map[string]any]{
						Type: "list",
						Body: struct {
							Text string "json:\"text\""
						}{Text: msgParts[i-len(msg.Attachments())]},
					}

					var section wacMTSection

					if len(qrs) > 0 {
						section = wacMTSection{
							Rows: make([]wacMTSectionRow, len(qrs)),
						}
						for i, qr := range qrs {
							text := parseBacklashes(qr)
							section.Rows[i] = wacMTSectionRow{
								ID:    fmt.Sprint(i),
								Title: text,
							}
						}
					} else {
						section = wacMTSection{
							Rows: make([]wacMTSectionRow, len(msg.ListMessage().ListItems)),
						}
						for i, listItem := range msg.ListMessage().ListItems {
							titleText := parseBacklashes(listItem.Title)
							descriptionText := parseBacklashes(listItem.Description)
							section.Rows[i] = wacMTSectionRow{
								ID:          listItem.UUID,
								Title:       titleText,
								Description: descriptionText,
							}
						}
						if msg.Footer() != "" {
							interactive.Footer = &struct {
								Text string "json:\"text,omitempty\""
							}{Text: parseBacklashes(msg.Footer())}
						}
					}

					interactive.Action = &struct {
						Button            string                 "json:\"button,omitempty\""
						Sections          []wacMTSection         "json:\"sections,omitempty\""
						Buttons           []wacMTButton          "json:\"buttons,omitempty\""
						CatalogID         string                 "json:\"catalog_id,omitempty\""
						ProductRetailerID string                 "json:\"product_retailer_id,omitempty\""
						Name              string                 "json:\"name,omitempty\""
						Parameters        map[string]interface{} "json:\"parameters,omitempty\""
					}{Button: "Menu", Sections: []wacMTSection{
						section,
					}}

					if msg.ListMessage().ButtonText != "" {
						interactive.Action.Button = msg.ListMessage().ButtonText
					} else if msg.TextLanguage() != "" {
						interactive.Action.Button = languageMenuMap[msg.TextLanguage()]
					}

					payload.Interactive = &interactive
				} else {
					return nil, fmt.Errorf("too many quick replies WAC supports only up to 10 quick replies")
				}
			} else if msg.InteractionType() == "location" { // Unreachable due to else if sending only the attachment
				interactive := wacInteractive[map[string]any]{Type: "location_request_message", Body: struct {
					Text string "json:\"text\""
				}{Text: msgParts[i-len(msg.Attachments())]}, Action: &struct {
					Button            string                 "json:\"button,omitempty\""
					Sections          []wacMTSection         "json:\"sections,omitempty\""
					Buttons           []wacMTButton          "json:\"buttons,omitempty\""
					CatalogID         string                 "json:\"catalog_id,omitempty\""
					ProductRetailerID string                 "json:\"product_retailer_id,omitempty\""
					Name              string                 "json:\"name,omitempty\""
					Parameters        map[string]interface{} "json:\"parameters,omitempty\""
				}{Name: "send_location"}}

				payload.Interactive = &interactive
			} else if msg.InteractionType() == "cta_url" { // Unreachable due to else if sending only the attachment
				if ctaMessage := msg.CTAMessage(); ctaMessage != nil {
					interactive := wacInteractive[map[string]any]{
						Type: "cta_url",
						Body: struct {
							Text string "json:\"text\""
						}{Text: msgParts[i-len(msg.Attachments())]},
						Action: &struct {
							Button            string                 "json:\"button,omitempty\""
							Sections          []wacMTSection         "json:\"sections,omitempty\""
							Buttons           []wacMTButton          "json:\"buttons,omitempty\""
							CatalogID         string                 "json:\"catalog_id,omitempty\""
							ProductRetailerID string                 "json:\"product_retailer_id,omitempty\""
							Name              string                 "json:\"name,omitempty\""
							Parameters        map[string]interface{} "json:\"parameters,omitempty\""
						}{
							Name: "cta_url",
							Parameters: map[string]interface{}{
								"display_text": parseBacklashes(ctaMessage.DisplayText),
								"url":          ctaMessage.URL,
							},
						},
					}

					if msg.Footer() != "" {
						interactive.Footer = &struct {
							Text string "json:\"text,omitempty\""
						}{Text: parseBacklashes(msg.Footer())}
					}

					if msg.HeaderText() != "" {
						interactive.Header = &struct {
							Type     string      "json:\"type\""
							Text     string      "json:\"text,omitempty\""
							Video    *wacMTMedia "json:\"video,omitempty\""
							Image    *wacMTMedia "json:\"image,omitempty\""
							Document *wacMTMedia "json:\"document,omitempty\""
						}{Type: "text", Text: parseBacklashes(msg.HeaderText())}
					}
					payload.Interactive = &interactive
				}
			} else if msg.InteractionType() == "flow_msg" { // Unreachable due to else if sending only the attachment
				if flowMessage := msg.FlowMessage(); flowMessage != nil {
					interactive := wacInteractive[map[string]any]{
						Type: "flow",
						Body: struct {
							Text string "json:\"text\""
						}{Text: msgParts[i-len(msg.Attachments())]},
						Action: &struct {
							Button            string                 "json:\"button,omitempty\""
							Sections          []wacMTSection         "json:\"sections,omitempty\""
							Buttons           []wacMTButton          "json:\"buttons,omitempty\""
							CatalogID         string                 "json:\"catalog_id,omitempty\""
							ProductRetailerID string                 "json:\"product_retailer_id,omitempty\""
							Name              string                 "json:\"name,omitempty\""
							Parameters        map[string]interface{} "json:\"parameters,omitempty\""
						}{
							Name: "flow",
							Parameters: map[string]interface{}{
								"mode":                 flowMessage.FlowMode,
								"flow_message_version": "3",
								"flow_token":           uuids.New(),
								"flow_id":              flowMessage.FlowID,
								"flow_cta":             flowMessage.FlowCTA,
								"flow_action":          "navigate",
								"flow_action_payload": wacFlowActionPayload{
									Screen: flowMessage.FlowScreen,
									Data:   flowMessage.FlowData,
								},
							},
						},
					}

					if msg.Footer() != "" {
						interactive.Footer = &struct {
							Text string "json:\"text,omitempty\""
						}{Text: parseBacklashes(msg.Footer())}
					}

					if msg.HeaderText() != "" {
						interactive.Header = &struct {
							Type     string      "json:\"type\""
							Text     string      "json:\"text,omitempty\""
							Video    *wacMTMedia "json:\"video,omitempty\""
							Image    *wacMTMedia "json:\"image,omitempty\""
							Document *wacMTMedia "json:\"document,omitempty\""
						}{Type: "text", Text: parseBacklashes(msg.HeaderText())}
					}
					payload.Interactive = &interactive
				}
			} else if msg.InteractionType() == "order_details" {
				if orderDetails := msg.OrderDetailsMessage(); orderDetails != nil {
					hasCaption = true
					payload.Type = "interactive"

					paymentSettings, catalogID, orderTax, orderShipping, orderDiscount := mountOrderInfo(msg)

					mountedOrderDetails := mountOrderDetails(msg, paymentSettings, catalogID, orderTax, orderShipping, orderDiscount)
					if mountedOrderDetails == nil {
						return status, fmt.Errorf("failed to mount order details")
					}

					interactive := wacInteractive[wacOrderDetails]{
						Type: "order_details",
						Body: struct {
							Text string "json:\"text\""
						}{Text: msgParts[i]},
						Action: &struct {
							Button            string          "json:\"button,omitempty\""
							Sections          []wacMTSection  "json:\"sections,omitempty\""
							Buttons           []wacMTButton   "json:\"buttons,omitempty\""
							CatalogID         string          "json:\"catalog_id,omitempty\""
							ProductRetailerID string          "json:\"product_retailer_id,omitempty\""
							Name              string          "json:\"name,omitempty\""
							Parameters        wacOrderDetails "json:\"parameters,omitempty\""
						}{
							Name:       "review_and_pay",
							Parameters: *mountedOrderDetails,
						},
					}
					if msg.Footer() != "" {
						interactive.Footer = &struct {
							Text string "json:\"text,omitempty\""
						}{Text: parseBacklashes(msg.Footer())}
					}

					if len(msg.Attachments()) > 0 {
						attType, attURL := handlers.SplitAttachment(msg.Attachments()[i])
						attType = strings.Split(attType, "/")[0]
						media := wacMTMedia{Link: attURL}

						if attType == "image" {
							interactive.Header = &struct {
								Type     string      "json:\"type\""
								Text     string      "json:\"text,omitempty\""
								Video    *wacMTMedia "json:\"video,omitempty\""
								Image    *wacMTMedia "json:\"image,omitempty\""
								Document *wacMTMedia "json:\"document,omitempty\""
							}{Type: "image", Image: &media}
						} else {
							return nil, fmt.Errorf("interactive order details message does not support attachments other than images")
						}
					}

					payload.Interactive = castInteractive[wacOrderDetails, map[string]any](interactive)
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

		status, respPayload, err := requestWAC(payload, token, msg, status, wacPhoneURL, zeroIndex, useMarketingMessages)
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
				// Instead of updating the existing URN, add a new URN to the contact
				contact, err := h.Backend().GetContact(ctx, msg.Channel(), msg.URN(), "", "")
				if err != nil {
					log := courier.NewChannelLogFromError("unable to get contact for new URN", msg.Channel(), msg.ID(), time.Since(start), err)
					status.AddLog(log)
				} else {
					_, err = h.Backend().AddURNtoContact(ctx, msg.Channel(), contact, toUpdateURN)
					if err != nil {
						log := courier.NewChannelLogFromError("unable to add new URN to contact", msg.Channel(), msg.ID(), time.Since(start), err)
						status.AddLog(log)
					}
					hasNewURN = true
				}
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

		payload := wacMTPayload[map[string]any]{MessagingProduct: "whatsapp", RecipientType: "individual", To: msg.URN().Path()}

		payload.Type = "interactive"

		products := msg.Products()

		isUnitaryProduct := true
		var unitaryProduct string
		for _, product := range products {
			retailerIDs := extractRetailerIDs(product)
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

		interactive := wacInteractive[map[string]any]{
			Type: interactiveType,
		}

		interactive.Body = struct {
			Text string `json:"text"`
		}{
			Text: msg.Body(),
		}

		if msg.Header() != "" && !isUnitaryProduct && !msg.SendCatalog() {
			interactive.Header = &struct {
				Type     string      `json:"type"`
				Text     string      `json:"text,omitempty"`
				Video    *wacMTMedia `json:"video,omitempty"`
				Image    *wacMTMedia `json:"image,omitempty"`
				Document *wacMTMedia `json:"document,omitempty"`
			}{
				Type: "text",
				Text: msg.Header(),
			}
		}

		if msg.Footer() != "" {
			interactive.Footer = &struct {
				Text string "json:\"text,omitempty\""
			}{
				Text: parseBacklashes(msg.Footer()),
			}
		}

		if msg.SendCatalog() {
			interactive.Action = &struct {
				Button            string                 `json:"button,omitempty"`
				Sections          []wacMTSection         `json:"sections,omitempty"`
				Buttons           []wacMTButton          `json:"buttons,omitempty"`
				CatalogID         string                 `json:"catalog_id,omitempty"`
				ProductRetailerID string                 `json:"product_retailer_id,omitempty"`
				Name              string                 `json:"name,omitempty"`
				Parameters        map[string]interface{} "json:\"parameters,omitempty\""
			}{
				Name: "catalog_message",
			}
			payload.Interactive = &interactive
			status, _, err := requestWAC(payload, accessToken, msg, status, wacPhoneURL, true, useMarketingMessages)
			if err != nil {
				return status, err
			}
		} else if len(products) > 0 {
			if !isUnitaryProduct {
				const maxSectionsPerMsg = 10
				const maxProductsPerMsg = 30

				actions := [][]wacMTSection{}
				sections := []wacMTSection{}
				totalProductsPerMsg := 0

				for _, product := range products {
					retailerIDs := extractRetailerIDs(product)

					title := product["product"].(string)
					if title == "product_retailer_id" {
						title = "items"
					}
					if len(title) > 24 {
						title = title[:24]
					}

					var sproducts []wacMTProductItem

					for _, p := range retailerIDs {
						// Check if adding this product would exceed the limit
						if totalProductsPerMsg >= maxProductsPerMsg {
							// Save current products to section before starting new message
							if len(sproducts) > 0 {
								sections = append(sections, wacMTSection{Title: title, ProductItems: sproducts})
								sproducts = []wacMTProductItem{}
							}

							// Save current sections to actions and restart for new message
							if len(sections) > 0 {
								actions = append(actions, sections)
								sections = []wacMTSection{}
								totalProductsPerMsg = 0
							}
						}

						// Check if adding this section would exceed the sections limit
						// We need to check before adding a new section
						if len(sproducts) == 0 && len(sections) >= maxSectionsPerMsg {
							// Save current sections to actions and restart for new message
							if len(sections) > 0 {
								actions = append(actions, sections)
								sections = []wacMTSection{}
								totalProductsPerMsg = 0
							}
						}

						sproducts = append(sproducts, wacMTProductItem{
							ProductRetailerID: p,
						})
						totalProductsPerMsg++
					}

					// After the inner loop, add the current section with the product
					if len(sproducts) > 0 {
						// Check if adding this section would exceed the sections limit
						if len(sections) >= maxSectionsPerMsg {
							actions = append(actions, sections)
							sections = []wacMTSection{}
							totalProductsPerMsg = len(sproducts)
						}
						sections = append(sections, wacMTSection{Title: title, ProductItems: sproducts})
					}
				}

				if len(sections) > 0 {
					actions = append(actions, sections)
				}

				for _, sections := range actions {
					interactive.Action = &struct {
						Button            string                 `json:"button,omitempty"`
						Sections          []wacMTSection         `json:"sections,omitempty"`
						Buttons           []wacMTButton          `json:"buttons,omitempty"`
						CatalogID         string                 `json:"catalog_id,omitempty"`
						ProductRetailerID string                 `json:"product_retailer_id,omitempty"`
						Name              string                 `json:"name,omitempty"`
						Parameters        map[string]interface{} "json:\"parameters,omitempty\""
					}{
						CatalogID: catalogID,
						Sections:  sections,
						Name:      msg.Action(),
					}

					payload.Interactive = &interactive
					status, _, err := requestWAC(payload, accessToken, msg, status, wacPhoneURL, true, useMarketingMessages)
					if err != nil {
						return status, err
					}
				}

			} else {
				interactive.Action = &struct {
					Button            string                 `json:"button,omitempty"`
					Sections          []wacMTSection         `json:"sections,omitempty"`
					Buttons           []wacMTButton          `json:"buttons,omitempty"`
					CatalogID         string                 `json:"catalog_id,omitempty"`
					ProductRetailerID string                 `json:"product_retailer_id,omitempty"`
					Name              string                 `json:"name,omitempty"`
					Parameters        map[string]interface{} "json:\"parameters,omitempty\""
				}{
					CatalogID:         catalogID,
					Name:              msg.Action(),
					ProductRetailerID: unitaryProduct,
				}
				payload.Interactive = &interactive
				status, _, err := requestWAC(payload, accessToken, msg, status, wacPhoneURL, true, useMarketingMessages)
				if err != nil {
					return status, err
				}
			}
		}
	}

	return status, nil
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

func castInteractive[I, O wacInteractiveActionParams](interactive wacInteractive[I]) *wacInteractive[O] {
	interactiveJSON, _ := json.Marshal(interactive)
	interactiveMap := wacInteractive[O]{}
	json.Unmarshal(interactiveJSON, &interactiveMap)
	return &interactiveMap
}

func mountOrderDetails(msg courier.Msg, paymentSettings []wacOrderDetailsPaymentSetting, catalogID *string, orderTax wacAmountWithOffset, orderShipping *wacAmountWithOffset, orderDiscount *wacAmountWithOffset) *wacOrderDetails {
	orderDetails := msg.OrderDetailsMessage()
	if orderDetails == nil {
		return nil
	}

	var paymentType string
	if orderDetails.PaymentSettings.OffsiteCardPay.CredentialID != "" {
		paymentType = "digital-goods"
	} else {
		paymentType = orderDetails.PaymentSettings.Type
	}

	// catalog_id is optional for offsite_card_pay messages
	var catalogIDValue string
	if catalogID != nil {
		catalogIDValue = *catalogID
	}

	return &wacOrderDetails{
		ReferenceID:     orderDetails.ReferenceID,
		Type:            paymentType,
		PaymentType:     "br",
		PaymentSettings: paymentSettings,
		Currency:        "BRL",
		TotalAmount: wacAmountWithOffset{
			Value:  orderDetails.TotalAmount,
			Offset: 100,
		},
		Order: wacOrder{
			Status:    "pending",
			CatalogID: catalogIDValue,
			Items:     orderDetails.Order.Items,
			Subtotal: wacAmountWithOffset{
				Value:  orderDetails.Order.Subtotal,
				Offset: 100,
			},
			Tax:      orderTax,
			Shipping: orderShipping,
			Discount: orderDiscount,
		},
	}
}

func mountOrderPaymentSettings(orderDetails *courier.OrderDetailsMessage) []wacOrderDetailsPaymentSetting {
	paymentSettings := make([]wacOrderDetailsPaymentSetting, 0)

	if orderDetails.PaymentSettings.PaymentLink != "" {
		paymentSettings = append(paymentSettings, wacOrderDetailsPaymentSetting{
			Type: "payment_link",
			PaymentLink: &wacOrderDetailsPaymentLink{
				URI: orderDetails.PaymentSettings.PaymentLink,
			},
		})
	}

	if orderDetails.PaymentSettings.PixConfig.Code != "" {
		paymentSettings = append(paymentSettings, wacOrderDetailsPaymentSetting{
			Type: "pix_dynamic_code",
			PixDynamicCode: &wacOrderDetailsPixDynamicCode{
				Code:         orderDetails.PaymentSettings.PixConfig.Code,
				MerchantName: orderDetails.PaymentSettings.PixConfig.MerchantName,
				Key:          orderDetails.PaymentSettings.PixConfig.Key,
				KeyType:      orderDetails.PaymentSettings.PixConfig.KeyType,
			},
		})
	}

	if orderDetails.PaymentSettings.OffsiteCardPay.CredentialID != "" {
		fmt.Printf("facebookapp.go - mountOrderPaymentSettings() - Offsite card pay: %v", orderDetails.PaymentSettings.OffsiteCardPay)
		paymentSettings = append(paymentSettings, wacOrderDetailsPaymentSetting{
			Type: "offsite_card_pay",
			OffsiteCardPay: &wacOrderDetailsOffsiteCardPay{
				LastFourDigits: orderDetails.PaymentSettings.OffsiteCardPay.LastFourDigits,
				CredentialID:   orderDetails.PaymentSettings.OffsiteCardPay.CredentialID,
			},
		})
	}

	return paymentSettings
}

func mountOrderInfo(msg courier.Msg) ([]wacOrderDetailsPaymentSetting, *string, wacAmountWithOffset, *wacAmountWithOffset, *wacAmountWithOffset) {
	fmt.Printf("facebookapp.go - mountOrderInfo() - msg.OrderDetailsMessage(): %v", msg.OrderDetailsMessage())
	paymentSettings := mountOrderPaymentSettings(msg.OrderDetailsMessage())

	strCatalogID := msg.Channel().StringConfigForKey("catalog_id", "")
	var catalogID *string
	if strCatalogID != "" {
		catalogID = &strCatalogID
	}

	orderTax, orderShipping, orderDiscount := mountOrderTaxShippingDiscount(msg.OrderDetailsMessage())

	return paymentSettings, catalogID, orderTax, orderShipping, orderDiscount
}

func mountOrderTaxShippingDiscount(orderDetails *courier.OrderDetailsMessage) (wacAmountWithOffset, *wacAmountWithOffset, *wacAmountWithOffset) {
	orderTax := wacAmountWithOffset{
		Value:       0,
		Offset:      100,
		Description: orderDetails.Order.Tax.Description,
	}
	if orderDetails.Order.Tax.Value > 0 {
		orderTax.Value = orderDetails.Order.Tax.Value
	}

	var orderShipping *wacAmountWithOffset
	var orderDiscount *wacAmountWithOffset
	if orderDetails.Order.Shipping.Value > 0 {
		orderShipping = &wacAmountWithOffset{
			Value:       orderDetails.Order.Shipping.Value,
			Offset:      100,
			Description: orderDetails.Order.Shipping.Description,
		}
	}

	if orderDetails.Order.Discount.Value > 0 {
		orderDiscount = &wacAmountWithOffset{
			Value:               orderDetails.Order.Discount.Value,
			Offset:              100,
			Description:         orderDetails.Order.Discount.Description,
			DiscountProgramName: orderDetails.Order.Discount.ProgramName,
		}
	}

	return orderTax, orderShipping, orderDiscount
}

func requestWAC[P wacInteractiveActionParams](payload wacMTPayload[P], accessToken string, msg courier.Msg, status courier.MsgStatus, wacPhoneURL *url.URL, zeroIndex bool, useMarketingMessages bool) (courier.MsgStatus, *wacMTResponse, error) {
	fmt.Printf("facebookapp.go - requestWAC() - payload: %v\n", payload)
	var jsonBody []byte
	var err error

	if useMarketingMessages {
		// Add message_activity_sharing to the original payload
		jsonBody, err = prepareMarketingMessagePayload(payload)
	} else {
		// Serialize the payload directly
		jsonBody, err = json.Marshal(payload)
	}

	if err != nil {
		return status, &wacMTResponse{}, err
	}

	// Prepare and send HTTP request
	req, err := prepareHTTPRequest(wacPhoneURL.String(), accessToken, jsonBody)
	if err != nil {
		return status, &wacMTResponse{}, err
	}

	rr, err := utils.MakeHTTPRequest(req)
	fmt.Printf("facebookapp.go - requestWAC() - rr: %v\n", rr)
	fmt.Printf("facebookapp.go - requestWAC() - err: %v\n", err)

	// Register status log based on message type
	logTitle := "Message Sent"
	if useMarketingMessages {
		logTitle = "Marketing Message Sent"
	}
	log := courier.NewChannelLogFromRR(logTitle, msg.Channel(), msg.ID(), rr).WithError("Message Send Error", err)
	status.AddLog(log)

	if err != nil {
		return status, &wacMTResponse{}, nil
	}

	// Process the response
	respPayload, err := processResponse(rr.Body)
	if err != nil {
		log.WithError("Message Send Error", errors.Errorf("unable to unmarshal response body"))
		return status, respPayload, nil
	}

	// Update message status if there is an external ID
	if len(respPayload.Messages) > 0 {
		externalID := respPayload.Messages[0].ID
		if zeroIndex && externalID != "" {
			status.SetExternalID(externalID)
		}
		status.SetStatus(courier.MsgWired)
	}

	return status, respPayload, nil
}

// Prepares the marketing message payload by adding message_activity_sharing
func prepareMarketingMessagePayload[P wacInteractiveActionParams](payload wacMTPayload[P]) ([]byte, error) {
	payloadMap := make(map[string]interface{})
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	err = json.Unmarshal(jsonBody, &payloadMap)
	if err != nil {
		return nil, err
	}

	payloadMap["message_activity_sharing"] = true

	return json.Marshal(payloadMap)
}

// Prepares the HTTP request
func prepareHTTPRequest(url string, accessToken string, jsonBody []byte) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// Process the response from the API
func processResponse(body []byte) (*wacMTResponse, error) {
	respPayload := &wacMTResponse{}
	err := json.Unmarshal(body, respPayload)
	return respPayload, err
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

// buildTemplateComponents builds all components for a WhatsApp template message
// This includes: body variables, carousel/header, order details, and buttons
func (h *handler) buildTemplateComponents(msg courier.Msg, templating *MsgTemplating, accessToken string, status courier.MsgStatus, start time.Time) ([]*wacComponent, error) {
	var components []*wacComponent

	// Build body component with variables
	if len(templating.Variables) > 0 {
		bodyComponent := &wacComponent{Type: "body"}
		for _, v := range templating.Variables {
			bodyComponent.Params = append(bodyComponent.Params, &wacParam{Type: "text", Text: v})
		}
		components = append(components, bodyComponent)
	}

	// Handle carousel templates - identified by IsCarousel flag
	// 2-10 cards required, media is mandatory per card, body and buttons are optional (max 2 buttons per card)
	if templating.IsCarousel {
		carouselComponent, err := h.buildCarouselComponent(msg, templating, accessToken, status, start)
		if err != nil {
			return nil, err
		}
		components = append(components, carouselComponent)
	} else if len(msg.Attachments()) > 0 {
		// Handle single header attachment (non-carousel)
		headerComponent, err := h.buildHeaderComponent(msg, accessToken, status, start)
		if err != nil {
			return nil, err
		}
		components = append(components, headerComponent)
	}

	// Handle order details button
	if msg.OrderDetailsMessage() != nil {
		orderDetailsComponent, err := buildOrderDetailsComponent(msg)
		if err != nil {
			return nil, err
		}
		components = append(components, orderDetailsComponent)
	}

	// Handle dynamic buttons
	if len(msg.Buttons()) > 0 {
		for i, button := range msg.Buttons() {
			buttonComponent := &wacComponent{Type: "button", SubType: button.SubType, Index: &i}
			for _, parameter := range button.Parameters {
				buttonComponent.Params = append(buttonComponent.Params, &wacParam{Type: parameter.Type, Text: parameter.Text})
			}
			components = append(components, buttonComponent)
		}
	}

	return components, nil
}

// buildHeaderComponent builds a single header component with media attachment
func (h *handler) buildHeaderComponent(msg courier.Msg, accessToken string, status courier.MsgStatus, start time.Time) (*wacComponent, error) {
	header := &wacComponent{Type: "header"}

	attType, attURL := handlers.SplitAttachment(msg.Attachments()[0])
	fileURL := attURL

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
		return nil, err
	}
	if attType == "application" {
		attType = "document"
	}

	media := wacMTMedia{ID: mediaID, Link: parsedURL.String()}
	switch attType {
	case "image":
		header.Params = append(header.Params, &wacParam{Type: "image", Image: &media})
	case "video":
		header.Params = append(header.Params, &wacParam{Type: "video", Video: &media})
	case "document":
		media.Filename, err = utils.BasePathForURL(fileURL)
		if err != nil {
			return nil, err
		}
		header.Params = append(header.Params, &wacParam{Type: "document", Document: &media})
	default:
		return nil, fmt.Errorf("unknown attachment mime type: %s", attType)
	}

	return header, nil
}

// buildOrderDetailsComponent builds the order details button component
func buildOrderDetailsComponent(msg courier.Msg) (*wacComponent, error) {
	index := 0
	button := &wacComponent{Type: "button", SubType: "order_details", Index: &index}

	paymentSettings, catalogID, orderTax, orderShipping, orderDiscount := mountOrderInfo(msg)

	mountedOrderDetails := mountOrderDetails(msg, paymentSettings, catalogID, orderTax, orderShipping, orderDiscount)
	if mountedOrderDetails == nil {
		return nil, fmt.Errorf("failed to mount order details")
	}

	param := wacParam{
		Type: "action",
		Action: &wacMTAction{
			OrderDetails: mountedOrderDetails,
		},
	}

	button.Params = append(button.Params, &param)
	return button, nil
}

// buildCarouselComponent builds the carousel component for WhatsApp template messages
// Card count is based on attachments count - media is mandatory per card (min 2, max 10 cards), body and buttons are optional (max 2 buttons per card)
// Media (header), body variables, and buttons are matched by index
func (h *handler) buildCarouselComponent(msg courier.Msg, templating *MsgTemplating, accessToken string, status courier.MsgStatus, start time.Time) (*wacComponent, error) {
	// Carousel requires 2-10 cards, each with mandatory media
	numCards := len(msg.Attachments())
	if numCards < 2 {
		return nil, fmt.Errorf("carousel templates require at least 2 media attachments, got %d", numCards)
	}
	if numCards > 10 {
		return nil, fmt.Errorf("carousel templates allow at most 10 media attachments, got %d", numCards)
	}

	carouselComponent := &wacComponent{Type: "carousel"}

	for cardIdx := 0; cardIdx < numCards; cardIdx++ {
		card := &wacCarouselCard{CardIndex: cardIdx}

		// Build header component with media (mandatory for each card)
		headerComponent := &wacComponent{Type: "header"}
		attType, attURL := handlers.SplitAttachment(msg.Attachments()[cardIdx])

		mediaID, mediaLogs, err := h.fetchWACMediaID(msg, attType, attURL, accessToken)
		for _, log := range mediaLogs {
			status.AddLog(log)
		}
		if err != nil {
			status.AddLog(courier.NewChannelLogFromError("error on fetch media ID for carousel card", msg.Channel(), msg.ID(), time.Since(start), err))
		} else if mediaID != "" {
			attURL = ""
		}
		attType = strings.Split(attType, "/")[0]

		parsedURL, err := url.Parse(attURL)
		if err != nil {
			return nil, err
		}

		media := wacMTMedia{ID: mediaID, Link: parsedURL.String()}
		switch attType {
		case "image":
			headerComponent.Params = append(headerComponent.Params, &wacParam{Type: "image", Image: &media})
		case "video":
			headerComponent.Params = append(headerComponent.Params, &wacParam{Type: "video", Video: &media})
		default:
			return nil, fmt.Errorf("unsupported attachment type for carousel card header: %s (only image and video are supported)", attType)
		}
		card.Components = append(card.Components, headerComponent)

		// Get card data if available (body variables and buttons are optional)
		if cardIdx < len(templating.CarouselCards) {
			cardData := templating.CarouselCards[cardIdx]

			// Build body component with text variables if present
			if len(cardData.Body) > 0 {
				bodyComponent := &wacComponent{Type: "body"}
				for _, bodyText := range cardData.Body {
					if bodyText != "" {
						bodyComponent.Params = append(bodyComponent.Params, &wacParam{Type: "text", Text: bodyText})
					}
				}
				if len(bodyComponent.Params) > 0 {
					card.Components = append(card.Components, bodyComponent)
				}
			}

			// Build button components if present (max 2 buttons per card)
			if len(cardData.Buttons) > 2 {
				return nil, fmt.Errorf("carousel card %d has %d buttons, maximum allowed is 2", cardIdx, len(cardData.Buttons))
			}
			for btnArrayIdx, btnData := range cardData.Buttons {
				// Use provided index or fall back to array position
				btnIdx := btnArrayIdx
				if btnData.Index != nil {
					btnIdx = *btnData.Index
				}
				buttonComponent := &wacComponent{Type: "button", SubType: btnData.SubType, Index: &btnIdx}
				switch btnData.SubType {
				case "quick_reply":
					buttonComponent.Params = append(buttonComponent.Params, &wacParam{Type: "payload", Payload: btnData.Parameter})
				case "url":
					buttonComponent.Params = append(buttonComponent.Params, &wacParam{Type: "text", Text: btnData.Parameter})
				}
				card.Components = append(card.Components, buttonComponent)
			}
		}

		carouselComponent.Cards = append(carouselComponent.Cards, card)
	}

	return carouselComponent, nil
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
		Name     string `json:"name" validate:"required"`
		UUID     string `json:"uuid" validate:"required"`
		Category string `json:"category"`
	} `json:"template" validate:"required,dive"`
	Language      string         `json:"language" validate:"required"`
	Country       string         `json:"country"`
	Namespace     string         `json:"namespace"`
	Variables     []string       `json:"variables"`
	IsCarousel    bool           `json:"is_carousel,omitempty"`
	CarouselCards []CarouselCard `json:"carousel_cards,omitempty"`
}

// CarouselCard represents a single card in a carousel template
// Body and buttons are optional (max 2 buttons) - cards are created based on attachments count (media is mandatory, 2-10 cards)
type CarouselCard struct {
	Body    []string             `json:"body,omitempty"`
	Buttons []CarouselCardButton `json:"buttons,omitempty"`
}

// CarouselCardButton represents a button in a carousel card
type CarouselCardButton struct {
	SubType   string `json:"sub_type"`            // quick_reply, url
	Index     *int   `json:"index,omitempty"`     // button index (optional, uses array position if not set)
	Parameter string `json:"parameter,omitempty"` // payload for quick_reply, url suffix for url
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

// extractRetailerIDs extracts retailer IDs from product map, checking both product_retailer_info (new format) and product_retailer_ids (old format)
func extractRetailerIDs(product map[string]interface{}) []string {
	// First check for new format: product_retailer_info
	if priData, ok := product["product_retailer_info"]; ok {
		if priList, ok := priData.([]interface{}); ok {
			var retailerIDs []string
			for _, pri := range priList {
				if priMap, ok := pri.(map[string]interface{}); ok {
					if retailerID, ok := priMap["retailer_id"].(string); ok {
						retailerIDs = append(retailerIDs, retailerID)
					}
				}
			}
			if len(retailerIDs) > 0 {
				return retailerIDs
			}
		}
	}

	// Fallback to old format: product_retailer_ids
	return toStringSlice(product["product_retailer_ids"])
}

var _ courier.ActionSender = (*handler)(nil)

// SendWhatsAppMessageAction sends a specific action to the WhatsApp API.
// This method is specific to the WhatsApp handler.
func (h *handler) SendAction(ctx context.Context, msg courier.Msg) (courier.MsgStatus, error) {
	channel := msg.Channel()
	targetMessageID := msg.ActionExternalID()

	// Ensure this action is only executed for WAC (WhatsApp Cloud) channel types
	if channel.ChannelType() != courier.ChannelType("WAC") {
		return nil, fmt.Errorf("WhatsApp actions are only supported for WAC channels, not for %s", channel.ChannelType())
	}

	accessToken := h.Server().Config().WhatsappAdminSystemUserToken
	userAccessToken := channel.StringConfigForKey(courier.ConfigUserToken, "")
	tokenToUse := accessToken
	if userAccessToken != "" {
		tokenToUse = userAccessToken
	}

	if tokenToUse == "" {
		return nil, errors.New("missing access token for WhatsApp action")
	}

	apiURLString := fmt.Sprintf("%s%s/messages", graphURL, channel.Address())

	if targetMessageID == "" {
		return nil, errors.New("targetMessageID (ExternalID) is required for combined action")
	}

	payloadMap := map[string]interface{}{
		"messaging_product": "whatsapp",
		"status":            "read",
		"message_id":        targetMessageID,
		"typing_indicator": map[string]interface{}{
			"type": "text",
		},
	}

	jsonBody, err := json.Marshal(payloadMap)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal WhatsApp action payload")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURLString, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create HTTP request")
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", tokenToUse))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	rr, err := utils.MakeHTTPRequest(req)
	if err != nil {
		// Include response body in error if available (WhatsApp API error details)
		if rr != nil && len(rr.Body) > 0 {
			return nil, fmt.Errorf("HTTP request failed (%d): %s - Response: %s", rr.StatusCode, err.Error(), string(rr.Body))
		}
		return nil, errors.Wrap(err, "HTTP request failed")
	}

	if rr.StatusCode < 200 || rr.StatusCode >= 300 {
		return nil, fmt.Errorf("WhatsApp API error (%d): %s", rr.StatusCode, string(rr.Body))
	}

	return nil, nil
}
