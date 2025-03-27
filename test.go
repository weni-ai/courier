package courier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/buger/jsonparser"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/gocommon/uuids"

	"github.com/gomodule/redigo/redis"
	_ "github.com/lib/pq" // postgres driver
)

//-----------------------------------------------------------------------------
// Mock backend implementation
//-----------------------------------------------------------------------------

// MockBackend is a mocked version of a backend which doesn't require a real database or cache
type MockBackend struct {
	channels          map[ChannelUUID]Channel
	channelsByAddress map[ChannelAddress]Channel
	contacts          map[urns.URN]Contact
	queueMsgs         []Msg
	errorOnQueue      bool

	mutex           sync.RWMutex
	outgoingMsgs    []Msg
	msgStatuses     []MsgStatus
	channelEvents   []ChannelEvent
	channelLogs     []*ChannelLog
	lastContactName string

	sentMsgs  map[MsgID]bool
	redisPool *redis.Pool

	seenExternalIDs []string
}

// NewMockBackend returns a new mock backend suitable for testing
func NewMockBackend() *MockBackend {
	redisPool := &redis.Pool{
		Wait:        true,              // makes callers wait for a connection
		MaxActive:   5,                 // only open this many concurrent connections at once
		MaxIdle:     2,                 // only keep up to 2 idle
		IdleTimeout: 240 * time.Second, // how long to wait before reaping a connection
		Dial: func() (redis.Conn, error) {
			conn, err := redis.Dial("tcp", "localhost:6379")
			if err != nil {
				return nil, err
			}
			_, err = conn.Do("SELECT", 0)
			return conn, err
		},
	}
	conn := redisPool.Get()
	defer conn.Close()

	_, err := conn.Do("FLUSHDB")
	if err != nil {
		log.Fatal(err)
	}

	return &MockBackend{
		channels:          make(map[ChannelUUID]Channel),
		channelsByAddress: make(map[ChannelAddress]Channel),
		contacts:          make(map[urns.URN]Contact),
		sentMsgs:          make(map[MsgID]bool),
		redisPool:         redisPool,
	}
}

// GetLastQueueMsg returns the last message queued to the server
func (mb *MockBackend) GetLastQueueMsg() (Msg, error) {
	if len(mb.queueMsgs) == 0 {
		return nil, ErrMsgNotFound
	}
	return mb.queueMsgs[len(mb.queueMsgs)-1], nil
}

// GetLastChannelEvent returns the last event written to the server
func (mb *MockBackend) GetLastChannelEvent() (ChannelEvent, error) {
	if len(mb.channelEvents) == 0 {
		return nil, errors.New("no channel events")
	}
	return mb.channelEvents[len(mb.channelEvents)-1], nil
}

// GetLastChannelLog returns the last channel log written to the server
func (mb *MockBackend) GetLastChannelLog() (*ChannelLog, error) {
	if len(mb.channelLogs) == 0 {
		return nil, errors.New("no channel logs")
	}
	return mb.channelLogs[len(mb.channelLogs)-1], nil
}

// GetLastMsgStatus returns the last status written to the server
func (mb *MockBackend) GetLastMsgStatus() (MsgStatus, error) {
	if len(mb.msgStatuses) == 0 {
		return nil, errors.New("no msg statuses")
	}
	return mb.msgStatuses[len(mb.msgStatuses)-1], nil
}

// GetLastContactName returns the contact name set on the last msg or channel event written
func (mb *MockBackend) GetLastContactName() string {
	return mb.lastContactName
}

// DeleteMsgWithExternalID delete a message we receive an event that it should be deleted
func (mb *MockBackend) DeleteMsgWithExternalID(ctx context.Context, channel Channel, externalID string) error {
	return nil
}

// NewIncomingMsg creates a new message from the given params
func (mb *MockBackend) NewIncomingMsg(channel Channel, urn urns.URN, text string) Msg {
	return &mockMsg{channel: channel, urn: urn, text: text}
}

// NewOutgoingMsg creates a new outgoing message from the given params
func (mb *MockBackend) NewOutgoingMsg(channel Channel, id MsgID, urn urns.URN, text string, highPriority bool, quickReplies []string, topic string, responseToID int64, responseToExternalID string, textLanguage string) Msg {
	msgResponseToID := NilMsgID
	if responseToID != 0 {
		msgResponseToID = NewMsgID(responseToID)
	}

	return &mockMsg{channel: channel, id: id, urn: urn, text: text, highPriority: highPriority, quickReplies: quickReplies, topic: topic, responseToID: msgResponseToID, responseToExternalID: responseToExternalID, textLanguage: textLanguage}
}

