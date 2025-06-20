package rapidpro

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/buger/jsonparser"
	"github.com/gabriel-vasile/mimetype"

	"mime"

	"github.com/gomodule/redigo/redis"
	"github.com/lib/pq"
	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/metrics"
	"github.com/nyaruka/courier/queue"
	"github.com/nyaruka/courier/utils"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/null"
	"github.com/sirupsen/logrus"
	filetype "gopkg.in/h2non/filetype.v1"
)

// MsgDirection is the direction of a message
type MsgDirection string

// Possible values for MsgDirection
const (
	MsgIncoming     MsgDirection = "I"
	MsgOutgoing     MsgDirection = "O"
	NilMsgDirection MsgDirection = ""
)

// MsgVisibility is the visibility of a message
type MsgVisibility string

// Possible values for MsgVisibility
const (
	MsgVisible  MsgVisibility = "V"
	MsgDeleted  MsgVisibility = "D"
	MsgArchived MsgVisibility = "A"
)

// WriteMsg creates a message given the passed in arguments
func writeMsg(ctx context.Context, b *backend, msg courier.Msg) error {
	m := msg.(*DBMsg)

	// this msg has already been written (we received it twice), we are a no op
	if m.alreadyWritten {
		return nil
	}

	channel := m.Channel()

	// if we have media, go download it to S3
	for i, attachment := range m.Attachments_ {
		if strings.HasPrefix(attachment, "http") {
			url, err := downloadMediaToS3(ctx, b, channel, m.OrgID_, m.UUID_, attachment)
			if err != nil {
				return err
			}
			m.Attachments_[i] = url
		}
	}

	// try to write it our db
	err := writeMsgToDB(ctx, b, m)

	// fail? log
	if err != nil {
		logrus.WithError(err).WithField("msg", m.UUID().String()).Error("error writing to db")
	}

	// if we failed write to spool
	if err != nil {
		err = courier.WriteToSpool(b.config.SpoolDir, "msgs", m)
	}
	// mark this msg as having been seen
	writeMsgSeen(b, m)
	return err
}

// newMsg creates a new DBMsg object with the passed in parameters
func newMsg(direction MsgDirection, channel courier.Channel, urn urns.URN, text string) *DBMsg {
	now := time.Now()
	dbChannel := channel.(*DBChannel)

	return &DBMsg{
		OrgID_:        dbChannel.OrgID(),
		UUID_:         courier.NewMsgUUID(),
		Direction_:    direction,
		Status_:       courier.MsgPending,
		Visibility_:   MsgVisible,
		HighPriority_: false,
		Text_:         text,

		ChannelID_:   dbChannel.ID(),
		ChannelUUID_: dbChannel.UUID(),

		URN_:          urn,
		MessageCount_: 1,

		NextAttempt_: now,
		CreatedOn_:   now,
		ModifiedOn_:  now,
		QueuedOn_:    now,

		channel:        dbChannel,
		workerToken:    "",
		alreadyWritten: false,
	}
}

const insertMsgSQL = `
INSERT INTO
	msgs_msg(org_id, uuid, direction, text, attachments, msg_count, error_count, high_priority, status,
             visibility, external_id, channel_id, contact_id, contact_urn_id, created_on, modified_on, next_attempt, queued_on, sent_on, metadata)
    VALUES(:org_id, :uuid, :direction, :text, :attachments, :msg_count, :error_count, :high_priority, :status,
           :visibility, :external_id, :channel_id, :contact_id, :contact_urn_id, :created_on, :modified_on, :next_attempt, :queued_on, :sent_on, :metadata)
RETURNING id
`

func writeMsgToDB(ctx context.Context, b *backend, m *DBMsg) error {
	// grab the contact for this msg
	contact, err := contactForURN(ctx, b, m.OrgID_, m.channel, m.URN_, m.URNAuth_, m.ContactName_)

	// our db is down, write to the spool, we will write/queue this later
	if err != nil {
		return err
	}

	// set our contact and urn ids from our contact
	m.ContactID_ = contact.ID_
	m.ContactURNID_ = contact.URNID_

	rows, err := b.db.NamedQueryContext(ctx, insertMsgSQL, m)
	if err != nil {
		return err
	}
	defer rows.Close()

	rows.Next()
	err = rows.Scan(&m.ID_)
	if err != nil {
		return err
	}

	// queue this up to be handled by RapidPro
	rc := b.redisPool.Get()
	defer rc.Close()
	err = queueMsgHandling(rc, contact, m)

	// if we had a problem queueing the handling, log it, but our message is written, it'll
	// get picked up by our rapidpro catch-all after a period
	if err != nil {
		logrus.WithError(err).WithField("msg_id", m.ID_).Error("error queueing msg handling")
	}

	return nil
}

