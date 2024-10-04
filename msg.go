package courier

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/nyaruka/null"

	"github.com/gofrs/uuid"
	"github.com/nyaruka/gocommon/urns"
)

// ErrMsgNotFound is returned when trying to queue the status for a Msg that doesn't exit
var ErrMsgNotFound = errors.New("message not found")

// ErrWrongIncomingMsgStatus use do ignore the status update if the DB raise this
var ErrWrongIncomingMsgStatus = errors.New("Incoming messages can only be PENDING or HANDLED")

// MsgID is our typing of the db int type
type MsgID null.Int

// NewMsgID creates a new MsgID for the passed in int64
func NewMsgID(id int64) MsgID {
	return MsgID(id)
}

// String satisfies the Stringer interface
func (i MsgID) String() string {
	if i != NilMsgID {
		return strconv.FormatInt(int64(i), 10)
	}
	return "null"
}

// MarshalJSON marshals into JSON. 0 values will become null
func (i MsgID) MarshalJSON() ([]byte, error) {
	return null.Int(i).MarshalJSON()
}

// UnmarshalJSON unmarshals from JSON. null values become 0
func (i *MsgID) UnmarshalJSON(b []byte) error {
	return null.UnmarshalInt(b, (*null.Int)(i))
}

// Value returns the db value, null is returned for 0
func (i MsgID) Value() (driver.Value, error) {
	return null.Int(i).Value()
}

// Scan scans from the db value. null values become 0
func (i *MsgID) Scan(value interface{}) error {
	return null.ScanInt(value, (*null.Int)(i))
}

// NilMsgID is our nil value for MsgID
var NilMsgID = MsgID(0)

// MsgUUID is the UUID of a message which has been received
type MsgUUID struct {
	uuid.UUID
}

// NilMsgUUID is a "zero value" message UUID
var NilMsgUUID = MsgUUID{uuid.Nil}

// NewMsgUUID creates a new unique message UUID
func NewMsgUUID() MsgUUID {
	u, _ := uuid.NewV4()
	return MsgUUID{u}
}

// NewMsgUUIDFromString creates a new message UUID for the passed in string
func NewMsgUUIDFromString(uuidString string) MsgUUID {
	uuid, _ := uuid.FromString(uuidString)
	return MsgUUID{uuid}
}

//-----------------------------------------------------------------------------
// Msg interface
//-----------------------------------------------------------------------------

// Msg is our interface to represent an incoming or outgoing message
type Msg interface {
	ID() MsgID
	UUID() MsgUUID
	Text() string
	Attachments() []string
	ExternalID() string
	URN() urns.URN
	URNAuth() string
	ContactName() string
	QuickReplies() []string
	Topic() string
	Metadata() json.RawMessage
	ResponseToID() MsgID
	ResponseToExternalID() string
	IsResend() bool

	Channel() Channel

	ReceivedOn() *time.Time
	SentOn() *time.Time

	HighPriority() bool

	WithContactName(name string) Msg
	WithReceivedOn(date time.Time) Msg
	WithExternalID(id string) Msg
	WithID(id MsgID) Msg
	WithUUID(uuid MsgUUID) Msg
	WithAttachment(url string) Msg
	WithURNAuth(auth string) Msg
	WithMetadata(metadata json.RawMessage) Msg

	EventID() int64
	SessionStatus() string

	TextLanguage() string

	Status() MsgStatusValue

	Products() []map[string]interface{}
	Header() string
	Body() string
	Footer() string
	Action() string
	SendCatalog() bool
	HeaderType() string
	HeaderText() string
	ListMessage() ListMessage
	InteractionType() string
	CTAMessage() *CTAMessage
	FlowMessage() *FlowMessage
	OrderDetailsMessage() *OrderDetailsMessage
}

type ListMessage struct {
	ButtonText string      `json:"button_text"`
	ListItems  []ListItems `json:"list_items"`
}

type CTAMessage struct {
	URL         string `json:"url"`
	DisplayText string `json:"display_text"`
}

type FlowMessage struct {
	FlowID     string                 `json:"flow_id"`
	FlowScreen string                 `json:"flow_screen"`
	FlowData   map[string]interface{} `json:"flow_data"`
	FlowCTA    string                 `json:"flow_cta"`
	FlowMode   string                 `json:"flow_mode"`
}

type ListItems struct {
	UUID        string `json:"uuid"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type RunEvent struct {
	Type      string     `json:"type,omitempty"`
	StepUUID  string     `json:"step_uuid,omitempty"`
	CreatedOn *time.Time `json:"created_on,omitempty"`
	Msg       EventMsg   `json:"msg,omitempty"`
}

type EventMsg struct {
	ID   int64  `json:"id,omitempty"`
	URN  string `json:"urn,omitempty"`
	Text string `json:"text,omitempty"`
	UUID string `json:"uuid,omitempty"`
}

type OrderItem struct {
	RetailerID string `json:"retailer_id"`
	Name       string `json:"name"`
	Amount     int    `json:"amount"`
	Quantity   int    `json:"quantity"`
	SaleAmount int    `json:"sale_amount,omitempty"`
}

type OrderAmountWithDescription struct {
	Value       int    `json:"value"`
	Description string `json:"description,omitempty"`
}

type OrderDiscount struct {
	Value       int    `json:"value"`
	Description string `json:"description,omitempty"`
	ProgramName string `json:"program_name,omitempty"`
}

type Order struct {
	Items    []OrderItem                `json:"items"`
	Subtotal int                        `json:"subtotal"`
	Tax      OrderAmountWithDescription `json:"tax"`
	Shipping OrderAmountWithDescription `json:"shipping,omitempty"`
	Discount OrderDiscount              `json:"discount,omitempty"`
}

type OrderPixConfig struct {
	Key          string `json:"key"`
	KeyType      string `json:"key_type"`
	MerchantName string `json:"merchant_name"`
	Code         string `json:"code"`
}

type OrderPaymentSettings struct {
	Type        string         `json:"type"`
	PaymentLink string         `json:"payment_link,omitempty"`
	PixConfig   OrderPixConfig `json:"pix_config,omitempty"`
}

type OrderDetailsMessage struct {
	ReferenceID     string               `json:"reference_id"`
	PaymentSettings OrderPaymentSettings `json:"payment_settings"`
	TotalAmount     int                  `json:"total_amount"`
	Order           Order                `json:"order"`
}