// PushOutgoingMsg is a test method to add a message to our queue of messages to send
func (mb *MockBackend) PushOutgoingMsg(msg Msg) {
	mb.mutex.Lock()
	defer mb.mutex.Unlock()

	mb.outgoingMsgs = append(mb.outgoingMsgs, msg)
}

// PopNextOutgoingMsg returns the next message that should be sent, or nil if there are none to send
func (mb *MockBackend) PopNextOutgoingMsg(ctx context.Context) (Msg, error) {
	mb.mutex.Lock()
	defer mb.mutex.Unlock()

	if len(mb.outgoingMsgs) > 0 {
		msg, rest := mb.outgoingMsgs[0], mb.outgoingMsgs[1:]
		mb.outgoingMsgs = rest
		return msg, nil
	}

	return nil, nil
}

// WasMsgSent returns whether the passed in msg was already sent
func (mb *MockBackend) WasMsgSent(ctx context.Context, id MsgID) (bool, error) {
	mb.mutex.Lock()
	defer mb.mutex.Unlock()

	return mb.sentMsgs[id], nil
}

func (mb *MockBackend) ClearMsgSent(ctx context.Context, id MsgID) error {
	mb.mutex.Lock()
	defer mb.mutex.Unlock()

	delete(mb.sentMsgs, id)
	return nil
}

// IsMsgLoop returns whether the passed in msg is a loop
func (mb *MockBackend) IsMsgLoop(ctx context.Context, msg Msg) (bool, error) {
	return false, nil
}

// MarkOutgoingMsgComplete marks the passed msg as having been dealt with
func (mb *MockBackend) MarkOutgoingMsgComplete(ctx context.Context, msg Msg, s MsgStatus) {
	mb.mutex.Lock()
	defer mb.mutex.Unlock()

	mb.sentMsgs[msg.ID()] = true
}

// WriteChannelLogs writes the passed in channel logs to the DB
func (mb *MockBackend) WriteChannelLogs(ctx context.Context, logs []*ChannelLog) error {
	mb.mutex.Lock()
	defer mb.mutex.Unlock()

	for _, log := range logs {
		mb.channelLogs = append(mb.channelLogs, log)
	}
	return nil
}

// SetErrorOnQueue is a mock method which makes the QueueMsg call throw the passed in error on next call
func (mb *MockBackend) SetErrorOnQueue(shouldError bool) {
	mb.errorOnQueue = shouldError
}

// WriteMsg queues the passed in message internally
func (mb *MockBackend) WriteMsg(ctx context.Context, m Msg) error {
	mock := m.(*mockMsg)

	// this msg has already been written (we received it twice), we are a no op
	if mock.alreadyWritten {
		return nil
	}

	if mb.errorOnQueue {
		return errors.New("unable to queue message")
	}

	mb.queueMsgs = append(mb.queueMsgs, m)
	mb.lastContactName = m.(*mockMsg).contactName
	return nil
}

// NewMsgStatusForID creates a new Status object for the given message id
func (mb *MockBackend) NewMsgStatusForID(channel Channel, id MsgID, status MsgStatusValue) MsgStatus {
	return &mockMsgStatus{
		channel:   channel,
		id:        id,
		status:    status,
		createdOn: time.Now().In(time.UTC),
	}
}

// NewMsgStatusForExternalID creates a new Status object for the given external id
func (mb *MockBackend) NewMsgStatusForExternalID(channel Channel, externalID string, status MsgStatusValue) MsgStatus {
	return &mockMsgStatus{
		channel:    channel,
		externalID: externalID,
		status:     status,
		createdOn:  time.Now().In(time.UTC),
	}
}

// WriteMsgStatus writes the status update to our queue
func (mb *MockBackend) WriteMsgStatus(ctx context.Context, status MsgStatus) error {
	mb.mutex.Lock()
	defer mb.mutex.Unlock()

	mb.msgStatuses = append(mb.msgStatuses, status)
	return nil
}

// NewChannelEvent creates a new channel event with the passed in parameters
func (mb *MockBackend) NewChannelEvent(channel Channel, eventType ChannelEventType, urn urns.URN) ChannelEvent {
	return &mockChannelEvent{
		channel:   channel,
		eventType: eventType,
		urn:       urn,
	}
}

// WriteChannelEvent writes the channel event passed in
func (mb *MockBackend) WriteChannelEvent(ctx context.Context, event ChannelEvent) error {
	mb.mutex.Lock()
	defer mb.mutex.Unlock()

	mb.channelEvents = append(mb.channelEvents, event)
	mb.lastContactName = event.(*mockChannelEvent).contactName
	return nil
}