const selectMsgSQL = `
SELECT
	org_id,
	direction,
	text,
	attachments,
	msg_count,
	error_count,
	high_priority,
	status,
	visibility,
	external_id,
	channel_id,
	contact_id,
	contact_urn_id,
	created_on,
	modified_on,
	next_attempt,
	queued_on,
	sent_on
FROM
	msgs_msg
WHERE
	id = $1
`

const selectMsgByUUIDSQL = `
SELECT
	id,
	uuid,
	org_id,
	direction,
	text,
	attachments,
	msg_count,
	error_count,
	high_priority,
	status,
	visibility,
	external_id,
	channel_id,
	contact_id,
	contact_urn_id,
	created_on,
	modified_on,
	queued_on,
	sent_on
FROM
	msgs_msg
WHERE
	uuid = $1
`

const selectChannelSQL = `
SELECT
	org_id,
	ch.id as id,
	ch.uuid as uuid,
	ch.name as name,
	channel_type, schemes,
	address, role,
	ch.country as country,
	ch.config as config,
	org.config as org_config,
	org.is_anon as org_is_anon
FROM
	channels_channel ch
	JOIN orgs_org org on ch.org_id = org.id
WHERE
    ch.id = $1
`

//-----------------------------------------------------------------------------
// Media download and classification
//-----------------------------------------------------------------------------

func downloadMediaToS3(ctx context.Context, b *backend, channel courier.Channel, orgID OrgID, msgUUID courier.MsgUUID, mediaURL string) (string, error) {

	parsedURL, err := url.Parse(mediaURL)
	if err != nil {
		return "", err
	}

	var req *http.Request
	handler := courier.GetHandler(channel.ChannelType())
	if handler != nil {
		builder, isBuilder := handler.(courier.MediaDownloadRequestBuilder)
		if isBuilder {
			req, err = builder.BuildDownloadMediaRequest(ctx, b, channel, parsedURL.String())

			// in the case of errors, we log the error but move onwards anyways
			if err != nil {
				logrus.WithField("channel_uuid", channel.UUID()).WithField("channel_type", channel.ChannelType()).WithField("media_url", mediaURL).WithError(err).Error("unable to build media download request")
			}
		}
	}

	if req == nil {
		// first fetch our media
		req, err = http.NewRequest(http.MethodGet, mediaURL, nil)
		if err != nil {
			return "", err
		}
	}

	switch channel.ChannelType() {
	case "WAC":
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", b.config.WhatsappAdminSystemUserToken))

	case "SL":
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", channel.StringConfigForKey("user_token", "")))
	}

	resp, err := utils.GetHTTPClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	mimeType := ""
	extension := filepath.Ext(parsedURL.Path)
	if extension != "" {
		extension = extension[1:]
	}

	// first try getting our mime type from the first 300 bytes of our body
	fileType, _ := filetype.Match(body[:300])
	mimeT := mimetype.Detect(body)

	if fileType != filetype.Unknown || mimeT.String() != "application/octet-stream" {
		if mimeT.String() == fileType.MIME.Type {
			mimeType = fileType.MIME.Value
			extension = fileType.Extension
		} else if mimeT.String() != "application/zip" && mimeT.String() != "application/octet-stream" {
			mimeType = mimeT.String()
			extension = mimeT.Extension()[1:]
		} else {
			// if that didn't work, try from our extension
			fileType = filetype.GetType(extension)
			if fileType != filetype.Unknown {
				mimeType = fileType.MIME.Value
				extension = fileType.Extension
			}
		}
	}

	// we still don't know our mime type, use our content header instead
	if mimeType == "" {
		mimeType, _, _ = mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if extension == "" {
			extensions, err := mime.ExtensionsByType(mimeType)
			if extensions == nil || err != nil {
				extension = ""
			} else {
				extension = extensions[0][1:]
			}
		}
	}

	// create our filename
	filename := msgUUID.String()
	if extension != "" {
		filename = fmt.Sprintf("%s.%s", msgUUID, extension)
	}
	path := filepath.Join(b.config.S3MediaPrefix, strconv.FormatInt(int64(orgID), 10), filename[:4], filename[4:8], filename)
	if !strings.HasPrefix(path, "/") {
		path = fmt.Sprintf("/%s", path)
	}

	s3URL, err := b.storage.Put(ctx, path, mimeType, body)
	if err != nil {
		return "", err
	}

	// get the file size in bytes and increase our media upload size metric
	metrics.IncrementMediaUploadSize(len(body))

	// return our new media URL, which is prefixed by our content type
	return fmt.Sprintf("%s:%s", mimeType, s3URL), nil
}

