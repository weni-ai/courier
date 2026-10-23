package rapidpro

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/gocommon/storage"
)

// memStorage records every Put so tests can see when a later attachment
// overwrites an earlier one at the same path.
type memStorage struct {
	files map[string][]byte
}

func (m *memStorage) Name() string { return "mem" }

func (m *memStorage) Test(context.Context) error { return nil }

func (m *memStorage) Get(_ context.Context, path string) (string, []byte, error) {
	body, ok := m.files[path]
	if !ok {
		return "", nil, fmt.Errorf("not found: %s", path)
	}
	return "", append([]byte(nil), body...), nil
}

func (m *memStorage) Put(_ context.Context, path, _ string, contents []byte) (string, error) {
	if m.files == nil {
		m.files = map[string][]byte{}
	}
	m.files[path] = append([]byte(nil), contents...)
	return "https://media.test" + path, nil
}

func (m *memStorage) BatchPut(context.Context, []*storage.Upload) error { return nil }

func TestDownloadMediaToS3KeepsEveryAttachment(t *testing.T) {
	// Four attachments, alternating types: 1 and 3 are jpeg, 2 and 4 are pdf.
	// Each body carries a marker that must survive storage unchanged.
	markers := []string{"ATT-ONE", "ATT-TWO", "ATT-THREE", "ATT-FOUR"}
	paths := []string{"/one.jpg", "/two.pdf", "/three.jpg", "/four.pdf"}
	bodies := make([][]byte, len(markers))
	for i, marker := range markers {
		prefix := []byte("\xFF\xD8\xFF")
		if strings.HasSuffix(paths[i], ".pdf") {
			prefix = []byte("%PDF-1.4 ")
		}
		bodies[i] = append(prefix, []byte(strings.Repeat(marker+" ", 40))...)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i, path := range paths {
			if r.URL.Path == path {
				w.Write(bodies[i])
				return
			}
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	store := &memStorage{}
	b := &backend{
		config:  courier.NewConfig(),
		storage: store,
	}
	channel := courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "EM", "support@company.com", "US", nil)
	msgUUID := courier.NewMsgUUID()

	stored := make([]string, len(paths))
	for i, path := range paths {
		got, err := downloadMediaToS3(context.Background(), b, channel, 1, msgUUID, srv.URL+path)
		if err != nil {
			t.Fatalf("attachment %d: %v", i+1, err)
		}
		stored[i] = got
	}

	for i, marker := range markers {
		if !bytes.Contains(storedBody(t, store, stored[i]), []byte(marker)) {
			t.Errorf("attachment %d was overwritten; stored %q, want marker %s", i+1, stored[i], marker)
		}
	}
	if stored[0] == stored[2] {
		t.Errorf("attachment 1 and 3 share the same object: %s", stored[0])
	}
	if stored[1] == stored[3] {
		t.Errorf("attachment 2 and 4 share the same object: %s", stored[1])
	}
}

func storedBody(t *testing.T, store *memStorage, ref string) []byte {
	t.Helper()
	path := ref
	if i := strings.Index(ref, ":"); i >= 0 {
		path = ref[i+1:]
	}
	const host = "https://media.test"
	path = strings.TrimPrefix(path, host)
	for key, body := range store.files {
		if key == path || strings.HasSuffix(ref, key) {
			return body
		}
	}
	t.Fatalf("no stored body for %s", ref)
	return nil
}
