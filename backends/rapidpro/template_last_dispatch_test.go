package rapidpro

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nyaruka/courier"
	"github.com/stretchr/testify/require"
)

func (ts *BackendTestSuite) TestRecordTemplateLastDispatchCreates() {
	ctx := context.Background()

	orgID := int64(1)
	templateUUID := "44019537-9afe-4898-9626-a5c724d169ef"
	name := "template_test"
	metaTemplateID := "123456789"
	firedOn := time.Now().UTC().Round(time.Microsecond)

	err := RecordTemplateLastDispatch(ctx, ts.b.db, orgID, templateUUID, name, metaTemplateID, firedOn, nil)
	ts.NoError(err)

	var got struct {
		OrgID          int64     `db:"org_id"`
		TemplateUUID   string    `db:"template_uuid"`
		Name           string    `db:"name"`
		MetaTemplateID string    `db:"meta_template_id"`
		LastFiredOn    time.Time `db:"last_fired_on"`
		TemplateID     *int      `db:"template_id"`
	}
	err = ts.b.db.Get(&got, `
		SELECT org_id, template_uuid, name, meta_template_id, last_fired_on, template_id
		FROM templates_templatelastdispatch
		WHERE org_id = $1 AND meta_template_id = $2`, orgID, metaTemplateID)
	ts.NoError(err)

	ts.Equal(orgID, got.OrgID)
	ts.Equal(templateUUID, got.TemplateUUID)
	ts.Equal(name, got.Name)
	ts.Equal(metaTemplateID, got.MetaTemplateID)
	ts.Equal(firedOn, got.LastFiredOn.UTC())
	ts.Nil(got.TemplateID)
}

func (ts *BackendTestSuite) TestRecordTemplateLastDispatchUpdatesLastFiredOn() {
	ctx := context.Background()

	orgID := int64(1)
	templateUUID := "44019537-9afe-4898-9626-a5c724d169ef"
	metaTemplateID := "987654321"
	firstFiredOn := time.Now().UTC().Add(-time.Hour).Round(time.Microsecond)
	secondFiredOn := time.Now().UTC().Round(time.Microsecond)

	err := RecordTemplateLastDispatch(ctx, ts.b.db, orgID, templateUUID, "template_v1", metaTemplateID, firstFiredOn, nil)
	ts.NoError(err)

	err = RecordTemplateLastDispatch(ctx, ts.b.db, orgID, templateUUID, "template_v2", metaTemplateID, secondFiredOn, nil)
	ts.NoError(err)

	var lastFiredOn time.Time
	var name string
	err = ts.b.db.QueryRow(`
		SELECT name, last_fired_on
		FROM templates_templatelastdispatch
		WHERE org_id = $1 AND meta_template_id = $2`, orgID, metaTemplateID).Scan(&name, &lastFiredOn)
	ts.NoError(err)
	ts.Equal("template_v2", name)
	ts.Equal(secondFiredOn, lastFiredOn.UTC())
}

func (ts *BackendTestSuite) TestRecordTemplateLastDispatchSkipsOlderTimestamp() {
	ctx := context.Background()

	orgID := int64(1)
	templateUUID := "44019537-9afe-4898-9626-a5c724d169ef"
	metaTemplateID := "555555555"
	newerFiredOn := time.Now().UTC().Round(time.Microsecond)
	olderFiredOn := newerFiredOn.Add(-2 * time.Hour)

	err := RecordTemplateLastDispatch(ctx, ts.b.db, orgID, templateUUID, "template_new", metaTemplateID, newerFiredOn, nil)
	ts.NoError(err)

	err = RecordTemplateLastDispatch(ctx, ts.b.db, orgID, templateUUID, "template_old", metaTemplateID, olderFiredOn, nil)
	ts.NoError(err)

	var got struct {
		Name        string    `db:"name"`
		LastFiredOn time.Time `db:"last_fired_on"`
	}
	err = ts.b.db.Get(&got, `
		SELECT name, last_fired_on
		FROM templates_templatelastdispatch
		WHERE org_id = $1 AND meta_template_id = $2`, orgID, metaTemplateID)
	ts.NoError(err)
	ts.Equal("template_new", got.Name)
	ts.Equal(newerFiredOn, got.LastFiredOn.UTC())
}

func TestOrgIDFromMsg(t *testing.T) {
	channel := testChannelWithOrg(1)
	msg := newTestOutgoingMsg(channel, "")

	orgID, ok := orgIDFromMsg(msg)
	require.True(t, ok)
	require.Equal(t, OrgID(1), orgID)
}

func TestTemplateLastDispatchWorkerDropsWhenBufferFull(t *testing.T) {
	worker := &templateLastDispatchWorker{
		events:   make(chan templateLastDispatchEvent, 1),
		stopChan: make(chan struct{}),
	}

	event := templateLastDispatchEvent{
		OrgID:          1,
		TemplateUUID:   "44019537-9afe-4898-9626-a5c724d169ef",
		Name:           "template_test",
		MetaTemplateID: "123456789",
		FiredOn:        time.Now(),
	}

	worker.enqueue(event)

	done := make(chan struct{})
	go func() {
		worker.enqueue(event)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("enqueue blocked when buffer was full")
	}

	require.Equal(t, 1, len(worker.events))
}

func TestQueueTemplateLastDispatchDoesNotBlock(t *testing.T) {
	channel := testChannelWithOrg(1)
	msg := newTestOutgoingMsg(channel, `{"templating":{"template":{"uuid":"44019537-9afe-4898-9626-a5c724d169ef","name":"template_test","id":"123456789"},"language":"por"}}`)

	b := &backend{
		templateLastDispatchWorker: &templateLastDispatchWorker{
			events:   make(chan templateLastDispatchEvent, 1),
			stopChan: make(chan struct{}),
		},
	}

	done := make(chan struct{})
	go func() {
		b.QueueTemplateLastDispatch(context.Background(), msg, courier.TemplateLastDispatchData{
			TemplateUUID:   "44019537-9afe-4898-9626-a5c724d169ef",
			Name:           "template_test",
			MetaTemplateID: "123456789",
		}, time.Now())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("QueueTemplateLastDispatch blocked the caller")
	}
}

func testChannelWithOrg(orgID int64) *DBChannel {
	return &DBChannel{OrgID_: OrgID(orgID)}
}

func newTestOutgoingMsg(channel *DBChannel, metadata string) *DBMsg {
	msg := &DBMsg{
		OrgID_:  channel.OrgID_,
		channel: channel,
	}

	if metadata != "" {
		raw := json.RawMessage(metadata)
		msg.Metadata_ = &raw
	}

	return msg
}