//-----------------------------------------------------------------------------
// Msg flusher for flushing failed writes
//-----------------------------------------------------------------------------

func (b *backend) flushMsgFile(filename string, contents []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	msg := &DBMsg{}
	err := json.Unmarshal(contents, msg)
	if err != nil {
		log.Printf("ERROR unmarshalling spool file '%s', renaming: %s\n", filename, err)
		os.Rename(filename, fmt.Sprintf("%s.error", filename))
		return nil
	}

	// look up our channel
	channel, err := b.GetChannel(ctx, courier.AnyChannelType, msg.ChannelUUID_)
	if err != nil {
		return err
	}
	msg.channel = channel.(*DBChannel)

	// try to write it our db
	err = writeMsgToDB(ctx, b, msg)

	// fail? oh well, we'll try again later
	return err
}

//-----------------------------------------------------------------------------
// Deduping utility methods
//-----------------------------------------------------------------------------

var luaMsgSeen = redis.NewScript(3, `-- KEYS: [Window, PrevWindow, URNFingerprint]
	-- try to look up in window
	local found = redis.call("hget", KEYS[1], KEYS[3])

	-- didn't find it, try in our previous window
	if not found then
		found = redis.call("hget", KEYS[2], KEYS[3])
	end

	-- return the fingerprint found
	return found
`)

// checkMsgSeen tries to look up whether a msg with the fingerprint passed in was seen in window or prevWindow. If
// found returns the UUID of that msg, if not returns empty string
func checkMsgSeen(b *backend, msg *DBMsg) courier.MsgUUID {
	r := b.redisPool.Get()
	defer r.Close()

	urnFingerprint := msg.urnFingerprint()

	now := time.Now().In(time.UTC)
	prev := now.Add(time.Second * -2)
	windowKey := fmt.Sprintf("seen:msgs:%s:%02d", now.Format("2006-01-02-15:04"), now.Second()/2*2)
	prevWindowKey := fmt.Sprintf("seen:msgs:%s:%02d", prev.Format("2006-01-02-15:04"), prev.Second()/2*2)

	// see if there were any messages received in the past 4 seconds
	found, _ := redis.String(luaMsgSeen.Do(r, windowKey, prevWindowKey, urnFingerprint))

	// if so, test whether the text it the same
	if found != "" {
		prevText := found[37:]
		foundSplit := strings.Split(found, "|")
		if len(foundSplit) > 2 && foundSplit[1] == "" {
			prevAtt := foundSplit[2]
			if prevAtt == string(msg.ExternalID_) {
				return courier.NewMsgUUIDFromString(found[:36])
			}
		} else if prevText == msg.Text() {
			// if it is the same, return the UUID
			return courier.NewMsgUUIDFromString(found[:36])
		}
	}
	return courier.NilMsgUUID
}

var luaWriteMsgSeen = redis.NewScript(3, `-- KEYS: [Window, URNFingerprint, UUIDText]
	redis.call("hset", KEYS[1], KEYS[2], KEYS[3])
	redis.call("expire", KEYS[1], 5)
`)

// writeMsgSeen writes that the message with the passed in fingerprint and UUID was seen in the
// passed in window
func writeMsgSeen(b *backend, msg *DBMsg) {
	r := b.redisPool.Get()
	defer r.Close()

	urnFingerprint := msg.urnFingerprint()
	var msgSeen string

	if msg.ExternalID_ != "" && msg.Attachments_ != nil {
		msgSeen = fmt.Sprintf("%s|%s|%s", msg.UUID().String(), msg.Text_, msg.ExternalID_)
	} else {
		msgSeen = fmt.Sprintf("%s|%s", msg.UUID().String(), msg.Text_)
	}

	now := time.Now().In(time.UTC)
	windowKey := fmt.Sprintf("seen:msgs:%s:%02d", now.Format("2006-01-02-15:04"), now.Second()/2*2)

	luaWriteMsgSeen.Do(r, windowKey, urnFingerprint, msgSeen)
}

