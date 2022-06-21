module github.com/nyaruka/courier

require (
	github.com/antchfx/xmlquery v0.0.0-20181223105952-355641961c92
	github.com/antchfx/xpath v0.0.0-20181208024549-4bbdf6db12aa // indirect
	github.com/aws/aws-sdk-go v1.40.56
	github.com/buger/jsonparser v0.0.0-20180318095312-2cac668e8456
	github.com/certifi/gocertifi v0.0.0-20180118203423-deb3ae2ef261 // indirect
	github.com/dghubble/oauth1 v0.4.0
	github.com/evalphobia/logrus_sentry v0.4.6
	github.com/getsentry/raven-go v0.0.0-20180517221441-ed7bcb39ff10 // indirect
	github.com/go-chi/chi v4.1.2+incompatible
	github.com/go-errors/errors v1.0.1
	github.com/go-playground/locales v0.14.0 // indirect
	github.com/go-playground/universal-translator v0.18.0 // indirect
	github.com/gofrs/uuid v3.3.0+incompatible
	github.com/gomodule/redigo v2.0.0+incompatible
	github.com/gorilla/schema v1.0.2
	github.com/jmoiron/sqlx v1.3.4
	github.com/kr/pretty v0.1.0 // indirect
	github.com/kylelemons/godebug v0.0.0-20170820004349-d65d576e9348 // indirect
	github.com/lib/pq v1.10.4
	github.com/mattn/go-sqlite3 v1.14.10 // indirect
	github.com/nyaruka/ezconf v0.2.1
	github.com/nyaruka/gocommon v1.16.2
	github.com/nyaruka/librato v1.0.0
	github.com/nyaruka/null v1.1.1
	github.com/patrickmn/go-cache v2.1.0+incompatible
	github.com/pkg/errors v0.9.1
	github.com/sirupsen/logrus v1.4.2
	github.com/stretchr/testify v1.7.1
	golang.org/x/mod v0.4.2
	gopkg.in/check.v1 v1.0.0-20180628173108-788fd7840127 // indirect
	gopkg.in/go-playground/validator.v9 v9.31.0
	gopkg.in/h2non/filetype.v1 v1.0.5
)

replace github.com/nyaruka/gocommon v1.16.2 => github.com/Ilhasoft/gocommon v1.16.2-teams-handler

require (
	github.com/gabriel-vasile/mimetype v1.4.0
	github.com/golang-jwt/jwt/v4 v4.4.1
	github.com/lestrrat-go/jwx v1.2.25
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.0.0-20210816181553-5444fa50b93d // indirect
	github.com/fatih/structs v1.0.0 // indirect
	github.com/goccy/go-json v0.9.7 // indirect
	github.com/golang/protobuf v1.3.2 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/konsorten/go-windows-terminal-sequences v1.0.1 // indirect
	github.com/leodido/go-urn v1.2.1 // indirect
	github.com/lestrrat-go/backoff/v2 v2.0.8 // indirect
	github.com/lestrrat-go/blackmagic v1.0.0 // indirect
	github.com/lestrrat-go/httpcc v1.0.1 // indirect
	github.com/lestrrat-go/iter v1.0.1 // indirect
	github.com/lestrrat-go/option v1.0.0 // indirect
	github.com/naoina/go-stringutil v0.1.0 // indirect
	github.com/naoina/toml v0.1.1 // indirect
	github.com/nyaruka/phonenumbers v1.0.71 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/shopspring/decimal v1.2.0 // indirect
	golang.org/x/crypto v0.0.0-20220427172511-eb4f295cb31f // indirect
	golang.org/x/net v0.0.0-20211112202133-69e39bad7dc2 // indirect
	golang.org/x/sys v0.0.0-20210615035016-665e8c7367d1 // indirect
	golang.org/x/text v0.3.6 // indirect
	gopkg.in/yaml.v3 v3.0.0-20200313102051-9f266ea9e77c // indirect
)

go 1.17