// GetChannel returns the channel with the passed in type and channel uuid
func (mb *MockBackend) GetChannel(ctx context.Context, cType ChannelType, uuid ChannelUUID) (Channel, error) {
	channel, found := mb.channels[uuid]
	if !found {
		return nil, ErrChannelNotFound
	}
	return channel, nil
}

// GetChannelByAddress returns the channel with the passed in type and channel address
func (mb *MockBackend) GetChannelByAddress(ctx context.Context, cType ChannelType, address ChannelAddress) (Channel, error) {
	channel, found := mb.channelsByAddress[address]
	if !found {
		return nil, ErrChannelNotFound
	}
	return channel, nil
}

// GetContact creates a new contact with the passed in channel and URN
func (mb *MockBackend) GetContact(ctx context.Context, channel Channel, urn urns.URN, auth string, name string) (Contact, error) {
	contact, found := mb.contacts[urn]
	if !found {
		uuid, _ := NewContactUUID(string(uuids.New()))
		contact = &mockContact{channel, urn, auth, uuid}
		mb.contacts[urn] = contact
	}
	return contact, nil
}

// AddURNtoContact adds a URN to the passed in contact
func (mb *MockBackend) AddURNtoContact(context context.Context, channel Channel, contact Contact, urn urns.URN) (urns.URN, error) {
	mb.contacts[urn] = contact
	return urn, nil
}

// RemoveURNFromcontact removes a URN from the passed in contact
func (mb *MockBackend) RemoveURNfromContact(context context.Context, channel Channel, contact Contact, urn urns.URN) (urns.URN, error) {
	contact, found := mb.contacts[urn]
	if found {
		delete(mb.contacts, urn)
	}
	return urn, nil
}

// AddChannel adds a test channel to the test server
func (mb *MockBackend) AddChannel(channel Channel) {
	mb.channels[channel.UUID()] = channel
	mb.channelsByAddress[channel.ChannelAddress()] = channel
}

// ClearChannels is a utility function on our mock server to clear all added channels
func (mb *MockBackend) ClearChannels() {
	mb.channels = nil
	mb.channelsByAddress = nil
}

// Start starts our mock backend
func (mb *MockBackend) Start() error { return nil }

// Stop stops our mock backend
func (mb *MockBackend) Stop() error { return nil }

// Cleanup cleans up any connections that are open
func (mb *MockBackend) Cleanup() error { return nil }

// ClearQueueMsgs clears our mock msg queue
func (mb *MockBackend) ClearQueueMsgs() {
	mb.queueMsgs = nil
}

// ClearSeenExternalIDs clears our mock seen external ids
func (mb *MockBackend) ClearSeenExternalIDs() {
	mb.seenExternalIDs = nil
}

// LenQueuedMsgs Get the length of queued msgs
func (mb *MockBackend) LenQueuedMsgs() int {
	return len(mb.queueMsgs)
}

// CheckExternalIDSeen checks if external ID has been seen in a period
func (mb *MockBackend) CheckExternalIDSeen(msg Msg) Msg {
	m := msg.(*mockMsg)

	for _, b := range mb.seenExternalIDs {
		if b == msg.ExternalID() {
			m.alreadyWritten = true
			return m
		}
	}
	return m
}

// WriteExternalIDSeen marks a external ID as seen for a period
func (mb *MockBackend) WriteExternalIDSeen(msg Msg) {
	mb.seenExternalIDs = append(mb.seenExternalIDs, msg.ExternalID())
}

// Health gives a string representing our health, empty for our mock
func (mb *MockBackend) Health() string {
	return ""
}

// Status returns a string describing the status of the service, queue size etc..
func (mb *MockBackend) Status() string {
	return ""
}

// Heartbeat is a noop for our mock backend
func (mb *MockBackend) Heartbeat() error {
	return nil
}

// RedisPool returns the redisPool for this backend
func (mb *MockBackend) RedisPool() *redis.Pool {
	return mb.redisPool
}

func (b *MockBackend) GetRunEventsByMsgUUIDFromDB(ctx context.Context, msgUUID string) ([]RunEvent, error) {
	return nil, nil
}

func (b *MockBackend) GetMessage(ctx context.Context, msgUUID string) (Msg, error) {
	return nil, nil
}

func buildMockBackend(config *Config) Backend {
	return NewMockBackend()
}

func init() {
	RegisterBackend("mock", buildMockBackend)
}

//-----------------------------------------------------------------------------
// Mock channel implementation
//-----------------------------------------------------------------------------

// MockChannel implements the Channel interface and is used in our tests
type MockChannel struct {
	uuid        ChannelUUID
	channelType ChannelType
	schemes     []string
	address     ChannelAddress
	country     string
	role        string
	config      map[string]interface{}
	orgConfig   map[string]interface{}
}