// clearMsgSeen clears our seen incoming messages for the passed in channel and URN
func clearMsgSeen(rc redis.Conn, msg *DBMsg) {
	urnFingerprint := msg.urnFingerprint()

	now := time.Now().In(time.UTC)
	prev := now.Add(time.Second * -2)
	windowKey := fmt.Sprintf("seen:msgs:%s:%02d", now.Format("2006-01-02-15:04"), now.Second()/2*2)
	prevWindowKey := fmt.Sprintf("seen:msgs:%s:%02d", prev.Format("2006-01-02-15:04"), prev.Second()/2*2)

	rc.Send("hdel", windowKey, urnFingerprint)
	rc.Do("hdel", prevWindowKey, urnFingerprint)
}

var luaExternalIDSeen = redis.NewScript(3, `-- KEYS: [Window, PrevWindow, ExternalID]
	-- try to look up in window
	local found = redis.call("hget", KEYS[1], KEYS[3])

	-- didn't find it, try in our previous window
	if not found then
		found = redis.call("hget", KEYS[2], KEYS[3])
	end

	-- return the fingerprint found
	return found
`)

func checkExternalIDSeen(b *backend, msg courier.Msg) courier.MsgUUID {
	r := b.redisPool.Get()
	defer r.Close()

	urnFingerprint := fmt.Sprintf("%s:%s|%s", msg.Channel().UUID(), msg.URN().Identity(), msg.ExternalID())

	now := time.Now().In(time.UTC)
	prev := now.Add(time.Hour * -24)
	windowKey := fmt.Sprintf("seen:externalid:%s", now.Format("2006-01-02"))
	prevWindowKey := fmt.Sprintf("seen:externalid:%s", prev.Format("2006-01-02"))

	// see if there were any messages received in the past 24 hours
	found, _ := redis.String(luaExternalIDSeen.Do(r, windowKey, prevWindowKey, urnFingerprint))

	// if so, test whether the text it the same
	if found != "" {
		prevText := found[37:]

		// if it is the same, return the UUID
		if prevText == msg.Text() {
			return courier.NewMsgUUIDFromString(found[:36])
		}
	}
	return courier.NilMsgUUID
}

var luaWriteExternalIDSeen = redis.NewScript(3, `-- KEYS: [Window, ExternalID, Seen]
	redis.call("hset", KEYS[1], KEYS[2], KEYS[3])
	redis.call("expire", KEYS[1], 86400)
`)

func writeExternalIDSeen(b *backend, msg courier.Msg) {
	r := b.redisPool.Get()
	defer r.Close()

	urnFingerprint := fmt.Sprintf("%s:%s|%s", msg.Channel().UUID(), msg.URN().Identity(), msg.ExternalID())
	uuidText := fmt.Sprintf("%s|%s", msg.UUID().String(), msg.Text())

	now := time.Now().In(time.UTC)
	windowKey := fmt.Sprintf("seen:externalid:%s", now.Format("2006-01-02"))

	luaWriteExternalIDSeen.Do(r, windowKey, urnFingerprint, uuidText)
}

//-----------------------------------------------------------------------------
// Our implementation of Msg interface
//-----------------------------------------------------------------------------

