package billing

import (
	"context"
	"encoding/json"
	"time"

	"github.com/pkg/errors"
	amqp "github.com/rabbitmq/amqp091-go"
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
func NewMessage(contactUUID, channelUUID, messageID, messageDate string) *Message {
	return &Message{
		ContactUUID: contactUUID,
		ChannelUUID: channelUUID,
		MessageDate: messageDate,
		MessageID:   messageID,
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
