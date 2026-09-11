package rapidpro

import (
	"context"
	"testing"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/gocommon/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockMediaStorage struct {
	putURL string
}

func (m *mockMediaStorage) Name() string { return "mock" }

func (m *mockMediaStorage) Test(ctx context.Context) error { return nil }

func (m *mockMediaStorage) Put(ctx context.Context, path, contentType string, contents []byte) (string, error) {
	return m.putURL, nil
}

func (m *mockMediaStorage) Get(ctx context.Context, path string) (string, []byte, error) {
	return "", nil, nil
}

func (m *mockMediaStorage) BatchPut(ctx context.Context, uploads []*storage.Upload) error {
	return nil
}

func TestPutMediaPresignUploadURLs(t *testing.T) {
	const s3URL = "https://courier-media.s3.us-east-1.amazonaws.com/media/1/abcd/efgh/abcdefgh12345678.png"

	t.Run("returns raw S3 URL when presign disabled", func(t *testing.T) {
		b := &backend{
			config:  &courier.Config{S3MediaPrefix: "/media/"},
			storage: &mockMediaStorage{putURL: s3URL},
		}

		url, err := b.PutMedia(context.Background(), &DBChannel{OrgID_: OrgID(1)}, "abcdefgh12345678.png", "image/png", []byte("png"))
		require.NoError(t, err)
		assert.Equal(t, s3URL, url)
	})

	t.Run("returns presigned URL when presign enabled", func(t *testing.T) {
		b := &backend{
			config: &courier.Config{
				S3MediaPrefix:            "/media/",
				S3PresignUploadURLs:      true,
				S3Region:                 "us-east-1",
				AWSAccessKeyID:           "test-key",
				AWSSecretAccessKey:       "test-secret",
				S3PresignedURLExpiration: 24,
			},
			storage: &mockMediaStorage{putURL: s3URL},
		}

		url, err := b.PutMedia(context.Background(), &DBChannel{OrgID_: OrgID(1)}, "abcdefgh12345678.png", "image/png", []byte("png"))
		require.NoError(t, err)
		assert.NotEqual(t, s3URL, url)
		assert.Contains(t, url, "X-Amz-Algorithm")
		assert.Contains(t, url, "courier-media")
	})
}
