package handlers

import (
	"bytes"
	_ "embed"
	"image"
	"image/png"
	"math"

	"github.com/pkg/errors"
	"golang.org/x/image/webp"
)

//go:embed testdata/carousel.webp
var minimalWebPBytes []byte

//go:embed testdata/sticker_512.webp
var sticker512WebPBytes []byte

const (
	// MaxImageSizeBytes is the maximum allowed size for images (5MB)
	// This is used for image resizing to ensure images don't exceed platform limits
	MaxImageSizeBytes = 5 * 1024 * 1024

	// WhatsAppStickerSize is the required width and height for WhatsApp sticker images.
	WhatsAppStickerSize = 512
	// WhatsAppStaticStickerMaxBytes is the max file size for static WhatsApp stickers.
	WhatsAppStaticStickerMaxBytes = 100 * 1024
	// WhatsAppAnimatedStickerMaxBytes is the max file size for animated WhatsApp stickers.
	WhatsAppAnimatedStickerMaxBytes = 500 * 1024
)

// IsWebPImage detects if the image data is actually WebP format by checking the magic bytes
// This is important because some images may have .jpeg or .png extension but are actually WebP
func IsWebPImage(data []byte) bool {
	if len(data) < 12 {
		return false
	}
	// WebP files start with "RIFF" (4 bytes) followed by 4 bytes for file size, then "WEBP" (4 bytes)
	return string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP"
}

func isAnimatedWebP(data []byte) bool {
	return bytes.Contains(data, []byte("ANIM")) || bytes.Contains(data, []byte("ANMF"))
}

// webpCanvasSize returns the logical canvas width and height for a WebP file.
// Animated and other extended WebP files expose dimensions in the VP8X chunk; golang.org/x/image/webp cannot decode them.
func webpCanvasSize(data []byte) (width, height int, ok bool) {
	if !IsWebPImage(data) {
		return 0, 0, false
	}

	if len(data) >= 30 && string(data[12:16]) == "VP8X" {
		chunkSize := int(data[16]) | int(data[17])<<8 | int(data[18])<<16 | int(data[19])<<24
		if chunkSize < 10 || len(data) < 20+chunkSize {
			return 0, 0, false
		}
		width = (int(data[24]) | int(data[25])<<8 | int(data[26])<<16) + 1
		height = (int(data[27]) | int(data[28])<<8 | int(data[29])<<16) + 1
		return width, height, width > 0 && height > 0
	}

	cfg, err := webp.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, false
	}
	return cfg.Width, cfg.Height, true
}

// IsWhatsAppStickerWebP reports whether data is a WebP file that meets WhatsApp sticker constraints.
func IsWhatsAppStickerWebP(data []byte) bool {
	if !IsWebPImage(data) {
		return false
	}

	maxBytes := WhatsAppStaticStickerMaxBytes
	if isAnimatedWebP(data) {
		maxBytes = WhatsAppAnimatedStickerMaxBytes
	}
	if len(data) > maxBytes {
		return false
	}

	width, height, ok := webpCanvasSize(data)
	if !ok {
		return false
	}
	return width == WhatsAppStickerSize && height == WhatsAppStickerSize
}

// ResizeImage resizes an image to fit within maxSizeBytes by reducing dimensions proportionally
// It uses a recursive approach to ensure the final image size is within the limit
func ResizeImage(img image.Image, maxSizeBytes int) (image.Image, error) {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// Try encoding first to check size
	var testBuf bytes.Buffer
	err := png.Encode(&testBuf, img)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to encode PNG for size check")
	}

	// If already under limit, return original
	if testBuf.Len() <= maxSizeBytes {
		return img, nil
	}

	// Calculate reduction factor based on current size
	// PNG size is roughly proportional to width * height, so we reduce by square root of size ratio
	sizeRatio := float64(testBuf.Len()) / float64(maxSizeBytes)
	reductionFactor := 1.0 / math.Sqrt(sizeRatio*0.9) // 0.9 is a safety margin

	// Calculate new dimensions
	newWidth := int(float64(width) * reductionFactor)
	newHeight := int(float64(height) * reductionFactor)

	// Ensure minimum dimensions (at least 100x100)
	if newWidth < 100 {
		newWidth = 100
	}
	if newHeight < 100 {
		newHeight = 100
	}

	// Create new image with new dimensions and scale using nearest neighbor (simpler and faster)
	resized := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))

	// Simple scaling using nearest neighbor
	for y := 0; y < newHeight; y++ {
		for x := 0; x < newWidth; x++ {
			srcX := int(float64(x) * float64(width) / float64(newWidth))
			srcY := int(float64(y) * float64(height) / float64(newHeight))
			if srcX < width && srcY < height {
				resized.Set(x, y, img.At(srcX, srcY))
			}
		}
	}

	// Verify the resized image is under the limit
	var verifyBuf bytes.Buffer
	err = png.Encode(&verifyBuf, resized)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to encode resized PNG")
	}

	// If still too large, recursively resize with more aggressive reduction
	if verifyBuf.Len() > maxSizeBytes {
		return ResizeImage(resized, maxSizeBytes)
	}

	return resized, nil
}

// normalizeToRGBA converts any image format to RGBA with 8 bits per channel
// This ensures compatibility with WhatsApp Cloud API requirements
func normalizeToRGBA(img image.Image) *image.RGBA {
	bounds := img.Bounds()
	rgba := image.NewRGBA(bounds)

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := img.At(x, y)
			rgba.Set(x, y, c)
		}
	}

	return rgba
}

// MinimalWebPBytes returns a decodable WebP image for tests.
func MinimalWebPBytes() []byte {
	return minimalWebPBytes
}

// Sticker512WebPBytes returns a 512x512 WebP image for tests.
func Sticker512WebPBytes() []byte {
	return sticker512WebPBytes
}

// ConvertWebPToPNG converts a WebP image to PNG format
// This is needed because some platforms (like Meta/Facebook) don't accept WebP images in templates
// The resulting PNG will be in RGBA format with 8 bits per channel (WhatsApp requirement)
// and will be resized if necessary to ensure it doesn't exceed the size limit
func ConvertWebPToPNG(webpData []byte, maxSizeBytes int) ([]byte, error) {
	img, err := webp.Decode(bytes.NewReader(webpData))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to decode WebP image")
	}

	// Normalize to RGBA format with 8 bits per channel (WhatsApp requirement)
	// This ensures the image is in RGB/RGBA format regardless of the original WebP format
	rgbaImg := normalizeToRGBA(img)

	// Resize if necessary to stay under size limit
	resizedImg, err := ResizeImage(rgbaImg, maxSizeBytes)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to resize image")
	}

	// Ensure the resized image is also RGBA (ResizeImage may return the original if no resize needed)
	var finalImg *image.RGBA
	if resizedRGBA, ok := resizedImg.(*image.RGBA); ok {
		finalImg = resizedRGBA
	} else {
		// If ResizeImage returned a different format, normalize again
		finalImg = normalizeToRGBA(resizedImg)
	}

	var buf bytes.Buffer
	encoder := &png.Encoder{CompressionLevel: png.BestCompression}
	err = encoder.Encode(&buf, finalImg)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to encode PNG image")
	}

	// Final size check - if still over limit, return error
	if buf.Len() > maxSizeBytes {
		return nil, errors.Errorf("image size (%d bytes) exceeds maximum allowed size (%d bytes) even after resizing", buf.Len(), maxSizeBytes)
	}

	return buf.Bytes(), nil
}