// DBMsg is our base struct to represent msgs both in our JSON and db representations
type DBMsg struct {
	OrgID_                OrgID                  `json:"org_id"          db:"org_id"`
	ID_                   courier.MsgID          `json:"id"              db:"id"`
	UUID_                 courier.MsgUUID        `json:"uuid"            db:"uuid"`
	Direction_            MsgDirection           `json:"direction"       db:"direction"`
	Status_               courier.MsgStatusValue `json:"status"          db:"status"`
	Visibility_           MsgVisibility          `json:"visibility"      db:"visibility"`
	HighPriority_         bool                   `json:"high_priority"   db:"high_priority"`
	URN_                  urns.URN               `json:"urn"`
	URNAuth_              string                 `json:"urn_auth"`
	Text_                 string                 `json:"text"            db:"text"`
	Attachments_          pq.StringArray         `json:"attachments"     db:"attachments"`
	ExternalID_           null.String            `json:"external_id"     db:"external_id"`
	ResponseToID_         courier.MsgID          `json:"response_to_id"  db:"response_to_id"`
	ResponseToExternalID_ string                 `json:"response_to_external_id"`
	IsResend_             bool                   `json:"is_resend,omitempty"`
	Metadata_             *json.RawMessage       `json:"metadata,omitempty"        db:"metadata"`

	ChannelID_    courier.ChannelID `json:"channel_id"      db:"channel_id"`
	ContactID_    ContactID         `json:"contact_id"      db:"contact_id"`
	ContactURNID_ ContactURNID      `json:"contact_urn_id"  db:"contact_urn_id"`

	MessageCount_ int `json:"msg_count"    db:"msg_count"`
	ErrorCount_   int `json:"error_count"  db:"error_count"`

	ChannelUUID_ courier.ChannelUUID `json:"channel_uuid"`
	ContactName_ string              `json:"contact_name"`

	NextAttempt_ time.Time  `json:"next_attempt"  db:"next_attempt"`
	CreatedOn_   time.Time  `json:"created_on"    db:"created_on"`
	ModifiedOn_  time.Time  `json:"modified_on"   db:"modified_on"`
	QueuedOn_    time.Time  `json:"queued_on"     db:"queued_on"`
	SentOn_      *time.Time `json:"sent_on"       db:"sent_on"`

	// fields used to allow courier to update a session's timeout when a message is sent for efficient timeout behavior
	SessionID_            SessionID  `json:"session_id,omitempty"`
	SessionTimeout_       int        `json:"session_timeout,omitempty"`
	SessionWaitStartedOn_ *time.Time `json:"session_wait_started_on,omitempty"`
	SessionStatus_        string     `json:"session_status,omitempty"`

	channel        *DBChannel
	workerToken    queue.WorkerToken
	alreadyWritten bool
	quickReplies   []string
	textLanguage   string

	products    []map[string]interface{}
	listMessage courier.ListMessage
}

func (m *DBMsg) ID() courier.MsgID            { return m.ID_ }
func (m *DBMsg) EventID() int64               { return int64(m.ID_) }
func (m *DBMsg) UUID() courier.MsgUUID        { return m.UUID_ }
func (m *DBMsg) Text() string                 { return m.Text_ }
func (m *DBMsg) Attachments() []string        { return []string(m.Attachments_) }
func (m *DBMsg) ExternalID() string           { return string(m.ExternalID_) }
func (m *DBMsg) URN() urns.URN                { return m.URN_ }
func (m *DBMsg) URNAuth() string              { return m.URNAuth_ }
func (m *DBMsg) ContactName() string          { return m.ContactName_ }
func (m *DBMsg) HighPriority() bool           { return m.HighPriority_ }
func (m *DBMsg) ReceivedOn() *time.Time       { return m.SentOn_ }
func (m *DBMsg) SentOn() *time.Time           { return m.SentOn_ }
func (m *DBMsg) ResponseToID() courier.MsgID  { return m.ResponseToID_ }
func (m *DBMsg) ResponseToExternalID() string { return m.ResponseToExternalID_ }
func (m *DBMsg) IsResend() bool               { return m.IsResend_ }

func (m *DBMsg) Channel() courier.Channel { return m.channel }
func (m *DBMsg) SessionStatus() string    { return m.SessionStatus_ }

func (m *DBMsg) QuickReplies() []string {
	if m.quickReplies != nil {
		return m.quickReplies
	}

	m.quickReplies = []string{}

	if m.Metadata_ == nil || *m.Metadata_ == nil {
		return m.quickReplies
	}

	jsonparser.ArrayEach(
		*m.Metadata_,
		func(value []byte, dataType jsonparser.ValueType, offset int, err error) {
			m.quickReplies = append(m.quickReplies, string(value))
		},
		"quick_replies")
	return m.quickReplies
}

func (m *DBMsg) Topic() string {
	if m.Metadata_ == nil {
		return ""
	}
	topic, _, _, _ := jsonparser.Get(*m.Metadata_, "topic")
	return string(topic)
}

