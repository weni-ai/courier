package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/furdarius/rabbitroutine"
	"github.com/pkg/errors"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/sirupsen/logrus"
)

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
	Metadata     string   `json:"metadata"`
}

// Create a new message
func NewMessage(contactURN, contactUUID, channelUUID, messageID, messageDate, direction, channelType, text string, attachments, quickreplies []string, metadata string) Message {
	return Message{
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
		Metadata:     metadata,
	}
}

// Client represents a client interface for billing service
type Client interface {
	Send(msg Message) error
	SendAsync(msg Message, pre func(), post func())
}

// rabbitmqRetryClient represents struct that implements billing service client interface
type rabbitmqRetryClient struct {
	publisher rabbitroutine.Publisher
	conn      *rabbitroutine.Connector
	queueName string
}

// NewRMQBillingResilientClient creates a new billing service client implementation using RabbitMQ with publish retry and reconnect features
func NewRMQBillingResilientClient(url string, retryAttempts int, retryDelay int, queueName string) (Client, error) {
	cconn, err := amqp.Dial(url)
	if err != nil {
		return nil, err
	}
	defer cconn.Close()

	ch, err := cconn.Channel()
	if err != nil {
		return nil, errors.Wrap(err, "failed to open a channel to rabbitmq")
	}
	defer ch.Close()
	_, err = ch.QueueDeclare(
		queueName,
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to declare a queue for billing publisher")
	}

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
		err := conn.Dial(context.Background(), url)
		if err != nil {
			logrus.Error("failed to establish RabbitMQ connection")
		}
	}()

	return &rabbitmqRetryClient{
		publisher: pub,
		conn:      conn,
		queueName: queueName,
	}, nil
}

func (c *rabbitmqRetryClient) Send(msg Message) error {
	msgMarshalled, _ := json.Marshal(msg)
	ctx := context.Background()
	err := c.publisher.Publish(
		ctx,
		"",
		c.queueName,
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

func (c *rabbitmqRetryClient) SendAsync(msg Message, pre func(), post func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logrus.Error(fmt.Sprintf("Recovering from: %v", r))
			}
		}()
		if pre != nil {
			pre()
		}
		err := c.Send(msg)
		if err != nil {
			logrus.WithError(err).Error("fail to send msg to billing service")
		}
		if post != nil {
			post()
		}
	}()
}
