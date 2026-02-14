package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/evalphobia/logrus_sentry"
	_ "github.com/lib/pq"
	"github.com/nyaruka/courier"
	"github.com/sirupsen/logrus"
	_ "go.uber.org/automaxprocs"

	// load channel handler packages
	"github.com/nyaruka/courier/billing"
	_ "github.com/nyaruka/courier/handlers/africastalking"
	_ "github.com/nyaruka/courier/handlers/arabiacell"
	_ "github.com/nyaruka/courier/handlers/blackmyna"
	_ "github.com/nyaruka/courier/handlers/bongolive"
	_ "github.com/nyaruka/courier/handlers/burstsms"
	_ "github.com/nyaruka/courier/handlers/chikka"
	_ "github.com/nyaruka/courier/handlers/clickatell"
	_ "github.com/nyaruka/courier/handlers/clickmobile"
	_ "github.com/nyaruka/courier/handlers/clicksend"
	_ "github.com/nyaruka/courier/handlers/dart"
	_ "github.com/nyaruka/courier/handlers/discord"
	_ "github.com/nyaruka/courier/handlers/dmark"
	_ "github.com/nyaruka/courier/handlers/email"
	_ "github.com/nyaruka/courier/handlers/external"
	_ "github.com/nyaruka/courier/handlers/externalv2"
	_ "github.com/nyaruka/courier/handlers/facebook"
	_ "github.com/nyaruka/courier/handlers/facebookapp"
	_ "github.com/nyaruka/courier/handlers/firebase"
	_ "github.com/nyaruka/courier/handlers/freshchat"
	_ "github.com/nyaruka/courier/handlers/globe"
	_ "github.com/nyaruka/courier/handlers/highconnection"
	_ "github.com/nyaruka/courier/handlers/hormuud"
	_ "github.com/nyaruka/courier/handlers/hub9"
	_ "github.com/nyaruka/courier/handlers/i2sms"
	_ "github.com/nyaruka/courier/handlers/infobip"
	_ "github.com/nyaruka/courier/handlers/jasmin"
	_ "github.com/nyaruka/courier/handlers/jiochat"
	_ "github.com/nyaruka/courier/handlers/junebug"
	_ "github.com/nyaruka/courier/handlers/kaleyra"
	_ "github.com/nyaruka/courier/handlers/kannel"
	_ "github.com/nyaruka/courier/handlers/line"
	_ "github.com/nyaruka/courier/handlers/m3tech"
	_ "github.com/nyaruka/courier/handlers/macrokiosk"
	_ "github.com/nyaruka/courier/handlers/mblox"
	_ "github.com/nyaruka/courier/handlers/messangi"
	_ "github.com/nyaruka/courier/handlers/mtarget"
	_ "github.com/nyaruka/courier/handlers/nexmo"
	_ "github.com/nyaruka/courier/handlers/novo"
	_ "github.com/nyaruka/courier/handlers/playmobile"
	_ "github.com/nyaruka/courier/handlers/plivo"
	_ "github.com/nyaruka/courier/handlers/redrabbit"
	_ "github.com/nyaruka/courier/handlers/rocketchat"
	_ "github.com/nyaruka/courier/handlers/shaqodoon"
	_ "github.com/nyaruka/courier/handlers/slack"
	_ "github.com/nyaruka/courier/handlers/smscentral"
	_ "github.com/nyaruka/courier/handlers/start"
	_ "github.com/nyaruka/courier/handlers/teams"
	_ "github.com/nyaruka/courier/handlers/telegram"
	_ "github.com/nyaruka/courier/handlers/telesom"
	_ "github.com/nyaruka/courier/handlers/thinq"
	_ "github.com/nyaruka/courier/handlers/twiml"
	_ "github.com/nyaruka/courier/handlers/twitter"
	_ "github.com/nyaruka/courier/handlers/viber"
	_ "github.com/nyaruka/courier/handlers/vk"
	_ "github.com/nyaruka/courier/handlers/wavy"
	_ "github.com/nyaruka/courier/handlers/wechat"
	_ "github.com/nyaruka/courier/handlers/weniwebchat"
	_ "github.com/nyaruka/courier/handlers/whatsapp"
	_ "github.com/nyaruka/courier/handlers/yo"
	_ "github.com/nyaruka/courier/handlers/zenvia"
	_ "github.com/nyaruka/courier/handlers/zenviaold"
	"github.com/nyaruka/courier/templates"

	// load available backends
	_ "github.com/nyaruka/courier/backends/rapidpro"
)

