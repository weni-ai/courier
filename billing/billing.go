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

const (
	RoutingKeyCreate = "create"
	RoutingKeyUpdate = "status-update"
	RoutingKeyWAC    = "whatsapp-cloud-token"
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
	Name         string   `json:"name,omitempty"`
	ChannelUUID  string   `json:"channel_uuid,omitempty"`
	MessageID    string   `json:"message_id,omitempty"`
	MessageDate  string   `json:"message_date,omitempty"`
	Direction    string   `json:"direction,omitempty"`
	ChannelType  string   `json:"channel_type,omitempty"`
	Text         string   `json:"text,omitempty"`
	Attachments  []string `json:"attachments,omitempty"`
	QuickReplies []string `json:"quick_replies,omitempty"`
	FromTicketer bool     `json:"from_ticketer"`
	ChatsUUID    string   `json:"chats_uuid,omitempty"`
	Status       string   `json:"status,omitempty"`
	TemplateUUID string   `json:"template_uuid,omitempty"`
}

// Create a new message
func NewMessage(contactURN, contactUUID, name, channelUUID, messageID, messageDate, direction, channelType, text string, attachments, quickreplies []string, fromTicketer bool, chatsUUID string, status string) Message {
	return Message{
		ContactURN:   contactURN,
		ContactUUID:  contactUUID,
		Name:         name,
		ChannelUUID:  channelUUID,
		MessageID:    messageID,
		MessageDate:  messageDate,
		Direction:    direction,
		ChannelType:  channelType,
		Text:         text,
		Attachments:  attachments,
		QuickReplies: quickreplies,
		FromTicketer: fromTicketer,
		ChatsUUID:    chatsUUID,
		Status:       status,
	}
}

// Client represents a client interface for billing service
type Client interface {
	Send(msg Message, routingKey string) error
	SendAsync(msg Message, routingKey string, pre func(), post func())
}

// SkipRoutingKeysClient is an optional interface: if a Client implements it,
// MultiBillingClient will not send messages whose routing key is in SkipRoutingKeys() to that client.
type SkipRoutingKeysClient interface {
	Client
	SkipRoutingKeys() []string
}

// rabbitmqRetryClient represents struct that implements billing service client interface
type rabbitmqRetryClient struct {
	publisher    rabbitroutine.Publisher
	conn         *rabbitroutine.Connector
	exchangeName string
}

// NewRMQBillingResilientClient creates a new billing service client implementation using RabbitMQ with publish retry and reconnect features
func NewRMQBillingResilientClient(url string, retryAttempts int, retryDelay int, exchangeName string) (Client, error) {
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
		publisher:    pub,
		conn:         conn,
		exchangeName: exchangeName,
	}, nil
}

func (c *rabbitmqRetryClient) Send(msg Message, routingKey string) error {
	fmt.Println("--------------------------------")
	fmt.Println("Send")
	fmt.Println("Message ID:", msg.MessageID)
	fmt.Printf("MSG: %v\n", msg)
	fmt.Println("--------------------------------")
	msgMarshalled, _ := json.Marshal(msg)
	ctx := context.Background()
	err := c.publisher.Publish(
		ctx,
		c.exchangeName,
		routingKey,
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

func (c *rabbitmqRetryClient) SendAsync(msg Message, routingKey string, pre func(), post func()) {
	fmt.Println("--------------------------------")
	fmt.Println("Message ID:", msg.MessageID)
	fmt.Printf("MSG: %v\n", msg)
	fmt.Println("--------------------------------")
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logrus.Error(fmt.Sprintf("Recovering from: %v", r))
			}
		}()
		if pre != nil {
			pre()
		}
		err := c.Send(msg, routingKey)
		if err != nil {
			logrus.WithError(err).Error("fail to send msg to billing service")
		}
		if post != nil {
			post()
		}
	}()
}

// clientWithSkipRoutingKeys wraps a Client and implements SkipRoutingKeysClient.
// Used for backends that must not receive certain routing keys (e.g. AmazonMQ and RoutingKeyWAC).
type clientWithSkipRoutingKeys struct {
	client   Client
	skipKeys map[string]struct{}
}

// NewClientWithSkipRoutingKeys returns a Client that implements SkipRoutingKeysClient.
// When used inside MultiBillingClient, messages with routing key in skipKeys will not be sent to this client.
func NewClientWithSkipRoutingKeys(client Client, skipKeys ...string) Client {
	skip := make(map[string]struct{}, len(skipKeys))
	for _, k := range skipKeys {
		skip[k] = struct{}{}
	}
	return &clientWithSkipRoutingKeys{client: client, skipKeys: skip}
}

func (c *clientWithSkipRoutingKeys) Send(msg Message, routingKey string) error {
	return c.client.Send(msg, routingKey)
}

func (c *clientWithSkipRoutingKeys) SendAsync(msg Message, routingKey string, pre func(), post func()) {
	c.client.SendAsync(msg, routingKey, pre, post)
}

func (c *clientWithSkipRoutingKeys) SkipRoutingKeys() []string {
	keys := make([]string, 0, len(c.skipKeys))
	for k := range c.skipKeys {
		keys = append(keys, k)
	}
	return keys
}

func skipRoutingKey(client Client, routingKey string) bool {
	skip, ok := client.(SkipRoutingKeysClient)
	if !ok {
		return false
	}
	for _, k := range skip.SkipRoutingKeys() {
		if k == routingKey {
			return true
		}
	}
	return false
}

// MultiBillingClient sends messages to multiple billing backends simultaneously.
// Each client can optionally implement SkipRoutingKeysClient to not receive certain routing keys.
type MultiBillingClient struct {
	clients []Client
}

// NewMultiBillingClient creates a new client that sends to multiple backends.
// Order does not matter: use NewClientWithSkipRoutingKeys for backends that must skip certain keys (e.g. AmazonMQ + RoutingKeyWAC).
func NewMultiBillingClient(clients ...Client) Client {
	return &MultiBillingClient{clients: clients}
}

func (m *MultiBillingClient) Send(msg Message, routingKey string) error {
	var lastErr error
	for _, client := range m.clients {
		if client == nil || skipRoutingKey(client, routingKey) {
			continue
		}
		if err := client.Send(msg, routingKey); err != nil {
			logrus.WithError(err).WithField("routing_key", routingKey).Error("failed to send to billing client")
			lastErr = err
		}
	}
	return lastErr
}

func (m *MultiBillingClient) SendAsync(msg Message, routingKey string, pre func(), post func()) {
	for _, client := range m.clients {
		if client == nil || skipRoutingKey(client, routingKey) {
			continue
		}
		client.SendAsync(msg, routingKey, pre, post)
	}
}