// UUID returns the uuid for this channel
func (c *MockChannel) UUID() ChannelUUID { return c.uuid }

// Name returns the name of this channel, we just return our UUID for our mock instances
func (c *MockChannel) Name() string { return fmt.Sprintf("Channel: %s", c.uuid.String()) }

// ChannelType returns the type of this channel
func (c *MockChannel) ChannelType() ChannelType { return c.channelType }

// SetScheme sets the scheme for this channel
func (c *MockChannel) SetScheme(scheme string) { c.schemes = []string{scheme} }

// Schemes returns the schemes for this channel
func (c *MockChannel) Schemes() []string { return c.schemes }

// IsScheme returns whether the passed in scheme is the scheme for this channel
func (c *MockChannel) IsScheme(scheme string) bool {
	return len(c.schemes) == 1 && c.schemes[0] == scheme
}

// Address returns the address as a string of this channel
func (c *MockChannel) Address() string { return c.address.String() }

// ChannelAddress returns the address of this channel
func (c *MockChannel) ChannelAddress() ChannelAddress { return c.address }

// Country returns the country this channel is for (if any)
func (c *MockChannel) Country() string { return c.country }

// SetConfig sets the passed in config parameter
func (c *MockChannel) SetConfig(key string, value interface{}) {
	c.config[key] = value
}

// CallbackDomain returns the callback domain to use for this channel
func (c *MockChannel) CallbackDomain(fallbackDomain string) string {
	value, found := c.config[ConfigCallbackDomain]
	if !found {
		return fallbackDomain
	}
	return value.(string)
}

// ConfigForKey returns the config value for the passed in key
func (c *MockChannel) ConfigForKey(key string, defaultValue interface{}) interface{} {
	value, found := c.config[key]
	if !found {
		return defaultValue
	}
	return value
}

// StringConfigForKey returns the config value for the passed in key
func (c *MockChannel) StringConfigForKey(key string, defaultValue string) string {
	val := c.ConfigForKey(key, defaultValue)
	str, isStr := val.(string)
	if !isStr {
		return defaultValue
	}
	return str
}

// BoolConfigForKey returns the config value for the passed in key
func (c *MockChannel) BoolConfigForKey(key string, defaultValue bool) bool {
	val := c.ConfigForKey(key, defaultValue)
	b, isBool := val.(bool)
	if !isBool {
		return defaultValue
	}
	return b
}

// IntConfigForKey returns the config value for the passed in key
func (c *MockChannel) IntConfigForKey(key string, defaultValue int) int {
	val := c.ConfigForKey(key, defaultValue)

	// golang unmarshals number literals in JSON into float64s by default
	f, isFloat := val.(float64)
	if isFloat {
		return int(f)
	}

	// test authors may use literal ints
	i, isInt := val.(int)
	if isInt {
		return i
	}

	str, isStr := val.(string)
	if isStr {
		i, err := strconv.Atoi(str)
		if err == nil {
			return i
		}
	}
	return defaultValue
}

// OrgConfigForKey returns the org config value for the passed in key
func (c *MockChannel) OrgConfigForKey(key string, defaultValue interface{}) interface{} {
	value, found := c.orgConfig[key]
	if !found {
		return defaultValue
	}
	return value
}

// SetRoles sets the role on the channel
func (c *MockChannel) SetRoles(roles []ChannelRole) {
	c.role = fmt.Sprint(roles)
}

// Roles returns the roles of this channel
func (c *MockChannel) Roles() []ChannelRole {
	roles := []ChannelRole{}
	for _, char := range strings.Split(c.role, "") {
		roles = append(roles, ChannelRole(char))
	}
	return roles
}

// HasRole returns whether the passed in channel supports the passed role
func (c *MockChannel) HasRole(role ChannelRole) bool {
	for _, r := range c.Roles() {
		if r == role {
			return true
		}
	}
	return false
}

// NewMockChannel creates a new mock channel for the passed in type, address, country and config
func NewMockChannel(uuid string, channelType string, address string, country string, config map[string]interface{}) *MockChannel {
	cUUID, _ := NewChannelUUID(uuid)

	channel := &MockChannel{
		uuid:        cUUID,
		channelType: ChannelType(channelType),
		schemes:     []string{urns.TelScheme},
		address:     ChannelAddress(address),
		country:     country,
		config:      config,
		role:        "SR",
		orgConfig:   map[string]interface{}{},
	}
	return channel
}

//-----------------------------------------------------------------------------
// Mock msg implementation
//-----------------------------------------------------------------------------

