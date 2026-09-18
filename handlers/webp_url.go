package handlers

import (
	"crypto/sha256"
	"fmt"

	"github.com/pkg/errors"
)

// MediaGetter downloads media bytes from a URL.
type MediaGetter func(mediaURL string) ([]byte, error)

// MediaPutter stores converted media and returns a public URL.
type MediaPutter func(filename, contentType string, contents []byte) (string, error)

// PngFilenameForURL returns a deterministic PNG filename derived from the source URL.
func PngFilenameForURL(mediaURL string) string {
	sum := sha256.Sum256([]byte(mediaURL))
	return fmt.Sprintf("%x.png", sum)
}

type webPConverter func([]byte) ([]byte, error)

// RewriteWebPImageURL downloads the media at mediaURL, converts WebP images to PNG,
// stores them via put, and returns the public URL. Non-WebP images return mediaURL unchanged.
func RewriteWebPImageURL(mediaURL string, get MediaGetter, put MediaPutter) (string, error) {
	return rewriteWebPImageURL(mediaURL, get, put, func(data []byte) ([]byte, error) {
		return ConvertWebPToPNG(data, MaxImageSizeBytes)
	})
}

func rewriteWebPImageURL(mediaURL string, get MediaGetter, put MediaPutter, convert webPConverter) (string, error) {
	body, err := get(mediaURL)
	if err != nil {
		return "", errors.Wrap(err, "error fetching carousel image")
	}

	if !IsWebPImage(body) {
		return mediaURL, nil
	}

	png, err := convert(body)
	if err != nil {
		return "", errors.Wrap(err, "error converting WebP image to PNG")
	}

	storedURL, err := put(PngFilenameForURL(mediaURL), "image/png", png)
	if err != nil {
		return "", errors.Wrap(err, "error storing converted PNG image")
	}

	return storedURL, nil
}
