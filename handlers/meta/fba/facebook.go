package fba

import (
	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/handlers/meta/metacommons"
)

func init() {
	courier.RegisterHandler(metacommons.NewHandler("FBA", "Faceboook", false))
}
