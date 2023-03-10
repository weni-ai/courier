package rapidpro

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/null"
)

type RunID int64

type FlowID null.Int

type RunStatus string

const (
	RunStatusActive      = "A"
	RunStatusWaiting     = "W"
	RunStatusCompleted   = "C"
	RunStatusExpired     = "X"
	RunStatusInterrupted = "I"
	RunStatusFailed      = "F"
)

type Run struct {
	ID              RunID
	UUID            uuids.UUID
	Status          RunStatus
	IsActive        bool
	CreatedOn       time.Time
	ModifiedOn      time.Time
	ExitedOn        *time.Time
	ExitType        *time.Time
	ExpiresOn       *time.Time
	Responded       bool
	Results         string
	Path            string
	Events          string
	CurrentNodeUUID null.String
	ContactID       ContactID
	FlowID          FlowID
	OrgID           OrgID
	SessionID       SessionID
	ConnectionID    *null.Int
}

type Event struct {
	Type      string     `json:"type,omitempty"`
	StepUUID  uuids.UUID `json:"step_uuid,omitempty"`
	CreatedOn *time.Time `json:"created_on,omitempty"`
	Msg       EventMsg   `json:"msg,omitempty"`
}

type EventMsg struct {
	ID      null.Int   `json:"id,omitempty"`
	URN     string     `json:"urn,omitempty"`
	Text    string     `json:"text,omitempty"`
	UUID    uuids.UUID `json:"uuid,omitempty"`
	Channel struct {
		Name string     `json:"name,omitempty"`
		UUID uuids.UUID `json:"uuid,omitempty"`
	} `json:"channel,omitempty"`
}

const selectFlowRunEventsByMsgUUID = `
SELECT 
 flows_flowrun.event
FROM
 flows_flowrun 
WHERE
 flows_flowrun.events @@ '$[*].msg.uuid like_regex "$2"';
`

type FlowRun struct {
	Events string `db:"events"`
}

func GetRunEventsByMsgUUIDFromDB(ctx context.Context, db *sqlx.DB, uuid string) ([]Event, error) {
	run := &FlowRun{}
	err := db.GetContext(ctx, run, selectFlowRunEventsByMsgUUID, uuid)

	if err == sql.ErrNoRows {
		return nil, errors.New("run not found")
	}

	if err != nil {
		return nil, err
	}

	events := []Event{}
	err = json.Unmarshal([]byte(run.Events), events)
	if err != nil {
		return nil, errors.New("failed to unmarshal events")
	}

	return events, nil
}

func GetRunEventsJSONByMsgUUIDFromDB(ctx context.Context, db *sqlx.DB, uuid string) (string, error) {
	run := &FlowRun{}
	err := db.GetContext(ctx, run, selectFlowRunEventsByMsgUUID, uuid)

	if err == sql.ErrNoRows {
		return "", errors.New("run not found")
	}

	if err != nil {
		return "", err
	}

	return run.Events, nil
}
