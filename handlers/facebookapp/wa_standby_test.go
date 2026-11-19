package facebookapp

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderWACStandby(t *testing.T) {
	const flowLine = "[outro atendimento] Book Appointment — Schedule your visit with us. — Tap below to continue — Book Now"
	const templateLine = "[outro atendimento] <image>\nHi Maria, enjoy 25% off!\nLimited time\nShop Now: https://example.com/summer2026"
	const bareLine = "[outro atendimento] template:summer_sale_2026 <image> | Maria | 25% | summer2026"

	t.Run("documented flow", func(t *testing.T) {
		echo := wacStandbyEcho{}
		echo.Message.Type = "interactive"
		echo.Message.Interactive.Type = "flow"
		echo.Message.Interactive.Header.Text = "Book Appointment"
		echo.Message.Interactive.Body.Text = "Schedule your visit with us."
		echo.Message.Interactive.Footer.Text = "Tap below to continue"
		echo.Message.Interactive.Action.Parameters.FlowCTA = "Book Now"
		echo.Flow = &wacStandbyFlow{Name: "Appointment Booking"}
		got, ok := renderWACStandbyEcho(echo)
		assert.True(t, ok)
		assert.Equal(t, flowLine, got)
	})

	t.Run("flow name only", func(t *testing.T) {
		echo := wacStandbyEcho{}
		echo.Message.Type = "interactive"
		echo.Message.Interactive.Type = "flow"
		echo.Flow = &wacStandbyFlow{Name: "Appointment Booking"}
		got, ok := renderWACStandbyEcho(echo)
		assert.True(t, ok)
		assert.Equal(t, "[outro atendimento] Appointment Booking", got)
	})

	t.Run("flow without text or name", func(t *testing.T) {
		echo := wacStandbyEcho{}
		echo.Message.Type = "interactive"
		echo.Message.Interactive.Type = "flow"
		got, ok := renderWACStandbyEcho(echo)
		assert.True(t, ok)
		assert.Equal(t, "[outro atendimento] <flow>", got)
	})

	t.Run("documented template", func(t *testing.T) {
		got, ok := renderWACStandbyEcho(summerSaleEcho(true))
		assert.True(t, ok)
		assert.Equal(t, templateLine, got)
		assert.NotContains(t, got, "\n[outro atendimento]")
	})

	t.Run("unmatched placeholder stays", func(t *testing.T) {
		echo := wacStandbyEcho{}
		echo.Message.Type = "template"
		echo.Message.Template = &wacStandbySendTemplate{
			Name: "partial",
			Components: []wacStandbySendComponent{{
				Type: "body",
				Parameters: []wacStandbyParameter{
					{Type: "text", Text: "one"},
					{Type: "text", Text: "two"},
				},
			}},
		}
		echo.Template = &wacStandbyTemplateDef{
			Components: []wacStandbyTemplateComponent{{Type: "BODY", Text: "{{3}}"}},
		}
		got, ok := renderWACStandbyEcho(echo)
		assert.True(t, ok)
		assert.Equal(t, "[outro atendimento] {{3}}", got)
	})

	t.Run("named parameter", func(t *testing.T) {
		echo := wacStandbyEcho{}
		echo.Message.Type = "template"
		echo.Message.Template = &wacStandbySendTemplate{
			Name: "greeting",
			Components: []wacStandbySendComponent{{
				Type: "body",
				Parameters: []wacStandbyParameter{{
					Type: "text", ParameterName: "customer_name", Text: "Maria",
				}},
			}},
		}
		echo.Template = &wacStandbyTemplateDef{
			Components: []wacStandbyTemplateComponent{{Type: "BODY", Text: "Hi {{customer_name}}"}},
		}
		got, ok := renderWACStandbyEcho(echo)
		assert.True(t, ok)
		assert.Equal(t, "[outro atendimento] Hi Maria", got)
	})

	t.Run("template without structure", func(t *testing.T) {
		got, ok := renderWACStandbyEcho(summerSaleEcho(false))
		assert.True(t, ok)
		assert.Equal(t, bareLine, got)
	})

	t.Run("template name only", func(t *testing.T) {
		echo := wacStandbyEcho{}
		echo.Message.Type = "template"
		echo.Message.Template = &wacStandbySendTemplate{Name: "summer_sale_2026"}
		got, ok := renderWACStandbyEcho(echo)
		assert.True(t, ok)
		assert.Equal(t, "[outro atendimento] template:summer_sale_2026", got)
	})

	t.Run("media markers", func(t *testing.T) {
		image := wacStandbyMessage{Type: "image"}
		got, ok := renderWACStandbyShopper(image)
		assert.True(t, ok)
		assert.Equal(t, "[shopper] <image>", got)

		audio := wacStandbyMessage{Type: "audio"}
		got, ok = renderWACStandbyShopper(audio)
		assert.True(t, ok)
		assert.Equal(t, "[shopper] <audio>", got)

		echo := wacStandbyEcho{}
		echo.Message.Type = "image"
		got, ok = renderWACStandbyEcho(echo)
		assert.True(t, ok)
		assert.Equal(t, "[outro atendimento] <image>", got)
	})

	t.Run("text", func(t *testing.T) {
		msg := wacStandbyMessage{Type: "text"}
		msg.Text.Body = "Test standby message"
		got, ok := renderWACStandbyShopper(msg)
		assert.True(t, ok)
		assert.Equal(t, "[shopper] Test standby message", got)

		echo := wacStandbyEcho{}
		echo.Message.Type = "text"
		echo.Message.Text.Body = "Hello! Your order #12345 has shipped."
		got, ok = renderWACStandbyEcho(echo)
		assert.True(t, ok)
		assert.Equal(t, "[outro atendimento] Hello! Your order #12345 has shipped.", got)
	})

	t.Run("blank text", func(t *testing.T) {
		msg := wacStandbyMessage{Type: "text"}
		msg.Text.Body = "   "
		_, ok := renderWACStandbyShopper(msg)
		assert.False(t, ok)
	})
}

func summerSaleEcho(withStructure bool) wacStandbyEcho {
	echo := wacStandbyEcho{}
	echo.Message.Type = "template"
	echo.Message.Template = &wacStandbySendTemplate{
		Name: "summer_sale_2026",
		Components: []wacStandbySendComponent{
			{Type: "header", Parameters: []wacStandbyParameter{{Type: "image"}}},
			{Type: "body", Parameters: []wacStandbyParameter{{Type: "text", Text: "Maria"}, {Type: "text", Text: "25%"}}},
			{Type: "button", SubType: "url", Index: 0, Parameters: []wacStandbyParameter{{Type: "text", Text: "summer2026"}}},
		},
	}
	if withStructure {
		echo.Template = &wacStandbyTemplateDef{
			Name: "summer_sale_2026",
			Components: []wacStandbyTemplateComponent{
				{Type: "HEADER", Format: "IMAGE"},
				{Type: "BODY", Text: "Hi {{1}}, enjoy {{2}} off!"},
				{Type: "FOOTER", Text: "Limited time"},
				{Type: "BUTTONS", Buttons: []wacStandbyTemplateButton{{Type: "URL", Text: "Shop Now", URL: "https://example.com/{{1}}"}}},
			},
		}
	}
	return echo
}
