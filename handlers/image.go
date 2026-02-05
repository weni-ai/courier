package handlers

import (
	"bytes"
	"image"
	"image/png"
	"math"

	"github.com/pkg/errors"
	"golang.org/x/image/webp"
)

const (
	// MaxImageSizeBytes is the maximum allowed size for images (5MB)
	// This is used for image resizing to ensure images don't exceed platform limits
	MaxImageSizeBytes = 5 * 1024 * 1024
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

// ConvertWebPToPNG converts a WebP image to PNG format
// This is needed because some platforms (like Meta/Facebook) don't accept WebP images in templates
// We use PNG as it's lossless, preserves image quality, and supports transparency
// The resulting PNG will be resized if necessary to ensure it doesn't exceed the size limit
func ConvertWebPToPNG(webpData []byte, maxSizeBytes int) ([]byte, error) {
	img, err := webp.Decode(bytes.NewReader(webpData))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to decode WebP image")
	}

	// Resize if necessary to stay under size limit
	resizedImg, err := ResizeImage(img, maxSizeBytes)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to resize image")
	}

	var buf bytes.Buffer
	encoder := &png.Encoder{CompressionLevel: png.BestCompression}
	err = encoder.Encode(&buf, resizedImg)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to encode PNG image")
	}

	// Final size check - if still over limit, return error
	if buf.Len() > maxSizeBytes {
		return nil, errors.Errorf("image size (%d bytes) exceeds maximum allowed size (%d bytes) even after resizing", buf.Len(), maxSizeBytes)
	}

	return buf.Bytes(), nil
}
