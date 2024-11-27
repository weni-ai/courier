package billing

import (
	"encoding/json"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/pkg/errors"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
)

const billingTestQueueName = "testqueue"

func TestInitialization(t *testing.T) {
	connURL := "amqp://localhost:5672/"
	conn, err := amqp.Dial(connURL)
	if err != nil {
		log.Fatal(err)
	}
	ch, err := conn.Channel()
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	defer ch.Close()
	defer ch.QueueDelete(billingTestQueueName, false, false, false)
}

func TestBillingResilientClient(t *testing.T) {
	connURL := "amqp://localhost:5672/"
	conn, err := amqp.Dial(connURL)
	assert.NoError(t, err)
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(errors.Wrap(err, "failed to declare a channel for consumer"))
	}
	defer ch.Close()
	defer ch.QueueDelete(billingTestQueueName, false, false, false)

	msgUUID, _ := uuid.NewV4()
	msg := NewMessage(
		"telegram:123456789",
		"02a6abf4-2145-4a2d-bf71-62d4039a2586",
		"64a75af3-7e8d-41a5-8ef8-c273056c4fca",
		time.Now().Format(time.RFC3339),
		msgUUID.String(),
		"O",
		"TG",
		"hello",
		nil,
		nil,
	)

	billingClient, err := NewRMQBillingResilientClient(connURL, 3, 1000, billingTestQueueName)
	time.Sleep(1 * time.Second)
	assert.NoError(t, err)
	err = billingClient.Send(msg)
	assert.NoError(t, err)

	msgs, err := ch.Consume(
		billingTestQueueName,
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
	assert.Equal(t, cmsg.MessageID, msg.MessageID)
}

func TestBillingResilientClientSendAsync(t *testing.T) {
	connURL := "amqp://localhost:5672/"
	conn, err := amqp.Dial(connURL)
	assert.NoError(t, err)
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(errors.Wrap(err, "failed to declare a channel for consumer"))
	}
	defer ch.Close()
	defer ch.QueueDelete(billingTestQueueName, false, false, false)

	msgUUID, _ := uuid.NewV4()
	msg := NewMessage(
		"telegram:123456789",
		"02a6abf4-2145-4a2d-bf71-62d4039a2586",
		"64a75af3-7e8d-41a5-8ef8-c273056c4fca",
		time.Now().Format(time.RFC3339),
		msgUUID.String(),
		"O",
		"TG",
		"hello",
		nil,
		nil,
	)

	billingClient, err := NewRMQBillingResilientClient(connURL, 3, 1000, billingTestQueueName)
	time.Sleep(1 * time.Second)
	assert.NoError(t, err)
	billingClient.SendAsync(msg, nil, nil)

	assert.NoError(t, err)
	msgs, err := ch.Consume(
		billingTestQueueName,
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
	assert.Equal(t, cmsg.MessageID, msg.MessageID)
}

func TestBillingResilientClientSendAsyncWithPanic(t *testing.T) {
	connURL := "amqp://localhost:5672/"
	conn, err := amqp.Dial(connURL)
	assert.NoError(t, err)
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(errors.Wrap(err, "failed to declare a channel for consumer"))
	}
	defer ch.Close()
	defer ch.QueueDelete(billingTestQueueName, false, false, false)

	msgUUID, _ := uuid.NewV4()
	msg := NewMessage(
		"telegram:123456789",
		"02a6abf4-2145-4a2d-bf71-62d4039a2586",
		"64a75af3-7e8d-41a5-8ef8-c273056c4fca",
		time.Now().Format(time.RFC3339),
		msgUUID.String(),
		"O",
		"TG",
		"hello",
		nil,
		nil,
	)

	billingClient, err := NewRMQBillingResilientClient(connURL, 3, 1000, billingTestQueueName)
	time.Sleep(1 * time.Second)
	assert.NoError(t, err)
	time.Sleep(1 * time.Second)
	billingClient.SendAsync(msg, nil, func() { panic("test panic") })

	assert.NoError(t, err)
	msgs, err := ch.Consume(
		billingTestQueueName,
		"",
		false,
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
	assert.Equal(t, cmsg.MessageID, msg.MessageID)
}
