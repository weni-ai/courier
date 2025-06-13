package templates

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
	RoutingKeySend   = "template-send"
	RoutingKeyStatus = "template-status"
)

// TemplateMessage representa os dados de um template enviado
type TemplateMessage struct {
	ContactURN        string   `json:"contact_urn,omitempty"`
	ContactUUID       string   `json:"contact_uuid,omitempty"`
	ChannelUUID       string   `json:"channel_uuid,omitempty"`
	MessageID         string   `json:"message_id,omitempty"`
	MessageDate       string   `json:"message_date,omitempty"`
	Direction         string   `json:"direction,omitempty"`
	ChannelType       string   `json:"channel_type,omitempty"`
	Text              string   `json:"text,omitempty"`
	TemplateName      string   `json:"template_name,omitempty"`
	TemplateUUID      string   `json:"template_uuid,omitempty"`
	TemplateLanguage  string   `json:"template_language,omitempty"`
	TemplateNamespace string   `json:"template_namespace,omitempty"`
	TemplateVariables []string `json:"template_variables,omitempty"`
	Status            string   `json:"status,omitempty"`
	TemplateType      string   `json:"template_type,omitempty"`
}

// NewTemplateMessage cria uma nova mensagem de template
func NewTemplateMessage(
	contactURN, contactUUID, channelUUID, messageID, messageDate,
	direction, channelType, text,
	templateName, templateUUID, templateLanguage, templateNamespace string,
	templateVariables []string) TemplateMessage {

	return TemplateMessage{
		ContactURN:        contactURN,
		ContactUUID:       contactUUID,
		ChannelUUID:       channelUUID,
		MessageID:         messageID,
		MessageDate:       messageDate,
		Direction:         direction,
		ChannelType:       channelType,
		Text:              text,
		TemplateName:      templateName,
		TemplateUUID:      templateUUID,
		TemplateLanguage:  templateLanguage,
		TemplateNamespace: templateNamespace,
		TemplateVariables: templateVariables,
	}
}

// NewTemplateStatusMessage cria uma nova mensagem de status de template
func NewTemplateStatusMessage(
	contactURN, channelUUID, messageID, status, templateType string) TemplateMessage {

	return TemplateMessage{
		ContactURN:   contactURN,
		ChannelUUID:  channelUUID,
		MessageID:    messageID,
		MessageDate:  time.Now().Format(time.RFC3339),
		Status:       status,
		TemplateType: templateType,
	}
}

// Client representa uma interface para o client do serviço de templates
type Client interface {
	SendTemplateData(msg TemplateMessage) error
	SendTemplateStatus(msg TemplateMessage) error
	SendAsync(msg TemplateMessage, routingKey string, pre func(), post func())
	Close() error
}

// rabbitmqClient representa um cliente que implementa a interface Client
type rabbitmqClient struct {
	publisher    rabbitroutine.Publisher
	conn         *rabbitroutine.Connector
	exchangeName string
}

type TemplateMetadata struct {
	Templating *MsgTemplating `json:"templating"`
}

type MsgTemplating struct {
	Template struct {
		Name string `json:"name" validate:"required"`
		UUID string `json:"uuid" validate:"required"`
	} `json:"template" validate:"required,dive"`
	Language  string   `json:"language" validate:"required"`
	Country   string   `json:"country"`
	Namespace string   `json:"namespace"`
	Variables []string `json:"variables"`
}

// NewRMQTemplateClient cria um novo cliente para o serviço de templates usando RabbitMQ
func NewRMQTemplateClient(url string, retryAttempts int, retryDelay int, exchangeName string) (Client, error) {
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

	return &rabbitmqClient{
		publisher:    pub,
		conn:         conn,
		exchangeName: exchangeName,
	}, nil
}

func (c *rabbitmqClient) send(msg TemplateMessage, routingKey string) error {
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
		return errors.Wrap(err, "failed to publish template message")
	}
	return nil
}

func (c *rabbitmqClient) SendTemplateData(msg TemplateMessage) error {
	return c.send(msg, RoutingKeySend)
}

func (c *rabbitmqClient) SendTemplateStatus(msg TemplateMessage) error {
	return c.send(msg, RoutingKeyStatus)
}

func (c *rabbitmqClient) SendAsync(msg TemplateMessage, routingKey string, pre func(), post func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logrus.Error(fmt.Sprintf("Recovering from: %v", r))
			}
		}()
		if pre != nil {
			pre()
		}
		err := c.send(msg, routingKey)
		if err != nil {
			logrus.WithError(err).Error("fail to send template message")
		}
		if post != nil {
			post()
		}
	}()
}

func (c *rabbitmqClient) Close() error {
	// Fechamos a conexão com RabbitMQ
	return nil // aqui você deve adicionar lógica para fechar a conexão apropriadamente
}
