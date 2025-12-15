package courier

import "github.com/nyaruka/ezconf"

// Config is our top level configuration object
type Config struct {
	Backend                   string `help:"the backend that will be used by courier (currently only rapidpro is supported)"`
	SentryDSN                 string `help:"the DSN used for logging errors to Sentry"`
	Domain                    string `help:"the domain courier is exposed on"`
	Address                   string `help:"the network interface address courier will bind to"`
	Port                      int    `help:"the port courier will listen on"`
	DB                        string `help:"URL describing how to connect to the RapidPro database"`
	Redis                     string `help:"URL describing how to connect to Redis"`
	SpoolDir                  string `help:"the local directory where courier will write statuses or msgs that need to be retried (needs to be writable)"`
	S3Endpoint                string `help:"the S3 endpoint we will write attachments to"`
	S3Region                  string `help:"the S3 region we will write attachments to"`
	S3MediaBucket             string `help:"the S3 bucket we will write attachments to"`
	S3MediaPrefix             string `help:"the prefix that will be added to attachment filenames"`
	S3DisableSSL              bool   `help:"whether we disable SSL when accessing S3. Should always be set to False unless you're hosting an S3 compatible service within a secure internal network"`
	S3ForcePathStyle          bool   `help:"whether we force S3 path style. Should generally need to default to False unless you're hosting an S3 compatible service"`
	S3PresignedURLExpiration  int    `help:"the expiration time in hours for pre-signed URLs (default 168 hours / 7 days)"`
	S3UseIamRole              bool   `help:"whether we use IAM role to authenticate to S3"`
	AWSAccessKeyID            string `help:"the access key id to use when authenticating S3"`
	AWSSecretAccessKey        string `help:"the secret access key id to use when authenticating S3"`
	FacebookApplicationSecret string `help:"the Facebook app secret"`
	FacebookWebhookSecret     string `help:"the secret for Facebook webhook URL verification"`
	MaxWorkers                int    `help:"the maximum number of go routines that will be used for sending (set to 0 to disable sending)"`
	LibratoUsername           string `help:"the username that will be used to authenticate to Librato"`
	LibratoToken              string `help:"the token that will be used to authenticate to Librato"`
	StatusUsername            string `help:"the username that is needed to authenticate against the /status endpoint"`
	StatusPassword            string `help:"the password that is needed to authenticate against the /status endpoint"`
	LogLevel                  string `help:"the logging level courier should use"`
	Version                   string `help:"the version that will be used in request and response headers"`

	WhatsappAdminSystemUserToken    string `help:"the token of the admin system user for WhatsApp"`
	WhatsappCloudApplicationSecret  string `help:"the Whatsapp Cloud app secret"`
	WhatsappCloudWebhookSecret      string `help:"the secret for WhatsApp Cloud webhook URL verification"`
	WhatsappCloudWebhooksUrl        string `help:"the url where all WhatsApp Cloud webhooks will be sent"`
	WhatsappCloudWebhooksUrlFlows   string `help:"the url where WhatsApp Cloud flow_message webhooks will be sent to Flows"`
	WhatsappCloudWebhooksTokenFlows string `help:"the token for sending WhatsApp Cloud flow_message webhooks that will be sent to Flows"`

	// IncludeChannels is the list of channels to enable, empty means include all
	IncludeChannels []string

	// ExcludeChannels is the list of channels to exclude, empty means exclude none
	ExcludeChannels []string

	// WaitMediaCount is the count limit to wait for previous media message be delivered before current msg be send
	// Default is 10
	WaitMediaCount int
	// WaitMediaSleepDuration is the duration time in milliseconds of each wait sleep
	// Default is 1000
	WaitMediaSleepDuration int
	// WaitMediaChannels is the list of channels that have the logic of wait for previous media message be delivered before current msg be send
	// Default is WA, WAC, FB, FBA, IG
	WaitMediaChannels []string

	RabbitmqURL              string `help:"rabbitmq url"`
	RabbitmqRetryPubAttempts int    `help:"rabbitmq retry attempts"`
	RabbitmqRetryPubDelay    int    `help:"rabbitmq retry delay"`
	BillingExchangeName      string `help:"billing exchange name"`

	EmailProxyURL       string `help:"email proxy url"`
	EmailProxyAuthToken string `help:"email proxy auth token"`

	TemplatesExchangeName    string `help:"templates exchange name"`
	WhatsappCloudDemoAddress string `help:"the address of the router"`
	WhatsappCloudDemoURL     string `help:"the url of the demo"`
	WhatsappCloudDemoToken   string `help:"the token of the demo"`

	CallsWebhookURL   string `help:"the url where calls webhooks will be sent"`
	CallsWebhookToken string `help:"the token for calls webhooks"`
}

// NewConfig returns a new default configuration object
func NewConfig() *Config {
	return &Config{
		Backend:                      "rapidpro",
		Domain:                       "localhost",
		Address:                      "",
		Port:                         8080,
		DB:                           "postgres://temba:temba@localhost/temba?sslmode=disable",
		Redis:                        "redis://localhost:6379/15",
		SpoolDir:                     "/var/spool/courier",
		S3Endpoint:                   "",
		S3Region:                     "us-east-1",
		S3MediaBucket:                "courier-media",
		S3MediaPrefix:                "/media/",
		S3DisableSSL:                 false,
		S3ForcePathStyle:             false,
		S3PresignedURLExpiration:     168, // 7 days in hours
		S3UseIamRole:                 false,
		AWSAccessKeyID:               "",
		AWSSecretAccessKey:           "",
		FacebookApplicationSecret:    "missing_facebook_app_secret",
		FacebookWebhookSecret:        "missing_facebook_webhook_secret",
		WhatsappAdminSystemUserToken: "missing_whatsapp_admin_system_user_token",
		MaxWorkers:                   32,
		LogLevel:                     "error",
		Version:                      "Dev",
		WaitMediaCount:               10,
		WaitMediaSleepDuration:       1000,
		WaitMediaChannels:            []string{},
		RabbitmqRetryPubAttempts:     3,
		RabbitmqRetryPubDelay:        1000,
		BillingExchangeName:          "msgs.topic",
		EmailProxyURL:                "http://localhost:9090",
		EmailProxyAuthToken:          "",
		TemplatesExchangeName:        "templates",
		WhatsappCloudDemoAddress:     "1234567890",
		WhatsappCloudDemoURL:         "http://localhost:3000/wacr/receive",
		WhatsappCloudDemoToken:       "1234567890",
	}
}

// LoadConfig loads our configuration from the passed in filename
func LoadConfig(filename string) *Config {
	config := NewConfig()
	loader := ezconf.NewLoader(
		config,
		"courier", "Courier - A fast message broker for SMS and IP messages",
		[]string{filename},
	)

	loader.MustLoad()
	return config
}