func (m *DBMsg) TextLanguage() string {
	if m.textLanguage != "" {
		return m.textLanguage
	}
	if m.Metadata_ == nil {
		return ""
	}

	textLanguage, _, _, _ := jsonparser.Get(*m.Metadata_, "text_language")
	return string(textLanguage)
}

func (m *DBMsg) IGCommentID() string {
	if m.Metadata_ == nil {
		return ""
	}

	igCommentID, _, _, _ := jsonparser.Get(*m.Metadata_, "ig_comment_id")
	return string(igCommentID)
}

func (m *DBMsg) IGResponseType() string {
	if m.Metadata_ == nil {
		return ""
	}
	igResponseType, _, _, _ := jsonparser.Get(*m.Metadata_, "ig_response_type")
	return string(igResponseType)
}

func (m *DBMsg) IGTag() string {
	if m.Metadata_ == nil {
		return ""
	}
	igTag, _, _, _ := jsonparser.Get(*m.Metadata_, "ig_tag")
	return string(igTag)
}

// Metadata returns the metadata for this message if it exists
func (m *DBMsg) Metadata() json.RawMessage {
	if m.Metadata_ == nil {
		return nil
	}

	if *m.Metadata_ == nil {
		return nil
	}

	return *m.Metadata_
}

// fingerprint returns a fingerprint for this msg, suitable for figuring out if this is a dupe
func (m *DBMsg) urnFingerprint() string {
	return fmt.Sprintf("%s:%s", m.ChannelUUID_, m.URN_.Identity())
}

// WithContactName can be used to set the contact name on a msg
func (m *DBMsg) WithContactName(name string) courier.Msg { m.ContactName_ = name; return m }

// WithReceivedOn can be used to set sent_on on a msg in a chained call
func (m *DBMsg) WithReceivedOn(date time.Time) courier.Msg { m.SentOn_ = &date; return m }

// WithExternalID can be used to set the external id on a msg in a chained call
func (m *DBMsg) WithExternalID(id string) courier.Msg { m.ExternalID_ = null.String(id); return m }

// WithID can be used to set the id on a msg in a chained call
func (m *DBMsg) WithID(id courier.MsgID) courier.Msg { m.ID_ = id; return m }

// WithUUID can be used to set the id on a msg in a chained call
func (m *DBMsg) WithUUID(uuid courier.MsgUUID) courier.Msg { m.UUID_ = uuid; return m }

// WithMetadata can be used to add metadata to a Msg
func (m *DBMsg) WithMetadata(metadata json.RawMessage) courier.Msg { m.Metadata_ = &metadata; return m }

// WithAttachment can be used to append to the media urls for a message
func (m *DBMsg) WithAttachment(url string) courier.Msg {
	m.Attachments_ = append(m.Attachments_, url)
	return m
}

// WithURNAuth can be used to add a URN auth setting to a message
func (m *DBMsg) WithURNAuth(auth string) courier.Msg {
	m.URNAuth_ = auth
	return m
}

func GetMsg(b *backend, id courier.MsgID) (*DBMsg, error) {
	m := &DBMsg{
		ID_: id,
	}
	err := b.db.Get(m, selectMsgSQL, id)
	if err != nil {
		return nil, err
	}

	return m, nil
}

func GetMsgByUUID(b *backend, uuid string) (*DBMsg, error) {
	m := &DBMsg{}
	err := b.db.Get(m, selectMsgByUUIDSQL, uuid)
	if err != nil {
		return nil, err
	}

	return m, nil
}

func (m *DBMsg) Status() courier.MsgStatusValue { return m.Status_ }

func (m *DBMsg) Products() []map[string]interface{} {
	if m.products != nil {
		return m.products
	}

	if m.Metadata_ == nil {
		return nil
	}

	p, _, _, _ := jsonparser.Get(*m.Metadata_, "products")
	err := json.Unmarshal(p, &m.products)
	if err != nil {
		return nil
	}
	return m.products
}

func (m *DBMsg) Header() string {
	if m.Metadata_ == nil {
		return ""
	}
	header, _, _, _ := jsonparser.Get(*m.Metadata_, "header")
	return string(header)
}

func (m *DBMsg) Body() string {
	if m.Metadata_ == nil {
		return ""
	}
	body, _, _, _ := jsonparser.Get(*m.Metadata_, "body")
	return string(body)
}

func (m *DBMsg) Footer() string {
	if m.Metadata_ == nil {
		return ""
	}
	footer, _, _, _ := jsonparser.Get(*m.Metadata_, "footer")
	return string(footer)
}

