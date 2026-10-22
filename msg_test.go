package courier_test

import (
	"testing"

	"github.com/nyaruka/courier"
	"github.com/stretchr/testify/assert"
)

func TestSplitAttachment(t *testing.T) {
	testCases := []struct {
		name         string
		attachment   string
		expectedType string
		expectedURL  string
	}{
		{
			name:         "Valid Attachment",
			attachment:   "image/jpeg:https://example.com/image.jpg",
			expectedType: "image/jpeg",
			expectedURL:  "https://example.com/image.jpg",
		},
		{
			name:         "No Type",
			attachment:   "https://example.com/image.jpg",
			expectedType: "https",
			expectedURL:  "//example.com/image.jpg",
		},
		{
			name:         "Multiple Colons",
			attachment:   "image/jpeg:https://example.com/image:with:colons.jpg",
			expectedType: "image/jpeg",
			expectedURL:  "https://example.com/image:with:colons.jpg",
		},
		{
			name:         "Empty String",
			attachment:   "",
			expectedType: "",
			expectedURL:  "",
		},
		{
			name:         "Only Type",
			attachment:   "image/jpeg:",
			expectedType: "image/jpeg",
			expectedURL:  "",
		},
		{
			name:         "Only URL",
			attachment:   ":https://example.com/image.jpg",
			expectedType: "",
			expectedURL:  "https://example.com/image.jpg",
		},
		{
			name:         "Multiple Types",
			attachment:   "image/jpeg:video/mp4:https://example.com/file",
			expectedType: "image/jpeg",
			expectedURL:  "video/mp4:https://example.com/file",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			attType, attURL := courier.SplitAttachment(tc.attachment)
			assert.Equal(t, tc.expectedType, attType)
			assert.Equal(t, tc.expectedURL, attURL)
		})
	}
}
