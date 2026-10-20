package handlers

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var sampleWebPBytes = MinimalWebPBytes()

var sampleJPEGBytes = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00, 0x01}

func TestRewriteWebPImageURL(t *testing.T) {
	const sourceURL = "https://example.com/card.webp"
	const disguisedURL = "https://example.com/card.jpg"

	tests := []struct {
		name            string
		mediaURL        string
		getBody         []byte
		getErr          error
		putErr          error
		useRealConverter bool
		wantURL         string
		wantErr         string
		wantPutCall     bool
	}{
		{
			name:        "webp bytes are converted and stored",
			mediaURL:    sourceURL,
			getBody:     sampleWebPBytes,
			wantURL:     "stored://" + PngFilenameForURL(sourceURL),
			wantPutCall: true,
		},
		{
			name:        "disguised jpeg url with webp bytes still converts",
			mediaURL:    disguisedURL,
			getBody:     sampleWebPBytes,
			wantURL:     "stored://" + PngFilenameForURL(disguisedURL),
			wantPutCall: true,
		},
		{
			name:     "real jpeg bytes keep original url",
			mediaURL: disguisedURL,
			getBody:  sampleJPEGBytes,
			wantURL:  disguisedURL,
		},
		{
			name:     "download error",
			mediaURL: sourceURL,
			getErr:   errors.New("download failed"),
			wantErr:  "error fetching carousel image",
		},
		{
			name:             "invalid webp content fails conversion",
			mediaURL:         sourceURL,
			getBody:          []byte("RIFF\x10\x00\x00\x00WEBPinvalid"),
			useRealConverter: true,
			wantErr:          "error converting WebP image to PNG",
		},
		{
			name:        "store error",
			mediaURL:    sourceURL,
			getBody:     sampleWebPBytes,
			putErr:      errors.New("store failed"),
			wantErr:     "error storing converted PNG image",
			wantPutCall: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			putCalled := false

			get := func(mediaURL string) ([]byte, error) {
				assert.Equal(t, tc.mediaURL, mediaURL)
				if tc.getErr != nil {
					return nil, tc.getErr
				}
				return tc.getBody, nil
			}

			put := func(filename, contentType string, contents []byte) (string, error) {
				putCalled = true
				assert.Equal(t, PngFilenameForURL(tc.mediaURL), filename)
				assert.Equal(t, "image/png", contentType)
				assert.NotEmpty(t, contents)
				if tc.putErr != nil {
					return "", tc.putErr
				}
				return "stored://" + filename, nil
			}

			convert := func(data []byte) ([]byte, error) {
				if tc.useRealConverter {
					return ConvertWebPToPNG(data, MaxImageSizeBytes)
				}
				return []byte("converted-png"), nil
			}

			gotURL, err := rewriteWebPImageURL(tc.mediaURL, get, put, convert)

			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantURL, gotURL)
			assert.Equal(t, tc.wantPutCall, putCalled)
		})
	}
}

func TestPngFilenameForURLIsDeterministic(t *testing.T) {
	url := "https://example.com/media.webp"
	assert.Equal(t, PngFilenameForURL(url), PngFilenameForURL(url))
	assert.NotEqual(t, PngFilenameForURL(url), PngFilenameForURL(url+"x"))
}