func (m *DBMsg) Action() string {
	if m.Metadata_ == nil {
		return ""
	}
	action, _, _, _ := jsonparser.Get(*m.Metadata_, "action")
	return string(action)
}

func (m *DBMsg) SendCatalog() bool {
	if m.Metadata_ == nil {
		return false
	}
	byteValue, _, _, _ := jsonparser.Get(*m.Metadata_, "send_catalog")
	sendCatalog, err := strconv.ParseBool(string(byteValue))
	if err != nil {
		return false
	}
	return sendCatalog
}

func (m *DBMsg) HeaderType() string {
	if m.Metadata_ == nil {
		return ""
	}
	byteValue, _, _, _ := jsonparser.Get(*m.Metadata_, "header_type")
	return string(byteValue)
}

func (m *DBMsg) HeaderText() string {
	if m.Metadata_ == nil {
		return ""
	}
	byteValue, _, _, _ := jsonparser.Get(*m.Metadata_, "header_text")
	return string(byteValue)
}

func (m *DBMsg) InteractionType() string {
	if m.Metadata_ == nil {
		return ""
	}
	byteValue, _, _, _ := jsonparser.Get(*m.Metadata_, "interaction_type")
	return string(byteValue)
}

func (m *DBMsg) ListMessage() courier.ListMessage {
	if m.Metadata_ == nil {
		return courier.ListMessage{}
	}

	var metadata map[string]interface{}
	err := json.Unmarshal(*m.Metadata_, &metadata)
	if err != nil {
		return m.listMessage
	}

	if metadata == nil {
		return courier.ListMessage{}
	}

	if interactionType, ok := metadata["interaction_type"].(string); ok && interactionType == "list" {
		m.listMessage = courier.ListMessage{}

		if listMessageData, ok := metadata["list_message"].(map[string]interface{}); ok {
			if buttonText, ok := listMessageData["button_text"].(string); ok {
				m.listMessage.ButtonText = buttonText
			}

			if listItems, ok := listMessageData["list_items"].([]interface{}); ok {
				m.listMessage.ListItems = make([]courier.ListItems, len(listItems))
				for i, item := range listItems {
					if itemMap, ok := item.(map[string]interface{}); ok {
						m.listMessage.ListItems[i] = courier.ListItems{
							Title: itemMap["title"].(string),
							UUID:  itemMap["uuid"].(string),
						}

						if description, ok := itemMap["description"].(string); ok {
							m.listMessage.ListItems[i].Description = description
						}
					}
				}
			}
		}
	}

	return m.listMessage
}