type mockMsg struct {
	channel              Channel
	id                   MsgID
	uuid                 MsgUUID
	text                 string
	attachments          []string
	externalID           string
	urn                  urns.URN
	urnAuth              string
	contactName          string
	highPriority         bool
	quickReplies         []string
	topic                string
	responseToID         MsgID
	responseToExternalID string
	metadata             json.RawMessage
	alreadyWritten       bool
	isResend             bool
	textLanguage         string

	receivedOn *time.Time
	sentOn     *time.Time
	wiredOn    *time.Time

	products    []map[string]interface{}
	listMessage ListMessage
}

func (m *mockMsg) SessionStatus() string { return "" }

func (m *mockMsg) Channel() Channel             { return m.channel }
func (m *mockMsg) ID() MsgID                    { return m.id }
func (m *mockMsg) EventID() int64               { return int64(m.id) }
func (m *mockMsg) UUID() MsgUUID                { return m.uuid }
func (m *mockMsg) Text() string                 { return m.text }
func (m *mockMsg) Attachments() []string        { return m.attachments }
func (m *mockMsg) ExternalID() string           { return m.externalID }
func (m *mockMsg) URN() urns.URN                { return m.urn }
func (m *mockMsg) URNAuth() string              { return m.urnAuth }
func (m *mockMsg) ContactName() string          { return m.contactName }
func (m *mockMsg) HighPriority() bool           { return m.highPriority }
func (m *mockMsg) QuickReplies() []string       { return m.quickReplies }
func (m *mockMsg) Topic() string                { return m.topic }
func (m *mockMsg) ResponseToID() MsgID          { return m.responseToID }
func (m *mockMsg) ResponseToExternalID() string { return m.responseToExternalID }
func (m *mockMsg) Metadata() json.RawMessage    { return m.metadata }
func (m *mockMsg) IsResend() bool               { return m.isResend }
func (m *mockMsg) TextLanguage() string         { return m.textLanguage }

func (m *mockMsg) ReceivedOn() *time.Time { return m.receivedOn }
func (m *mockMsg) SentOn() *time.Time     { return m.sentOn }
func (m *mockMsg) WiredOn() *time.Time    { return m.wiredOn }

func (m *mockMsg) WithContactName(name string) Msg   { m.contactName = name; return m }
func (m *mockMsg) WithURNAuth(auth string) Msg       { m.urnAuth = auth; return m }
func (m *mockMsg) WithReceivedOn(date time.Time) Msg { m.receivedOn = &date; return m }
func (m *mockMsg) WithExternalID(id string) Msg      { m.externalID = id; return m }
func (m *mockMsg) WithID(id MsgID) Msg               { m.id = id; return m }
func (m *mockMsg) WithUUID(uuid MsgUUID) Msg         { m.uuid = uuid; return m }
func (m *mockMsg) WithAttachment(url string) Msg {
	m.attachments = append(m.attachments, url)
	return m
}
func (m *mockMsg) WithMetadata(metadata json.RawMessage) Msg { m.metadata = metadata; return m }
func (m *mockMsg) Status() MsgStatusValue                    { return "" }

func (m *mockMsg) Header() string {
	if m.metadata == nil {
		return ""
	}
	header, _, _, _ := jsonparser.Get(m.metadata, "header")
	return string(header)
}

func (m *mockMsg) IGCommentID() string {
	if m.metadata == nil {
		return ""
	}
	igCommentID, _, _, _ := jsonparser.Get(m.metadata, "ig_comment_id")
	return string(igCommentID)
}

func (m *mockMsg) IGResponseType() string {
	if m.metadata == nil {
		return ""
	}
	igResponseType, _, _, _ := jsonparser.Get(m.metadata, "ig_response_type")
	return string(igResponseType)
}

func (m *mockMsg) Body() string {
	if m.metadata == nil {
		return ""
	}
	body, _, _, _ := jsonparser.Get(m.metadata, "body")
	return string(body)
}

func (m *mockMsg) Footer() string {
	if m.metadata == nil {
		return ""
	}
	footer, _, _, _ := jsonparser.Get(m.metadata, "footer")
	return string(footer)
}

func (m *mockMsg) Products() []map[string]interface{} {
	if m.products != nil {
		return m.products
	}

	if m.Metadata() == nil {
		return nil
	}

	p, _, _, _ := jsonparser.Get(m.Metadata(), "products")
	err := json.Unmarshal(p, &m.products)
	if err != nil {
		return nil
	}

	return m.products
}

func (m *mockMsg) Action() string {
	if m.metadata == nil {
		return ""
	}
	action, _, _, _ := jsonparser.Get(m.metadata, "action")
	return string(action)
}