var version = "Dev"

func main() {
	config := courier.LoadConfig("courier.toml")

	// if we have a custom version, use it
	if version != "Dev" {
		config.Version = version
	}

	// configure our logger
	logrus.SetOutput(os.Stdout)
	level, err := logrus.ParseLevel(config.LogLevel)
	if err != nil {
		logrus.Fatalf("Invalid log level '%s'", level)
	}
	logrus.SetLevel(level)

	// if we have a DSN entry, try to initialize it
	if config.SentryDSN != "" {
		hook, err := logrus_sentry.NewSentryHook(config.SentryDSN, []logrus.Level{logrus.PanicLevel, logrus.FatalLevel, logrus.ErrorLevel})
		hook.Timeout = 0
		hook.StacktraceConfiguration.Enable = true
		hook.StacktraceConfiguration.Skip = 4
		hook.StacktraceConfiguration.Context = 5
		if err != nil {
			logrus.Fatalf("Invalid sentry DSN: '%s': %s", config.SentryDSN, err)
		}
		logrus.StandardLogger().Hooks.Add(hook)
	}

	// load our backend
	backend, err := courier.NewBackend(config)
	if err != nil {
		logrus.Fatalf("Error creating backend: %s", err)
	}

	server := courier.NewServer(config, backend)
	err = server.Start()
	if err != nil {
		logrus.Fatalf("Error starting server: %s", err)
	}

	// Initialize billing clients
	var billingClients []billing.Client

	// RabbitMQ billing client (current)
	if config.EnableRabbitMQBilling && config.RabbitmqURL != "" {
		client, err := billing.NewRMQBillingResilientClient(
			config.RabbitmqURL,
			config.RabbitmqRetryPubAttempts,
			config.RabbitmqRetryPubDelay,
			config.BillingExchangeName,
		)
		if err != nil {
			logrus.WithError(err).Error("Error creating RabbitMQ billing client")
		} else {
			billingClients = append(billingClients, client)
			logrus.Info("RabbitMQ billing client initialized")
		}
	}

	// AmazonMQ billing client (new)
	if config.EnableAmazonmqBilling && config.AmazonmqURL != "" {
		client, err := billing.NewRMQBillingResilientClient(
			config.AmazonmqURL,
			config.RabbitmqRetryPubAttempts,
			config.RabbitmqRetryPubDelay,
			config.AmazonmqBillingExchange,
		)
		if err != nil {
			logrus.WithError(err).Error("Error creating AmazonMQ billing client")
		} else {
			billingClients = append(billingClients, client)
			logrus.Info("AmazonMQ billing client initialized")
		}
	}

	// Set billing client(s) on server
	if len(billingClients) > 0 {
		server.SetBilling(billing.NewMultiBillingClient(billingClients...))
	} else {
		logrus.Warn("No billing clients configured")
	}

	// Templates client (uses RabbitMQ)
	if config.RabbitmqURL != "" {
		templatesClient, err := templates.NewRMQTemplateClient(
			config.RabbitmqURL,
			config.RabbitmqRetryPubAttempts,
			config.RabbitmqRetryPubDelay,
			config.TemplatesExchangeName,
		)
		if err != nil {
			logrus.WithError(err).Error("Error creating templates RabbitMQ client")
		} else {
			server.SetTemplates(templatesClient)
		}
	} else {
		logrus.Warn("RabbitMQ URL not configured, templates client not initialized")
	}

	ch := make(chan os.Signal)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	logrus.WithField("comp", "main").WithField("signal", <-ch).Info("stopping")

	server.Stop()
}
