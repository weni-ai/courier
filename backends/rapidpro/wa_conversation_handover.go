package rapidpro

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/null"
	"github.com/sirupsen/logrus"
)

const upsertWAConversationHandoverSQL = `
INSERT INTO wa_conversation_handover
	(org_id, channel_id, contact_id, contact_urn, context_type, context_text, context_payload,
	 previous_owner_app_id, previous_owner_app_role, previous_owner_business_id, handover_metadata,
	 occurred_on, created_on)
VALUES
	(:org_id, :channel_id, :contact_id, :contact_urn, :context_type, :context_text, :context_payload,
	 :previous_owner_app_id, :previous_owner_app_role, :previous_owner_business_id, :handover_metadata,
	 :occurred_on, NOW())
ON CONFLICT (channel_id, contact_id) WHERE consumed_on IS NULL DO UPDATE SET
	contact_urn = EXCLUDED.contact_urn,
	context_type = EXCLUDED.context_type,
	context_text = EXCLUDED.context_text,
	context_payload = EXCLUDED.context_payload,
	previous_owner_app_id = EXCLUDED.previous_owner_app_id,
	previous_owner_app_role = EXCLUDED.previous_owner_app_role,
	previous_owner_business_id = EXCLUDED.previous_owner_business_id,
	handover_metadata = EXCLUDED.handover_metadata,
	occurred_on = EXCLUDED.occurred_on,
	created_on = NOW()
`

type DBWAConversationHandover struct {
	OrgID                   OrgID       `db:"org_id"`
	ChannelID               courier.ChannelID `db:"channel_id"`
	ContactID               ContactID         `db:"contact_id"`
	ContactURN              string      `db:"contact_urn"`
	ContextType             string      `db:"context_type"`
	ContextText             string      `db:"context_text"`
	ContextPayload          null.String `db:"context_payload"`
	PreviousOwnerAppID      null.String `db:"previous_owner_app_id"`
	PreviousOwnerAppRole    null.String `db:"previous_owner_app_role"`
	PreviousOwnerBusinessID null.String `db:"previous_owner_business_id"`
	HandoverMetadata        null.String `db:"handover_metadata"`
	OccurredOn              time.Time   `db:"occurred_on"`
}

func writeWAConversationHandover(ctx context.Context, b *backend, event courier.WAConversationHandoverEvent) error {
	err := writeWAConversationHandoverToDB(ctx, b, event)
	if err != nil {
		logrus.WithError(err).WithField("contact_urn", event.ContactURN.Identity()).Error("error writing wa conversation handover to db")
		err = courier.WriteToSpool(b.config.SpoolDir, "wa_conversation_handover", event)
	}

	return err
}

func writeWAConversationHandoverToDB(ctx context.Context, b *backend, event courier.WAConversationHandoverEvent) error {
	channel, err := b.GetChannel(ctx, courier.AnyChannelType, event.ChannelUUID)
	if err != nil {
		return err
	}
	dbChannel := channel.(*DBChannel)

	contact, err := contactForURN(ctx, b, dbChannel.OrgID_, dbChannel, event.ContactURN, "", event.ContactName)
	if err != nil {
		return err
	}

	row := &DBWAConversationHandover{
		OrgID:        dbChannel.OrgID_,
		ChannelID:    dbChannel.ID_,
		ContactID:    contact.ID_,
		ContactURN:   event.ContactURN.String(),
		ContextType:  event.ContextType,
		ContextText:  event.ContextText,
		OccurredOn:   event.OccurredOn,
		ContextPayload: nullStringFromValue(string(event.ContextPayload)),
	}

	if event.PreviousOwnerAppID != "" {
		row.PreviousOwnerAppID = null.String(event.PreviousOwnerAppID)
	}
	if event.PreviousOwnerAppRole != "" {
		row.PreviousOwnerAppRole = null.String(event.PreviousOwnerAppRole)
	}
	if event.PreviousOwnerBusinessID != "" {
		row.PreviousOwnerBusinessID = null.String(event.PreviousOwnerBusinessID)
	}
	if event.HandoverMetadata != "" {
		row.HandoverMetadata = null.String(event.HandoverMetadata)
	}

	_, err = b.db.NamedExecContext(ctx, upsertWAConversationHandoverSQL, row)
	return err
}

func (b *backend) flushWAConversationHandoverFile(filename string, contents []byte) error {
	event := courier.WAConversationHandoverEvent{}
	err := json.Unmarshal(contents, &event)
	if err != nil {
		log.Printf("ERROR unmarshalling spool file '%s', renaming: %s\n", filename, err)
		os.Rename(filename, fmt.Sprintf("%s.error", filename))
		return nil
	}

	if event.ChannelUUID == courier.NilChannelUUID {
		return fmt.Errorf("missing channel uuid in spooled wa conversation handover event")
	}

	return writeWAConversationHandoverToDB(context.Background(), b, event)
}
