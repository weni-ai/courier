package rapidpro

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gofrs/uuid"
	"github.com/sirupsen/logrus"
)

type protocolResolveResponse struct {
	ProtocolID    int64  `json:"protocol_id"`
	Created       bool   `json:"created"`
	PredecessorID *int64 `json:"predecessor_id"`
}

// bindInboundProtocol asks mailroom which protocol owns this message.
// When mailroom cannot answer, Courier opens one protocol itself and still stores the message.
func bindInboundProtocol(ctx context.Context, b *backend, m *DBMsg) error {
	protocolID, err := resolveProtocol(ctx, b, m)
	if err != nil {
		protocolID, err = insertFailSafeProtocol(ctx, b, m)
		if err != nil {
			return err
		}
	}
	m.ProtocolID_ = &protocolID
	logrus.WithFields(logrus.Fields{
		"org_id":      m.OrgID_,
		"channel_id":  m.ChannelID_,
		"protocol_id": protocolID,
	}).Info("inbound message bound to protocol")
	return nil
}

func resolveProtocol(ctx context.Context, b *backend, m *DBMsg) (int64, error) {
	if b.config.MailroomURL == "" {
		return 0, fmt.Errorf("mailroom url is not configured")
	}
	var projectUUID string
	if err := b.db.GetContext(ctx, &projectUUID, `SELECT proj_uuid FROM orgs_org WHERE id = $1`, m.OrgID_); err != nil {
		return 0, err
	}
	body, err := json.Marshal(map[string]interface{}{
		"project_id":  projectUUID,
		"urn_id":      m.ContactURNID_,
		"contact_id":  m.ContactID_,
		"channel_id":  m.ChannelID_,
		"external_id": m.ExternalID(),
	})
	if err != nil {
		return 0, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, b.config.MailroomURL+"/mr/protocol/resolve", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if b.config.MailroomAuthToken != "" {
		req.Header.Set("Authorization", "Token "+b.config.MailroomAuthToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("protocol resolve returned %d", resp.StatusCode)
	}
	var decoded protocolResolveResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return 0, err
	}
	if decoded.ProtocolID == 0 {
		return 0, fmt.Errorf("protocol resolve returned an empty id")
	}
	return decoded.ProtocolID, nil
}

func insertFailSafeProtocol(ctx context.Context, b *backend, m *DBMsg) (int64, error) {
	if m.ExternalID() != "" {
		var existing int64
		err := b.db.GetContext(ctx, &existing, `
SELECT id FROM msgs_protocol
WHERE org_id = $1 AND urn_id = $2 AND external_id = $3`, m.OrgID_, m.ContactURNID_, m.ExternalID())
		if err == nil {
			return existing, nil
		}
		if err != sql.ErrNoRows {
			return 0, err
		}
	}
	protocolUUID, err := uuid.NewV4()
	if err != nil {
		return 0, err
	}
	var external interface{}
	if m.ExternalID() != "" {
		external = m.ExternalID()
	}
	var id int64
	err = b.db.GetContext(ctx, &id, `
INSERT INTO msgs_protocol (
	uuid, org_id, contact_id, urn_id, state, opened_on, external_id,
	idle_accumulated, timer_paused, timer_kind, timer_deadline
) VALUES (
	$1, $2, $3, $4, 'open', NOW(), $5,
	0, false, 'ai', NOW() + interval '1 hour'
) RETURNING id`, protocolUUID.String(), m.OrgID_, m.ContactID_, m.ContactURNID_, external)
	if err != nil {
		return 0, err
	}
	return id, nil
}