func (m *mockMsg) SendCatalog() bool {
	if m.metadata == nil {
		return false
	}
	byteValue, _, _, _ := jsonparser.Get(m.metadata, "send_catalog")
	sendCatalog, err := strconv.ParseBool(string(byteValue))
	if err != nil {
		return false
	}
	return sendCatalog
}

func (m *mockMsg) ListMessage() ListMessage {
	if m.metadata == nil {
		return ListMessage{}
	}

	var metadata map[string]interface{}
	err := json.Unmarshal(m.metadata, &metadata)
	if err != nil {
		return m.listMessage
	}

	byteValue, _, _, _ := jsonparser.Get(m.metadata, "interaction_type")
	interactionType := string(byteValue)

	if interactionType == "list" {
		m.listMessage = ListMessage{}
		m.listMessage.ButtonText = metadata["list_message"].(map[string]interface{})["button_text"].(string)

		listItems := metadata["list_message"].(map[string]interface{})["list_items"].([]interface{})
		m.listMessage.ListItems = make([]ListItems, len(listItems))
		for i, item := range listItems {
			itemMap := item.(map[string]interface{})
			m.listMessage.ListItems[i] = ListItems{
				Title: itemMap["title"].(string),
				UUID:  itemMap["uuid"].(string),
			}

			if itemMap["description"] != nil {
				m.listMessage.ListItems[i].Description = itemMap["description"].(string)
			}
		}
	}
	return m.listMessage
}

func (m *mockMsg) HeaderType() string {
	if m.metadata == nil {
		return ""
	}
	byteValue, _, _, _ := jsonparser.Get(m.metadata, "header_type")
	return string(byteValue)
}

func (m *mockMsg) HeaderText() string {
	if m.metadata == nil {
		return ""
	}
	byteValue, _, _, _ := jsonparser.Get(m.metadata, "header_text")
	return string(byteValue)
}

func (m *mockMsg) InteractionType() string {
	if m.metadata == nil {
		return ""
	}
	byteValue, _, _, _ := jsonparser.Get(m.metadata, "interaction_type")
	return string(byteValue)
}

func (m *mockMsg) CTAMessage() *CTAMessage {
	if m.metadata == nil {
		return nil
	}

	var metadata map[string]interface{}
	err := json.Unmarshal(m.metadata, &metadata)
	if err != nil {
		return nil
	}

	if metadata == nil {
		return nil
	}

	if interactionType, ok := metadata["interaction_type"].(string); ok && interactionType == "cta_url" {
		if ctaMessageData, ok := metadata["cta_message"].(map[string]interface{}); ok {
			ctaMessage := &CTAMessage{}
			if displayText, ok := ctaMessageData["display_text"].(string); ok {
				ctaMessage.DisplayText = displayText
			}
			if actionURL, ok := ctaMessageData["url"].(string); ok {
				ctaMessage.URL = actionURL
			}
			return ctaMessage
		}
	}
	return nil
}

func (m *mockMsg) FlowMessage() *FlowMessage {
	if m.metadata == nil {
		return nil
	}

	var metadata map[string]interface{}
	err := json.Unmarshal(m.metadata, &metadata)
	if err != nil {
		return nil
	}

	if metadata == nil {
		return nil
	}

	if interactionType, ok := metadata["interaction_type"].(string); ok && interactionType == "flow_msg" {
		if flowMessageData, ok := metadata["flow_message"].(map[string]interface{}); ok {
			flowMessage := &FlowMessage{}
			if flowID, ok := flowMessageData["flow_id"].(string); ok {
				flowMessage.FlowID = flowID
			}
			if flowScreen, ok := flowMessageData["flow_screen"].(string); ok {
				flowMessage.FlowScreen = flowScreen
			}
			if flowData, ok := flowMessageData["flow_data"].(map[string]interface{}); ok {
				convertedFlowData := map[string]interface{}{}
				for key, value := range flowData {
					convertedFlowData[key] = value
				}
				flowMessage.FlowData = convertedFlowData
			}
			if flowCTA, ok := flowMessageData["flow_cta"].(string); ok {
				flowMessage.FlowCTA = flowCTA
			}
			if flowMode, ok := flowMessageData["flow_mode"].(string); ok {
				flowMessage.FlowMode = flowMode
			}
			return flowMessage
		}
	}
	return nil
}

