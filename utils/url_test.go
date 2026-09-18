package utils_test

import (
	"testing"

	"github.com/nyaruka/courier/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeMediaURL(t *testing.T) {
	tcs := []struct {
		label    string
		input    string
		expected string
	}{
		{
			label:    "empty string",
			input:    "",
			expected: "",
		},
		{
			label:    "ascii url unchanged",
			input:    "https://foo.bar/card1.jpg",
			expected: "https://foo.bar/card1.jpg",
		},
		{
			label:    "ordinal indicator in path",
			input:    "https://foo.bar/1ª-edicao.jpg",
			expected: "https://foo.bar/1%C2%AA-edicao.jpg",
		},
		{
			label:    "already encoded is idempotent",
			input:    "https://foo.bar/1%C2%AA-edicao.jpg",
			expected: "https://foo.bar/1%C2%AA-edicao.jpg",
		},
		{
			label:    "pt-br path characters",
			input:    "https://cdn.example.com/capa-educação-ação.jpg",
			expected: "https://cdn.example.com/capa-educa%C3%A7%C3%A3o-a%C3%A7%C3%A3o.jpg",
		},
		{
			label:    "mixed encoded and raw unicode",
			input:    "https://cdn.example.com/capa-%C3%A7ão.jpg",
			expected: "https://cdn.example.com/capa-%C3%A7%C3%A3o.jpg",
		},
		{
			label:    "space in path",
			input:    "https://cdn.example.com/foo bar.jpg",
			expected: "https://cdn.example.com/foo%20bar.jpg",
		},
		{
			label:    "unicode in query",
			input:    "https://cdn.example.com/file.jpg?name=edição",
			expected: "https://cdn.example.com/file.jpg?name=edi%C3%A7%C3%A3o",
		},
	}

	for _, tc := range tcs {
		t.Run(tc.label, func(t *testing.T) {
			encoded, err := utils.EncodeMediaURL(tc.input)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, encoded)

			again, err := utils.EncodeMediaURL(encoded)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, again)
		})
	}
}

func TestEncodeMediaURLInvalid(t *testing.T) {
	_, err := utils.EncodeMediaURL("http://[::1")
	assert.Error(t, err)
}
