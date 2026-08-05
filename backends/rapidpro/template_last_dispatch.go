package rapidpro

import (
	"context"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/metrics"
	"github.com/sirupsen/logrus"
)

const (
	templateLastDispatchBufferSize = 1000
	templateLastDispatchWorkers    = 3
	templateLastDispatchDBTimeout  = 2 * time.Second

	upsertTemplateLastDispatchSQL = `
INSERT INTO templates_templatelastdispatch
    (org_id, template_id, template_uuid, name, meta_template_id, last_fired_on)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (org_id, meta_template_id)
DO UPDATE SET
    name = EXCLUDED.name,
    template_uuid = EXCLUDED.template_uuid,
    last_fired_on = EXCLUDED.last_fired_on,
    template_id = COALESCE(EXCLUDED.template_id, templates_templatelastdispatch.template_id)
WHERE EXCLUDED.last_fired_on >= templates_templatelastdispatch.last_fired_on`
)

type templateLastDispatchEvent struct {
	OrgID          int64
	TemplateUUID   string
	Name           string
	MetaTemplateID string
	FiredOn        time.Time
	TemplateID     *int
}

type templateLastDispatchWorker struct {
	db       *sqlx.DB
	events   chan templateLastDispatchEvent
	stopChan chan struct{}
	wg       sync.WaitGroup
}

func newTemplateLastDispatchWorker(db *sqlx.DB) *templateLastDispatchWorker {
	return &templateLastDispatchWorker{
		db:       db,
		events:   make(chan templateLastDispatchEvent, templateLastDispatchBufferSize),
		stopChan: make(chan struct{}),
	}
}

func (w *templateLastDispatchWorker) start() {
	for i := 0; i < templateLastDispatchWorkers; i++ {
		w.wg.Add(1)
		go w.run(i)
	}
}

func (w *templateLastDispatchWorker) run(workerID int) {
	defer w.wg.Done()
	log := logrus.WithField("comp", "template_last_dispatch").WithField("worker_id", workerID)

	for {
		select {
		case <-w.stopChan:
			return
		case event := <-w.events:
			err := RecordTemplateLastDispatch(context.Background(), w.db, event.OrgID, event.TemplateUUID, event.Name, event.MetaTemplateID, event.FiredOn, event.TemplateID)
			if err != nil {
				log.WithError(err).WithFields(logrus.Fields{
					"org_id":           event.OrgID,
					"template_uuid":    event.TemplateUUID,
					"meta_template_id": event.MetaTemplateID,
				}).Error("error recording template last dispatch")
			}
		}
	}
}

func (w *templateLastDispatchWorker) enqueue(event templateLastDispatchEvent) {
	select {
	case w.events <- event:
	default:
		metrics.IncrementTemplateLastDispatchDropped()
		logrus.WithFields(logrus.Fields{
			"comp":             "template_last_dispatch",
			"org_id":           event.OrgID,
			"template_uuid":    event.TemplateUUID,
			"meta_template_id": event.MetaTemplateID,
		}).Warn("template last dispatch buffer full, dropping event")
	}
}

func (w *templateLastDispatchWorker) stop() {
	close(w.stopChan)
	w.wg.Wait()
}

// RecordTemplateLastDispatch upserts the last dispatch timestamp for a WhatsApp template.
func RecordTemplateLastDispatch(ctx context.Context, db *sqlx.DB, orgID int64, templateUUID, name, metaTemplateID string, firedOn time.Time, templateID *int) error {
	ctx, cancel := context.WithTimeout(ctx, templateLastDispatchDBTimeout)
	defer cancel()

	start := time.Now()
	defer func() {
		metrics.ObserveTemplateLastDispatchLatency(float64(time.Since(start).Milliseconds()))
	}()

	var templateIDArg interface{}
	if templateID != nil {
		templateIDArg = *templateID
	}

	_, err := db.ExecContext(ctx, upsertTemplateLastDispatchSQL,
		orgID, templateIDArg, templateUUID, name, metaTemplateID, firedOn)
	if err != nil {
		metrics.IncrementTemplateLastDispatchErrors()
		return err
	}

	metrics.IncrementTemplateLastDispatchUpsert()
	return nil
}

func orgIDFromMsg(msg courier.Msg) (OrgID, bool) {
	if dbMsg, ok := msg.(*DBMsg); ok && dbMsg.OrgID_ != NilOrgID {
		return dbMsg.OrgID_, true
	}

	if dbChannel, ok := msg.Channel().(*DBChannel); ok && dbChannel.OrgID_ != NilOrgID {
		return dbChannel.OrgID_, true
	}

	return NilOrgID, false
}

// QueueTemplateLastDispatch enqueues a template last dispatch record for async persistence.
func (b *backend) QueueTemplateLastDispatch(_ context.Context, msg courier.Msg, data courier.TemplateLastDispatchData, firedOn time.Time) {
	if b.templateLastDispatchWorker == nil {
		return
	}

	orgID, ok := orgIDFromMsg(msg)
	if !ok {
		return
	}

	b.templateLastDispatchWorker.enqueue(templateLastDispatchEvent{
		OrgID:          int64(orgID),
		TemplateUUID:   data.TemplateUUID,
		Name:           data.Name,
		MetaTemplateID: data.MetaTemplateID,
		FiredOn:        firedOn,
	})
}
