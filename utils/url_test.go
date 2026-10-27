package utils_test

import (
	"net/url"
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
		{
			label:    "encoded slash in path segment is preserved",
			input:    "https://foo.bar/foo%2Fbar.jpg",
			expected: "https://foo.bar/foo%2Fbar.jpg",
		},
		{
			label:    "encoded slash and unicode in same path",
			input:    "https://foo.bar/foo%2F1ª-edicao.jpg",
			expected: "https://foo.bar/foo%2F1%C2%AA-edicao.jpg",
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

func TestEncodeMediaURLPresigned(t *testing.T) {
	presigned := "https://bucket.s3.amazonaws.com/media/1/card.jpg" +
		"?X-Amz-Algorithm=AWS4-HMAC-SHA256" +
		"&X-Amz-Credential=AKIAEXAMPLE%2F20261021%2Fus-east-1%2Fs3%2Faws4_request" +
		"&X-Amz-Date=20261021T120000Z" +
		"&X-Amz-Expires=86400" +
		"&X-Amz-Security-Token=FwoGZXIvYXdzE%2B%2Fabc%3D%3D" +
		"&X-Amz-SignedHeaders=host" +
		"&X-Amz-Signature=0123456789abcdef"

	encoded, err := utils.EncodeMediaURL(presigned)
	require.NoError(t, err)

	original, err := url.Parse(presigned)
	require.NoError(t, err)
	result, err := url.Parse(encoded)
	require.NoError(t, err)

	assert.Equal(t, original.Scheme+"://"+original.Host+original.EscapedPath(), result.Scheme+"://"+result.Host+result.EscapedPath())
	assert.Equal(t, original.Query(), result.Query())
	assert.Equal(t, "AKIAEXAMPLE/20261021/us-east-1/s3/aws4_request", result.Query().Get("X-Amz-Credential"))
	assert.Equal(t, "FwoGZXIvYXdzE+/abc==", result.Query().Get("X-Amz-Security-Token"))
}

func TestStripPresignParams(t *testing.T) {
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
			label:    "url without query is unchanged",
			input:    "https://bucket.s3.amazonaws.com/media/card.jpg",
			expected: "https://bucket.s3.amazonaws.com/media/card.jpg",
		},
		{
			label:    "presign params are removed",
			input:    "https://bucket.s3.amazonaws.com/media/card.jpg?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=AKIA%2F20261021&X-Amz-Signature=abc",
			expected: "https://bucket.s3.amazonaws.com/media/card.jpg",
		},
		{
			label:    "other params are kept in order and encoding",
			input:    "https://cdn.example.com/card.jpg?b=2&X-Amz-Date=20261021T120000Z&a=edi%C3%A7%C3%A3o&X-Amz-Signature=abc",
			expected: "https://cdn.example.com/card.jpg?b=2&a=edi%C3%A7%C3%A3o",
		},
		{
			label:    "url without presign params is unchanged",
			input:    "https://cdn.example.com/card.jpg?b=2&a=1",
			expected: "https://cdn.example.com/card.jpg?b=2&a=1",
		},
		{
			label:    "presign param names are matched case insensitively",
			input:    "https://cdn.example.com/card.jpg?x-amz-signature=abc&X%2DAmz%2DDate=20261021T120000Z",
			expected: "https://cdn.example.com/card.jpg",
		},
		{
			label:    "encoded path is preserved",
			input:    "https://bucket.s3.amazonaws.com/media/1%C2%AA-edi%C3%A7%C3%A3o.jpg?X-Amz-Signature=abc",
			expected: "https://bucket.s3.amazonaws.com/media/1%C2%AA-edi%C3%A7%C3%A3o.jpg",
		},
		{
			label:    "invalid url is unchanged",
			input:    "http://[::1?X-Amz-Signature=abc",
			expected: "http://[::1?X-Amz-Signature=abc",
		},
	}

	for _, tc := range tcs {
		t.Run(tc.label, func(t *testing.T) {
			assert.Equal(t, tc.expected, utils.StripPresignParams(tc.input))
		})
	}
}

func TestStripPresignParamsIsStableAcrossSignatures(t *testing.T) {
	first := "https://bucket.s3.amazonaws.com/media/card.jpg?X-Amz-Date=20261021T120000Z&X-Amz-Signature=aaa"
	second := "https://bucket.s3.amazonaws.com/media/card.jpg?X-Amz-Date=20261022T090000Z&X-Amz-Signature=bbb"

	assert.Equal(t, utils.StripPresignParams(first), utils.StripPresignParams(second))
}
