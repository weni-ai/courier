package billing

import (
	"context"
	"encoding/json"
	"time"

	"github.com/furdarius/rabbitroutine"
	"github.com/pkg/errors"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/sirupsen/logrus"
)

const QUEUE_NAME = "billing_message"

// Message represents a object that is sent to the billing service
//
//	{
//		  "contact_uuid": "69625dca-7922-477c-97c6-9dae8ffff46d",
//		  "channel_uuid": "9d24bce2-145f-4e65-b9ed-72ef19ee81e0",
//		  "message_id": "54398",
//		  "message_date": "2024-03-08T16:08:19-03:00"
//	 }
type Message struct {
	ContactURN   string   `json:"contact_urn,omitempty"`
	ContactUUID  string   `json:"contact_uuid,omitempty"`
	ChannelUUID  string   `json:"channel_uuid,omitempty"`
	MessageID    string   `json:"message_id,omitempty"`
	MessageDate  string   `json:"message_date,omitempty"`
	Direction    string   `json:"direction,omitempty"`
	ChannelType  string   `json:"channel_type,omitempty"`
	Text         string   `json:"text,omitempty"`
	Attachments  []string `json:"attachments,omitempty"`
	QuickReplies []string `json:"quick_replies,omitempty"`
}

// Create a new message
func NewMessage(contactURN, contactUUID, channelUUID, messageID, messageDate, direction, channelType, text string, attachments, quickreplies []string) *Message {
	return &Message{
		ContactURN:   contactURN,
		ContactUUID:  contactUUID,
		ChannelUUID:  channelUUID,
		MessageID:    messageID,
		MessageDate:  messageDate,
		Direction:    direction,
		ChannelType:  channelType,
		Text:         text,
		Attachments:  attachments,
		QuickReplies: quickreplies,
	}
}

// Client represents a client interface for billing service
type Client interface {
	Send(msg Message) error
}

// rabbitmqClient represents struct that implements billing service client interface
type rabbitmqClient struct {
	channel *amqp.Channel
	queue   *amqp.Queue
}

// NewRMQConn creates a new connection to rabbitmq for the given url
func NewRMQConn(url string) (*amqp.Connection, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// NewRMQBillingClient creates a new billing service client implementation using RabbitMQ
func NewRMQBillingClient(conn *amqp.Connection) (Client, error) {
	ch, err := conn.Channel()
	if err != nil {
		return nil, errors.Wrap(err, "failed to open a channel to rabbitmq")
	}
	q, err := ch.QueueDeclare(
		QUEUE_NAME,
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to declare a queue for billing publisher")
	}
	return &rabbitmqClient{
		queue:   &q,
		channel: ch,
	}, nil
}

// Send sends a message to Billing service
func (c *rabbitmqClient) Send(msg Message) error {
	msgMarshalled, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	err = c.channel.PublishWithContext(
		ctx,
		"",
		c.queue.Name,
		false,
		false,
		amqp.Publishing{
			ContentType: "application/json",
			Body:        msgMarshalled,
		},
	)
	if err != nil {
		return err
	}
	return nil
}

// rabbitmqRetryClient represents struct that implements billing service client interface
type rabbitmqRetryClient struct {
	publisher rabbitroutine.Publisher
	conn      *rabbitroutine.Connector
}

// NewRMQBillingResilientClient creates a new billing service client implementation using RabbitMQ with publish retry and reconnect features
func NewRMQBillingResilientClient(url string, retryAttempts int, retryDelay int) (Client, error) {
	ctx := context.Background()

	conn := rabbitroutine.NewConnector(rabbitroutine.Config{
		ReconnectAttempts: 1000,
		Wait:              2 * time.Second,
	})

	conn.AddRetriedListener(func(r rabbitroutine.Retried) {
		logrus.Infof("try to connect to RabbitMQ: attempt=%d, error=\"%v\"",
			r.ReconnectAttempt, r.Error)
	})

	conn.AddDialedListener(func(_ rabbitroutine.Dialed) {
		logrus.Info("RabbitMQ connection successfully established")
	})

	conn.AddAMQPNotifiedListener(func(n rabbitroutine.AMQPNotified) {
		logrus.Errorf("RabbitMQ error received: %v", n.Error)
	})

	pool := rabbitroutine.NewPool(conn)
	ensurePub := rabbitroutine.NewEnsurePublisher(pool)
	pub := rabbitroutine.NewRetryPublisher(
		ensurePub,
		rabbitroutine.PublishMaxAttemptsSetup(uint(retryAttempts)),
		rabbitroutine.PublishDelaySetup(
			rabbitroutine.LinearDelay(time.Duration(retryDelay)*time.Millisecond),
		),
	)

	go func() {
		err := conn.Dial(ctx, url)
		if err != nil {
			logrus.Error("failed to establish RabbitMQ connection")
		}
	}()

	return &rabbitmqRetryClient{
		publisher: pub,
		conn:      conn,
	}, nil
}

func (c *rabbitmqRetryClient) Send(msg Message) error {
	msgMarshalled, _ := json.Marshal(msg)
	ctx := context.Background()
	err := c.publisher.Publish(
		ctx,
		"",
		QUEUE_NAME,
		amqp.Publishing{
			ContentType: "application/json",
			Body:        msgMarshalled,
		},
	)
	if err != nil {
		return errors.Wrap(err, "failed to publish msg to billing")
	}
	return nil
}
