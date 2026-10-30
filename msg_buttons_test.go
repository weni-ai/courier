package courier

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestButtonsFromMetadata(t *testing.T) {
	buttons := ButtonsFromMetadata(json.RawMessage(`{"buttons":[{"sub_type":"copy_code","parameters":[{"type":"coupon_code","text":"1234","coupon_code":"ignored"}]}]}`))
	assert.Equal(t, []ButtonComponent{{
		SubType:    "copy_code",
		Parameters: []ButtonParam{{Type: "coupon_code", Text: "1234"}},
	}}, buttons)

	missingText := ButtonsFromMetadata(json.RawMessage(`{"buttons":[{"sub_type":"copy_code","parameters":[{"type":"coupon_code"}]}]}`))
	assert.Len(t, missingText, 1)
	assert.Equal(t, "copy_code", missingText[0].SubType)
	assert.Equal(t, "", missingText[0].Parameters[0].Text)

	assert.Nil(t, ButtonsFromMetadata(nil))
	assert.Nil(t, ButtonsFromMetadata(json.RawMessage(`{"buttons":"bad"}`)))
}