func (m *mockMsg) OrderDetailsMessage() *OrderDetailsMessage {
	if m.metadata == nil {
		return nil
	}

	var metadata map[string]interface{}
	err := json.Unmarshal(m.metadata, &metadata)
	if err != nil {
		return nil
	}

	if metadata == nil {
		return nil
	}

	if orderDetailsMessageData, ok := metadata["order_details_message"].(map[string]interface{}); ok {
		orderDetailsMessage := &OrderDetailsMessage{}
		if referenceID, ok := orderDetailsMessageData["reference_id"].(string); ok {
			orderDetailsMessage.ReferenceID = referenceID
		}
		if paymentSettings, ok := orderDetailsMessageData["payment_settings"].(map[string]interface{}); ok {
			orderDetailsMessage.PaymentSettings = OrderPaymentSettings{}
			if payment_type, ok := paymentSettings["type"].(string); ok {
				orderDetailsMessage.PaymentSettings.Type = payment_type
			}
			if payment_link, ok := paymentSettings["payment_link"].(string); ok {
				orderDetailsMessage.PaymentSettings.PaymentLink = payment_link
			}
			if pix_config, ok := paymentSettings["pix_config"].(map[string]interface{}); ok {
				orderDetailsMessage.PaymentSettings.PixConfig = OrderPixConfig{}
				if pix_config_key, ok := pix_config["key"].(string); ok {
					orderDetailsMessage.PaymentSettings.PixConfig.Key = pix_config_key
				}
				if pix_config_key_type, ok := pix_config["key_type"].(string); ok {
					orderDetailsMessage.PaymentSettings.PixConfig.KeyType = pix_config_key_type
				}
				if pix_config_merchant_name, ok := pix_config["merchant_name"].(string); ok {
					orderDetailsMessage.PaymentSettings.PixConfig.MerchantName = pix_config_merchant_name
				}
				if pix_config_code, ok := pix_config["code"].(string); ok {
					orderDetailsMessage.PaymentSettings.PixConfig.Code = pix_config_code
				}
			}
		}
		if totalAmount, ok := orderDetailsMessageData["total_amount"].(float64); ok {
			orderDetailsMessage.TotalAmount = int(totalAmount)
		}
		if orderData, ok := orderDetailsMessageData["order"].(map[string]interface{}); ok {
			orderDetailsMessage.Order = Order{}
			if itemsData, ok := orderData["items"].([]interface{}); ok {
				orderDetailsMessage.Order.Items = make([]OrderItem, len(itemsData))
				for i, item := range itemsData {
					if itemMap, ok := item.(map[string]interface{}); ok {
						itemAmount := itemMap["amount"].(map[string]interface{})
						item := OrderItem{
							RetailerID: itemMap["retailer_id"].(string),
							Name:       itemMap["name"].(string),
							Quantity:   int(itemMap["quantity"].(float64)),
							Amount: OrderAmountWithOffset{
								Value:  int(itemAmount["value"].(float64)),
								Offset: int(itemAmount["offset"].(float64)),
							},
						}

						if itemMap["sale_amount"] != nil {
							saleAmount := itemMap["sale_amount"].(map[string]interface{})
							item.SaleAmount = &OrderAmountWithOffset{
								Value:  int(saleAmount["value"].(float64)),
								Offset: int(saleAmount["offset"].(float64)),
							}
						}

						orderDetailsMessage.Order.Items[i] = item
					}
				}
			}
			if subtotal, ok := orderData["subtotal"].(float64); ok {
				orderDetailsMessage.Order.Subtotal = int(subtotal)
			}
			if taxData, ok := orderData["tax"].(map[string]interface{}); ok {
				orderDetailsMessage.Order.Tax = OrderAmountWithDescription{}
				if value, ok := taxData["value"].(float64); ok {
					orderDetailsMessage.Order.Tax.Value = int(value)
				}
				if description, ok := taxData["description"].(string); ok {
					orderDetailsMessage.Order.Tax.Description = description
				}
			}
			if shippingData, ok := orderData["shipping"].(map[string]interface{}); ok {
				orderDetailsMessage.Order.Shipping = OrderAmountWithDescription{}
				if value, ok := shippingData["value"].(float64); ok {
					orderDetailsMessage.Order.Shipping.Value = int(value)
				}
				if description, ok := shippingData["description"].(string); ok {
					orderDetailsMessage.Order.Shipping.Description = description
				}
			}
			if discountData, ok := orderData["discount"].(map[string]interface{}); ok {
				orderDetailsMessage.Order.Discount = OrderDiscount{}
				if value, ok := discountData["value"].(float64); ok {
					orderDetailsMessage.Order.Discount.Value = int(value)
				}
				if description, ok := discountData["description"].(string); ok {
					orderDetailsMessage.Order.Discount.Description = description
				}
				if programName, ok := discountData["program_name"].(string); ok {
					orderDetailsMessage.Order.Discount.ProgramName = programName
				}
			}
		}
		return orderDetailsMessage
	}

	return nil
}

