package utils

import (
	"net/url"
	"path"
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

	u.RawPath = ""
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
