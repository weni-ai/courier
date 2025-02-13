package meta

import "github.com/nyaruka/courier"

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

type moPayload struct {
	Object string `json:"object"`
	Entry  []struct {
		ID        string      `json:"id"`
		Time      int64       `json:"time"`
		Changes   []Change    `json:"changes"`
		Messaging []Messaging `json:"messaging"`
	} `json:"entry"`
}

type Change struct {
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
}

type Messaging struct {
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
}

type Sender struct {
	ID      string `json:"id"`
	UserRef string `json:"user_ref,omitempty"`
}

type User struct {
	ID string `json:"id"`
}

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

type Flow struct {
	NFMReply NFMReply `json:"nfm_reply"`
}

type NFMReply struct {
	Name         string                 `json:"name,omitempty"`
	ResponseJSON map[string]interface{} `json:"response_json"`
}

type FeedbackQuestion struct {
	Type     string `json:"type"`
	Payload  string `json:"payload"`
	FollowUp *struct {
		Type    string `json:"type"`
		Payload string `json:"payload"`
	} `json:"follow_up"`
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
	Type     string       `json:"type"`
	Text     string       `json:"text,omitempty"`
	Image    *wacMTMedia  `json:"image,omitempty"`
	Document *wacMTMedia  `json:"document,omitempty"`
	Video    *wacMTMedia  `json:"video,omitempty"`
	Action   *wacMTAction `json:"action,omitempty"`
}

type wacComponent struct {
	Type    string      `json:"type"`
	SubType string      `json:"sub_type,omitempty"`
	Index   *int        `json:"index,omitempty"`
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

type wacMTSectionProduct struct {
	Title string `json:"title,omitempty"`
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

type wacOrderDetailsPaymentSetting struct {
	Type           string                         `json:"type" validate:"required"`
	PaymentLink    *wacOrderDetailsPaymentLink    `json:"payment_link,omitempty"`
	PixDynamicCode *wacOrderDetailsPixDynamicCode `json:"pix_dynamic_code,omitempty"`
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
