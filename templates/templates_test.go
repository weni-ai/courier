package templates

import (
	"encoding/json"
	"log"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/pkg/errors"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

const (
	templatesTestExchangeName = "test-templates-exchange"
	templatesTestQueueName    = "test-templates-queue"
)

func initializeRMQ(ch *amqp.Channel) {
	err := ch.ExchangeDeclare(
		templatesTestExchangeName,
		"topic",
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		log.Fatal(err)
	}

	_, err = ch.QueueDeclare(
		templatesTestQueueName,
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		log.Fatal(errors.Wrap(err, "failed to declare a queue for templates publisher"))
	}

	err = ch.QueueBind(
		templatesTestQueueName,
		"#",
		templatesTestExchangeName,
		false,
		nil,
	)
	if err != nil {
		log.Fatal(err)
	}
}

func TestInitialization(t *testing.T) {
	connURL := "amqp://" + envOr("RABBITMQ_HOST", "localhost") + ":5672/"
	conn, err := amqp.Dial(connURL)
	if err != nil {
		log.Fatal(err)
	}
	ch, err := conn.Channel()
	if err != nil {
		log.Fatal(err)
	}

	initializeRMQ(ch)

	defer ch.QueueDelete(templatesTestQueueName, false, false, false)
	defer ch.ExchangeDelete(templatesTestExchangeName, false, false)
	defer ch.Close()
	defer conn.Close()
}

func TestTemplateResilientClient(t *testing.T) {
	connURL := "amqp://" + envOr("RABBITMQ_HOST", "localhost") + ":5672/"
	conn, err := amqp.Dial(connURL)
	assert.NoError(t, err)
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(errors.Wrap(err, "failed to declare a channel for consumer"))
	}
	initializeRMQ(ch)
	defer ch.QueueDelete(templatesTestQueueName, false, false, false)
	defer ch.ExchangeDelete(templatesTestExchangeName, false, false)
	defer ch.Close()
	defer conn.Close()

	// Criar uma mensagem de template para teste
	msg := NewTemplateMessage(
		"whatsapp:123456789",
		"02a6abf4-2145-4a2d-bf71-62d4039a2586",
		"64a75af3-7e8d-41a5-8ef8-c273056c4fca",
		"msg-123456",
		time.Now().Format(time.RFC3339),
		"O",
		"WAC",
		"Olá {{1}}, sua conta está {{2}}.",
		"welcome_template",
		"template-uuid-12345",
		"pt_BR",
		"namespace-12345",
		[]string{"João", "ativa"},
	)

	templateClient, err := NewRMQTemplateClient(connURL, 3, 1000, templatesTestExchangeName)
	time.Sleep(1 * time.Second)
	assert.NoError(t, err)
	err = templateClient.SendTemplateData(msg)
	assert.NoError(t, err)

	msgs, err := ch.Consume(
		templatesTestQueueName,
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

	var receivedMsg TemplateMessage

	go func() {
		for d := range msgs {
			err := json.Unmarshal(d.Body, &receivedMsg)
			if err != nil {
				t.Error(errors.Wrap(err, "failed to unmarshal"))
			}

			wg.Done()
		}
	}()

	wg.Wait()
	assert.Equal(t, msg.MessageID, receivedMsg.MessageID)
	assert.Equal(t, msg.TemplateName, receivedMsg.TemplateName)
	assert.Equal(t, msg.TemplateUUID, receivedMsg.TemplateUUID)
	assert.Equal(t, msg.TemplateLanguage, receivedMsg.TemplateLanguage)
	assert.ElementsMatch(t, msg.TemplateVariables, receivedMsg.TemplateVariables)
}

func TestTemplateResilientClientSendStatus(t *testing.T) {
	connURL := "amqp://" + envOr("RABBITMQ_HOST", "localhost") + ":5672/"
	conn, err := amqp.Dial(connURL)
	assert.NoError(t, err)
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(errors.Wrap(err, "failed to declare a channel for consumer"))
	}
	initializeRMQ(ch)
	defer ch.QueueDelete(templatesTestQueueName, false, false, false)
	defer ch.ExchangeDelete(templatesTestExchangeName, false, false)
	defer ch.Close()
	defer conn.Close()

	// Criar uma mensagem de status de template para teste
	msg := NewTemplateStatusMessage(
		"whatsapp:123456789",
		"64a75af3-7e8d-41a5-8ef8-c273056c4fca",
		"msg-123456",
		"delivered",
		"marketing",
	)

	templateClient, err := NewRMQTemplateClient(connURL, 3, 1000, templatesTestExchangeName)
	time.Sleep(1 * time.Second)
	assert.NoError(t, err)
	err = templateClient.SendTemplateStatus(msg)
	assert.NoError(t, err)

	msgs, err := ch.Consume(
		templatesTestQueueName,
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

	var receivedMsg TemplateMessage

	go func() {
		for d := range msgs {
			err := json.Unmarshal(d.Body, &receivedMsg)
			if err != nil {
				t.Error(errors.Wrap(err, "failed to unmarshal"))
			}

			wg.Done()
		}
	}()

	wg.Wait()
	assert.Equal(t, msg.MessageID, receivedMsg.MessageID)
	assert.Equal(t, msg.Status, receivedMsg.Status)
}

func TestTemplateResilientClientSendAsync(t *testing.T) {
	connURL := "amqp://" + envOr("RABBITMQ_HOST", "localhost") + ":5672/"
	conn, err := amqp.Dial(connURL)
	assert.NoError(t, err)
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(errors.Wrap(err, "failed to declare a channel for consumer"))
	}
	initializeRMQ(ch)
	defer ch.QueueDelete(templatesTestQueueName, false, false, false)
	defer ch.ExchangeDelete(templatesTestExchangeName, false, false)
	defer ch.Close()
	defer conn.Close()

	// Criar uma mensagem de template para teste
	msg := NewTemplateMessage(
		"whatsapp:123456789",
		"02a6abf4-2145-4a2d-bf71-62d4039a2586",
		"64a75af3-7e8d-41a5-8ef8-c273056c4fca",
		"msg-123456",
		time.Now().Format(time.RFC3339),
		"O",
		"WAC",
		"Olá {{1}}, sua conta está {{2}}.",
		"welcome_template",
		"template-uuid-12345",
		"pt_BR",
		"namespace-12345",
		[]string{"João", "ativa"},
	)

	templateClient, err := NewRMQTemplateClient(connURL, 3, 1000, templatesTestExchangeName)
	time.Sleep(1 * time.Second)
	assert.NoError(t, err)

	var preExecuted bool
	var postExecuted bool

	templateClient.SendAsync(msg, RoutingKeySend, func() {
		preExecuted = true
	}, func() {
		postExecuted = true
	})

	msgs, err := ch.Consume(
		templatesTestQueueName,
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

	var receivedMsg TemplateMessage

	go func() {
		for d := range msgs {
			err := json.Unmarshal(d.Body, &receivedMsg)
			if err != nil {
				t.Error(errors.Wrap(err, "failed to unmarshal"))
			}

			wg.Done()
		}
	}()

	wg.Wait()
	time.Sleep(100 * time.Millisecond) // Give some time for post function to execute

	assert.Equal(t, msg.MessageID, receivedMsg.MessageID)
	assert.True(t, preExecuted, "Pre-function should have executed")
	assert.True(t, postExecuted, "Post-function should have executed")
}

func TestTemplateResilientClientSendAsyncWithPanic(t *testing.T) {
	connURL := "amqp://" + envOr("RABBITMQ_HOST", "localhost") + ":5672/"
	conn, err := amqp.Dial(connURL)
	assert.NoError(t, err)
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(errors.Wrap(err, "failed to declare a channel for consumer"))
	}
	initializeRMQ(ch)
	defer ch.QueueDelete(templatesTestQueueName, false, false, false)
	defer ch.ExchangeDelete(templatesTestExchangeName, false, false)
	defer ch.Close()
	defer conn.Close()

	// Criar uma mensagem de template para teste
	msg := NewTemplateMessage(
		"whatsapp:123456789",
		"02a6abf4-2145-4a2d-bf71-62d4039a2586",
		"64a75af3-7e8d-41a5-8ef8-c273056c4fca",
		"msg-123456",
		time.Now().Format(time.RFC3339),
		"O",
		"WAC",
		"Olá {{1}}, sua conta está {{2}}.",
		"welcome_template",
		"template-uuid-12345",
		"pt_BR",
		"namespace-12345",
		[]string{"João", "ativa"},
	)

	templateClient, err := NewRMQTemplateClient(connURL, 3, 1000, templatesTestExchangeName)
	time.Sleep(1 * time.Second)
	assert.NoError(t, err)

	// Função post vai causar um panic, mas a mensagem deve ser enviada mesmo assim
	templateClient.SendAsync(msg, RoutingKeySend, nil, func() { panic("test panic") })

	msgs, err := ch.Consume(
		templatesTestQueueName,
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

	var receivedMsg TemplateMessage

	go func() {
		for d := range msgs {
			err := json.Unmarshal(d.Body, &receivedMsg)
			if err != nil {
				t.Error(errors.Wrap(err, "failed to unmarshal"))
			}

			wg.Done()
		}
	}()

	wg.Wait()

	assert.Equal(t, msg.MessageID, receivedMsg.MessageID)
	// O teste deve passar mesmo com o panic no callback post
}
