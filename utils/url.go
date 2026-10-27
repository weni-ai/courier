package utils

import (
	"net/url"
	"path"
	"strings"
)

func AddURLPath(urlStr string, paths ...string) (string, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return "", err
	}
	allPaths := []string{u.Path}
	allPaths = append(allPaths, paths...)
	p, err := url.Parse(path.Join(allPaths...))
	if err != nil {
		return "", err
	}
	return u.ResolveReference(p).String(), nil
}

// EncodeMediaURL converts an IRI-style media URL into an RFC 3986 URI so
// downstream downloaders (e.g. WhatsApp Cloud) can fetch it. Non-ASCII path
// and query characters are percent-encoded; already-encoded sequences are
// left intact (no double-encoding). Empty input is returned unchanged.
func EncodeMediaURL(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}

	// Parse decodes %2F into u.Path as '/', so encoding from Path would
	// turn a single segment into two. Re-encode the original raw path
	// per segment to keep that distinction while still escaping IRIs.
	pathToEncode := u.RawPath
	if pathToEncode == "" {
		pathToEncode = u.Path
	}
	u.RawPath = encodePath(pathToEncode)
	u.RawFragment = ""

	if u.RawQuery != "" {
		query, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return "", err
		}
		u.RawQuery = query.Encode()
	}

	return u.String(), nil
}

// StripPresignParams removes the X-Amz-* query parameters of an S3 presigned URL, keeping any other
// parameter in its original order and encoding, so the result is stable across signatures.
func StripPresignParams(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.RawQuery == "" {
		return rawURL
	}

	params := strings.Split(u.RawQuery, "&")
	kept := make([]string, 0, len(params))
	for _, param := range params {
		if !isPresignParam(param) {
			kept = append(kept, param)
		}
	}
	if len(kept) == len(params) {
		return rawURL
	}

	u.RawQuery = strings.Join(kept, "&")
	return u.String()
}

func isPresignParam(param string) bool {
	key, _, _ := strings.Cut(param, "=")
	if unescaped, err := url.QueryUnescape(key); err == nil {
		key = unescaped
	}
	return strings.HasPrefix(strings.ToLower(key), "x-amz-")
}

func encodePath(p string) string {
	segments := strings.Split(p, "/")
	for i, s := range segments {
		decoded, err := url.PathUnescape(s)
		if err != nil {
			decoded = s
		}
		segments[i] = url.PathEscape(decoded)
	}
	return strings.Join(segments, "/")
}
