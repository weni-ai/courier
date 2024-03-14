package billing

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
)

func TestBillingClient(t *testing.T) {

	conn, err := NewRMQConn("amqp://localhost:5672/")
	assert.NoError(t, err)
	billingClient, err := NewRMQBillingClient(conn)
	assert.NoError(t, err)

	msgUUID, _ := uuid.NewV4()

	msg := NewMessage(
		"02a6abf4-2145-4a2d-bf71-62d4039a2586",
		"64a75af3-7e8d-41a5-8ef8-c273056c4fca",
		msgUUID.String(),
		time.Now().Format(time.RFC3339),
	)

	err = billingClient.Send(*msg)
	assert.NoError(t, err)

	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(errors.Wrap(err, "failed to declare a channel for consumer"))
	}
	msgs, err := ch.Consume(
		QUEUE_NAME,
		"",
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		t.Fatal(errors.Wrap(err, "failed to declare consumer"))
	}

	wg := &sync.WaitGroup{}
	wg.Add(1)

	var cmsg Message

	go func() {
		for d := range msgs {
			err := json.Unmarshal(d.Body, &cmsg)
			if err != nil {
				t.Error(errors.Wrap(err, "failed to unmarshal"))
			}

			wg.Done()
		}
	}()

	wg.Wait()
	assert.Equal(t, cmsg.MessageUUID, msg.MessageUUID)
}