func (m *DBMsg) CTAMessage() *courier.CTAMessage {
	if m.Metadata_ == nil {
		return nil
	}

	var metadata map[string]interface{}
	err := json.Unmarshal(*m.Metadata_, &metadata)
	if err != nil {
		return nil
	}

	if metadata == nil {
		return nil
	}

	if interactionType, ok := metadata["interaction_type"].(string); ok && interactionType == "cta_url" {
		if ctaMessageData, ok := metadata["cta_message"].(map[string]interface{}); ok {
			ctaMessage := &courier.CTAMessage{}
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

func (m *DBMsg) FlowMessage() *courier.FlowMessage {
	if m.Metadata_ == nil {
		return nil
	}

	var metadata map[string]interface{}
	err := json.Unmarshal(*m.Metadata_, &metadata)
	if err != nil {
		return nil
	}

	if metadata == nil {
		return nil
	}

	if interactionType, ok := metadata["interaction_type"].(string); ok && interactionType == "flow_msg" {
		if flowMessageData, ok := metadata["flow_message"].(map[string]interface{}); ok {
			flowMessage := &courier.FlowMessage{}
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
				flowMessage.FlowMode = strings.ToLower(flowMode)
			}
			return flowMessage
		}
	}
	return nil
}

func (m *DBMsg) OrderDetailsMessage() *courier.OrderDetailsMessage {
	if m.Metadata_ == nil {
		return nil
	}

	var metadata map[string]interface{}
	err := json.Unmarshal(*m.Metadata_, &metadata)
	if err != nil {
		return nil
	}

	if metadata == nil {
		return nil
	}

	if orderDetailsMessageData, ok := metadata["order_details_message"].(map[string]interface{}); ok {
		orderDetailsMessage := &courier.OrderDetailsMessage{}
		if referenceID, ok := orderDetailsMessageData["reference_id"].(string); ok {
			orderDetailsMessage.ReferenceID = referenceID
		}
		if paymentSettings, ok := orderDetailsMessageData["payment_settings"].(map[string]interface{}); ok {
			orderDetailsMessage.PaymentSettings = courier.OrderPaymentSettings{}
			if payment_type, ok := paymentSettings["type"].(string); ok {
				orderDetailsMessage.PaymentSettings.Type = payment_type
			}
			if payment_link, ok := paymentSettings["payment_link"].(string); ok {
				orderDetailsMessage.PaymentSettings.PaymentLink = payment_link
			}
			if pix_config, ok := paymentSettings["pix_config"].(map[string]interface{}); ok {
				orderDetailsMessage.PaymentSettings.PixConfig = courier.OrderPixConfig{}
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
			orderDetailsMessage.Order = courier.Order{}
			if itemsData, ok := orderData["items"].([]interface{}); ok {
				orderDetailsMessage.Order.Items = make([]courier.OrderItem, len(itemsData))
				for i, item := range itemsData {
					if itemMap, ok := item.(map[string]interface{}); ok {
						itemAmount := itemMap["amount"].(map[string]interface{})
						item := courier.OrderItem{
							RetailerID: itemMap["retailer_id"].(string),
							Name:       itemMap["name"].(string),
							Quantity:   int(itemMap["quantity"].(float64)),
							Amount: courier.OrderAmountWithOffset{
								Value:  int(itemAmount["value"].(float64)),
								Offset: int(itemAmount["offset"].(float64)),
							},
						}

						if itemMap["sale_amount"] != nil {
							saleAmount := itemMap["sale_amount"].(map[string]interface{})
							item.SaleAmount = &courier.OrderAmountWithOffset{
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
				orderDetailsMessage.Order.Tax = courier.OrderAmountWithDescription{}
				if value, ok := taxData["value"].(float64); ok {
					orderDetailsMessage.Order.Tax.Value = int(value)
				}
				if description, ok := taxData["description"].(string); ok {
					orderDetailsMessage.Order.Tax.Description = description
				}
			}
			if shippingData, ok := orderData["shipping"].(map[string]interface{}); ok {
				orderDetailsMessage.Order.Shipping = courier.OrderAmountWithDescription{}
				if value, ok := shippingData["value"].(float64); ok {
					orderDetailsMessage.Order.Shipping.Value = int(value)
				}
				if description, ok := shippingData["description"].(string); ok {
					orderDetailsMessage.Order.Shipping.Description = description
				}
			}
			if discountData, ok := orderData["discount"].(map[string]interface{}); ok {
				orderDetailsMessage.Order.Discount = courier.OrderDiscount{}
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

func (m *DBMsg) Buttons() []courier.ButtonComponent {
	if m.Metadata_ == nil {
		return nil
	}

	var metadata map[string]interface{}
	err := json.Unmarshal(*m.Metadata_, &metadata)
	if err != nil {
		return nil
	}

	if metadata == nil {
		return nil
	}

	if buttonsData, ok := metadata["buttons"].([]interface{}); ok {
		buttons := make([]courier.ButtonComponent, len(buttonsData))
		for i, button := range buttonsData {
			buttonMap := button.(map[string]interface{})
			buttons[i] = courier.ButtonComponent{
				SubType:    buttonMap["sub_type"].(string),
				Parameters: []courier.ButtonParam{},
			}

			if buttonMap["parameters"] != nil {
				parameters := buttonMap["parameters"].([]interface{})
				for _, parameter := range parameters {
					parameterMap := parameter.(map[string]interface{})
					buttons[i].Parameters = append(buttons[i].Parameters, courier.ButtonParam{
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

func (m *DBMsg) ActionType() courier.MsgActionType {
	if m.Metadata_ == nil {
		return courier.MsgActionNone
	}
	actionType, _, _, _ := jsonparser.Get(*m.Metadata_, "action_type")
	return courier.MsgActionType(actionType)
}

func (m *DBMsg) ActionExternalID() string {
	if m.Metadata_ == nil {
		return ""
	}
	actionExternalID, _, _, _ := jsonparser.Get(*m.Metadata_, "action_external_id")
	return string(actionExternalID)
}
