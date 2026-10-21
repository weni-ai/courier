package rapidpro

import (
	"context"
	"fmt"
	"net/url"
	"testing"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/gocommon/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSigningTestBackend(t *testing.T, expirationHours int) *backend {
	config := courier.NewConfig()
	config.S3Region = "sa-east-1"
	config.S3MediaBucket = "courier-media"
	config.S3SignedBuckets = "flows-static, mailroom-attachments"
	config.S3PresignedURLExpiration = expirationHours
	config.AWSAccessKeyID = "AKIAFAKEKEY"
	config.AWSSecretAccessKey = "fake-secret"

	signer, err := newAttachmentURLSigner(config, &storage.S3Options{
		AWSAccessKeyID:     config.AWSAccessKeyID,
		AWSSecretAccessKey: config.AWSSecretAccessKey,
		Region:             config.S3Region,
	})
	require.NoError(t, err)

	return &backend{config: config, attachmentURLSigner: signer}
}

func TestSignAttachmentURL(t *testing.T) {
	ctx := context.Background()
	b := newSigningTestBackend(t, 168)

	assertSigned := func(rawURL, expectedHost, expectedPath, expectedExpires string) {
		signedURL, err := b.SignAttachmentURL(ctx, rawURL)
		require.NoError(t, err)

		parsed, err := url.Parse(signedURL)
		require.NoError(t, err)
		assert.Equal(t, expectedHost, parsed.Host)
		assert.Equal(t, expectedPath, parsed.Path)
		assert.Equal(t, expectedExpires, parsed.Query().Get("X-Amz-Expires"))
		assert.NotEmpty(t, parsed.Query().Get("X-Amz-Signature"))
		assert.Contains(t, parsed.Query().Get("X-Amz-Credential"), "AKIAFAKEKEY/")
		assert.Contains(t, parsed.Query().Get("X-Amz-Credential"), "/sa-east-1/s3/")
	}

	// our own media bucket
	assertSigned(
		"https://courier-media.s3.sa-east-1.amazonaws.com/media/1/photo.jpg",
		"courier-media.s3.sa-east-1.amazonaws.com", "/media/1/photo.jpg", "604800",
	)

	// extra bucket, with a stale signature from a previously persisted presigned URL
	assertSigned(
		"https://flows-static.s3.sa-east-1.amazonaws.com/attachments/doc.pdf?X-Amz-Signature=stale&X-Amz-Expires=60",
		"flows-static.s3.sa-east-1.amazonaws.com", "/attachments/doc.pdf", "604800",
	)

	// path-style URL of an extra bucket
	assertSigned(
		"https://s3.sa-east-1.amazonaws.com/mailroom-attachments/media/audio.mp3",
		"mailroom-attachments.s3.sa-east-1.amazonaws.com", "/media/audio.mp3", "604800",
	)

	// buckets we don't sign for and non S3 URLs are left unchanged
	for _, rawURL := range []string{
		"https://other-bucket.s3.sa-east-1.amazonaws.com/media/photo.jpg",
		"https://example.com/media/photo.jpg",
	} {
		signedURL, err := b.SignAttachmentURL(ctx, rawURL)
		assert.NoError(t, err)
		assert.Equal(t, rawURL, signedURL)
	}

	// expiration comes from config
	b = newSigningTestBackend(t, 24)
	assertSigned(
		"https://courier-media.s3.sa-east-1.amazonaws.com/media/1/photo.jpg",
		"courier-media.s3.sa-east-1.amazonaws.com", "/media/1/photo.jpg", "86400",
	)
}

func TestSignAttachmentURLWithoutSigner(t *testing.T) {
	b := &backend{config: courier.NewConfig()}

	rawURL := "https://courier-media.s3.sa-east-1.amazonaws.com/media/1/photo.jpg"
	signedURL, err := b.SignAttachmentURL(context.Background(), rawURL)
	assert.NoError(t, err)
	assert.Equal(t, rawURL, signedURL)
}

func TestNewAttachmentURLSignerValidatesExpiration(t *testing.T) {
	for _, hours := range []int{0, -1, 169} {
		config := courier.NewConfig()
		config.S3PresignedURLExpiration = hours

		_, err := newAttachmentURLSigner(config, &storage.S3Options{AWSAccessKeyID: "key", AWSSecretAccessKey: "secret", Region: "sa-east-1"})
		assert.EqualError(t, err, fmt.Sprintf("S3PresignedURLExpiration must be between 1 and 168 hours, got %d", hours))
	}

	for _, hours := range []int{1, 168} {
		config := courier.NewConfig()
		config.S3PresignedURLExpiration = hours

		_, err := newAttachmentURLSigner(config, &storage.S3Options{AWSAccessKeyID: "key", AWSSecretAccessKey: "secret", Region: "sa-east-1"})
		assert.NoError(t, err)
	}
}
