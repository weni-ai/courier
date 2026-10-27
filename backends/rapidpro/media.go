package rapidpro

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/metrics"
)

// PutMedia stores media contents and returns a public URL.
func (b *backend) PutMedia(ctx context.Context, channel courier.Channel, filename, contentType string, contents []byte) (string, error) {
	orgID := OrgID(0)
	if dbChannel, ok := channel.(*DBChannel); ok {
		orgID = dbChannel.OrgID()
	}

	if len(filename) < 8 {
		return "", fmt.Errorf("media filename must be at least 8 characters")
	}

	path := filepath.Join(b.config.S3MediaPrefix, strconv.FormatInt(int64(orgID), 10), filename[:4], filename[4:8], filename)
	if !strings.HasPrefix(path, "/") {
		path = fmt.Sprintf("/%s", path)
	}

	url, err := b.storage.Put(ctx, path, contentType, contents)
	if err != nil {
		return "", err
	}

	metrics.IncrementMediaUploadSize(len(contents))
	return url, nil
}
