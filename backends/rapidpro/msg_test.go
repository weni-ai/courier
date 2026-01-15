package rapidpro

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDBMsg_NewContactFields(t *testing.T) {
	// Test 1: NewContactFields is nil when not set
	msg := &DBMsg{}
	assert.Nil(t, msg.NewContactFields())

	// Test 2: NewContactFields returns correct values when set
	fields := map[string]string{
		"name":  "John Doe",
		"email": "john@example.com",
	}
	msg.NewContactFields_ = &fields
	result := msg.NewContactFields()
	assert.NotNil(t, result)
	assert.Equal(t, "John Doe", result["name"])
	assert.Equal(t, "john@example.com", result["email"])

	// Test 3: NewContactFields handles nil pointer to nil map
	var nilMap map[string]string
	msg2 := &DBMsg{}
	msg2.NewContactFields_ = &nilMap
	assert.Nil(t, msg2.NewContactFields())

	// Test 4: NewContactFields handles empty map
	emptyMap := map[string]string{}
	msg3 := &DBMsg{}
	msg3.NewContactFields_ = &emptyMap
	result3 := msg3.NewContactFields()
	assert.NotNil(t, result3)
	assert.Equal(t, 0, len(result3))
}

func TestDBMsg_NewContactFieldsJSON(t *testing.T) {
	// Test JSON unmarshaling with contact fields
	msgJSON := `{
		"status": "P",
		"direction": "I",
		"text": "Test message",
		"org_id": 1,
		"channel_id": 10,
		"new_contact_fields": {"name": "Jane", "phone": "123456"}
	}`

	msg := &DBMsg{}
	err := json.Unmarshal([]byte(msgJSON), msg)
	assert.NoError(t, err)
	assert.NotNil(t, msg.NewContactFields())
	assert.Equal(t, "Jane", msg.NewContactFields()["name"])
	assert.Equal(t, "123456", msg.NewContactFields()["phone"])

	// Test JSON unmarshaling with null contact fields
	msgJSONNull := `{
		"status": "P",
		"direction": "I",
		"text": "Test message",
		"org_id": 1,
		"channel_id": 10,
		"new_contact_fields": null
	}`

	msg2 := &DBMsg{}
	err = json.Unmarshal([]byte(msgJSONNull), msg2)
	assert.NoError(t, err)
	assert.Nil(t, msg2.NewContactFields())

	// Test JSON unmarshaling without contact fields
	msgJSONNoFields := `{
		"status": "P",
		"direction": "I",
		"text": "Test message",
		"org_id": 1,
		"channel_id": 10
	}`

	msg3 := &DBMsg{}
	err = json.Unmarshal([]byte(msgJSONNoFields), msg3)
	assert.NoError(t, err)
	assert.Nil(t, msg3.NewContactFields())

	// Test JSON unmarshaling with empty contact fields
	msgJSONEmpty := `{
		"status": "P",
		"direction": "I",
		"text": "Test message",
		"org_id": 1,
		"channel_id": 10,
		"new_contact_fields": {}
	}`

	msg4 := &DBMsg{}
	err = json.Unmarshal([]byte(msgJSONEmpty), msg4)
	assert.NoError(t, err)
	assert.NotNil(t, msg4.NewContactFields())
	assert.Equal(t, 0, len(msg4.NewContactFields()))
}

func TestDBMsg_WithNewContactFields(t *testing.T) {
	msg := &DBMsg{}

	// Test WithNewContactFields sets fields correctly
	fields := map[string]string{
		"field1": "value1",
		"field2": "value2",
	}
	result := msg.WithNewContactFields(fields)
	assert.NotNil(t, result)
	assert.Equal(t, "value1", msg.NewContactFields()["field1"])
	assert.Equal(t, "value2", msg.NewContactFields()["field2"])

	// Test WithNewContactFields with nil
	msg2 := &DBMsg{}
	msg2.WithNewContactFields(nil)
	assert.Nil(t, msg2.NewContactFields())

	// Test WithNewContactFields with empty map
	msg3 := &DBMsg{}
	msg3.WithNewContactFields(map[string]string{})
	assert.NotNil(t, msg3.NewContactFields())
	assert.Equal(t, 0, len(msg3.NewContactFields()))

	// Test method chaining
	msg4 := &DBMsg{}
	msg4.WithNewContactFields(map[string]string{"key": "value"}).WithContactName("Test Name")
	assert.Equal(t, "value", msg4.NewContactFields()["key"])
	assert.Equal(t, "Test Name", msg4.ContactName())
}