func (m *mockMsg) Buttons() []ButtonComponent {
	if m.metadata == nil {
		return nil
	}

	var metadata map[string]interface{}
	err := json.Unmarshal(m.metadata, &metadata)
	if err != nil {
		return nil
	}

	if metadata == nil {
		return nil
	}

	if buttonsData, ok := metadata["buttons"].([]interface{}); ok {
		buttons := make([]ButtonComponent, len(buttonsData))
		for i, button := range buttonsData {
			buttonMap := button.(map[string]interface{})
			buttons[i] = ButtonComponent{
				SubType:    buttonMap["sub_type"].(string),
				Parameters: []ButtonParam{},
			}

			if buttonMap["parameters"] != nil {
				parameters := buttonMap["parameters"].([]interface{})
				for _, parameter := range parameters {
					parameterMap := parameter.(map[string]interface{})
					buttons[i].Parameters = append(buttons[i].Parameters, ButtonParam{
						Type: parameterMap["type"].(string),
						Text: parameterMap["text"].(string),
					})
				}
			}
		}
		return buttons
	}

	return nil
}

//-----------------------------------------------------------------------------
// Mock status implementation
//-----------------------------------------------------------------------------

type mockMsgStatus struct {
	channel    Channel
	id         MsgID
	oldURN     urns.URN
	newURN     urns.URN
	externalID string
	status     MsgStatusValue
	createdOn  time.Time

	logs []*ChannelLog
}

func (m *mockMsgStatus) ChannelUUID() ChannelUUID { return m.channel.UUID() }
func (m *mockMsgStatus) ID() MsgID                { return m.id }
func (m *mockMsgStatus) EventID() int64           { return int64(m.id) }

func (m *mockMsgStatus) SetUpdatedURN(old, new urns.URN) error {
	m.oldURN = old
	m.newURN = new
	return nil
}
func (m *mockMsgStatus) UpdatedURN() (urns.URN, urns.URN) {
	return m.oldURN, m.newURN
}
func (m *mockMsgStatus) HasUpdatedURN() bool {
	if m.oldURN != urns.NilURN && m.newURN != urns.NilURN {
		return true
	}
	return false
}

func (m *mockMsgStatus) ExternalID() string      { return m.externalID }
func (m *mockMsgStatus) SetExternalID(id string) { m.externalID = id }

func (m *mockMsgStatus) Status() MsgStatusValue          { return m.status }
func (m *mockMsgStatus) SetStatus(status MsgStatusValue) { m.status = status }

func (m *mockMsgStatus) Logs() []*ChannelLog    { return m.logs }
func (m *mockMsgStatus) AddLog(log *ChannelLog) { m.logs = append(m.logs, log) }

//-----------------------------------------------------------------------------
// Mock channel event implementation
//-----------------------------------------------------------------------------

type mockChannelEvent struct {
	channel    Channel
	eventType  ChannelEventType
	urn        urns.URN
	createdOn  time.Time
	occurredOn time.Time

	contactName string
	extra       map[string]interface{}

	logs []*ChannelLog
}

func (e *mockChannelEvent) EventID() int64                { return 0 }
func (e *mockChannelEvent) ChannelUUID() ChannelUUID      { return e.channel.UUID() }
func (e *mockChannelEvent) EventType() ChannelEventType   { return e.eventType }
func (e *mockChannelEvent) CreatedOn() time.Time          { return e.createdOn }
func (e *mockChannelEvent) OccurredOn() time.Time         { return e.occurredOn }
func (e *mockChannelEvent) Extra() map[string]interface{} { return e.extra }
func (e *mockChannelEvent) ContactName() string           { return e.contactName }
func (e *mockChannelEvent) URN() urns.URN                 { return e.urn }

func (e *mockChannelEvent) WithExtra(extra map[string]interface{}) ChannelEvent {
	e.extra = extra
	return e
}
func (e *mockChannelEvent) WithContactName(name string) ChannelEvent {
	e.contactName = name
	return e
}
func (e *mockChannelEvent) WithOccurredOn(time time.Time) ChannelEvent {
	e.occurredOn = time
	return e
}

func (e *mockChannelEvent) Logs() []*ChannelLog    { return e.logs }
func (e *mockChannelEvent) AddLog(log *ChannelLog) { e.logs = append(e.logs, log) }

//-----------------------------------------------------------------------------
// Mock Contact implementation
//-----------------------------------------------------------------------------

type mockContact struct {
	channel Channel
	urn     urns.URN
	auth    string
	uuid    ContactUUID
}

func (c *mockContact) UUID() ContactUUID { return c.uuid }

func ReadFile(path string) []byte {
	d, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return d
}
