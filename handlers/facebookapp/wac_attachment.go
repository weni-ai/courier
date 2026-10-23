package facebookapp

import (
	"bytes"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/gabriel-vasile/mimetype"
)

// metaWACDocumentUploadMIME is accepted by WhatsApp Cloud POST /media for generic documents
// (see Meta error #100 allowlist). text/xml and application/xml are not accepted on upload.
const metaWACDocumentUploadMIME = "text/plain"

// wacUploadContentType picks the Content-Type for WhatsApp Cloud media upload.
// declaredMIME is the attachment prefix from RapidPro (e.g. text/plain, application/xml).
func wacUploadContentType(declaredMIME, fileName string, body []byte) string {
	declaredMIME = normalizedMIME(declaredMIME)

	fileType := normalizedMIME(http.DetectContentType(body))

	if fileType != declaredMIME || fileType == "application/octet-stream" || fileType == "application/zip" {
		if detected := normalizedMIME(mimetype.Detect(body).String()); detected != "" {
			fileType = detected
		}
	}

	if isXMLForUpload(declaredMIME, fileType, fileName, body) {
		return metaWACDocumentUploadMIME
	}

	// Prefer the attachment's declared type when it is a full type/subtype (e.g. image/png).
	// Bare majors like "text" or "application" are ignored; use detected fileType instead.
	if strings.Contains(declaredMIME, "/") {
		return declaredMIME
	}

	return fileType
}

func normalizedMIME(raw string) string {
	raw = strings.TrimSpace(raw)
	if base, _, err := mime.ParseMediaType(raw); err == nil {
		return base
	}
	return raw
}

// isXMLForUpload reports XML after declared and detected MIME types are known (single pass).
func isXMLForUpload(declaredMIME, detectedMIME, fileName string, body []byte) bool {
	return isXMLMimeType(declaredMIME) ||
		isXMLMimeType(detectedMIME) ||
		isXMLFileName(fileName) ||
		looksLikeXML(body)
}

func isXMLMimeType(mimeType string) bool {
	m := normalizedMIME(mimeType)
	return m == "application/xml" || m == "text/xml"
}

// wacPayloadMediaType maps a declared attachment MIME and media URL to a WhatsApp Cloud message type
// (image, video, audio, document, sticker, or text).
func wacPayloadMediaType(declaredMIME, mediaURL string) (waType string, format string) {
	declaredMIME = normalizedMIME(declaredMIME)

	parts := strings.SplitN(declaredMIME, "/", 2)
	major := parts[0]
	format = ""
	if len(parts) > 1 {
		format = parts[1]
	}

	switch major {
	case "image", "video", "audio":
		return major, format
	case "application":
		return "document", format
	case "text":
		// Attachments are always sent as WhatsApp media messages. text/plain (and other
		// text/*) must be "document", not "text" — otherwise the /messages payload has
		// type "text" with no text body (Meta error #100).
		return "document", format
	default:
		if isXMLFileURL(mediaURL) {
			return "document", format
		}
		return major, format
	}
}

func isXMLFileName(name string) bool {
	return strings.EqualFold(path.Ext(name), ".xml")
}

func isXMLFileURL(mediaURL string) bool {
	if mediaURL == "" {
		return false
	}
	// Attachment URLs may include query strings (e.g. S3 presigned).
	if i := strings.Index(mediaURL, "?"); i >= 0 {
		mediaURL = mediaURL[:i]
	}
	return isXMLFileName(path.Base(mediaURL))
}

func looksLikeXML(body []byte) bool {
	b := bytes.TrimSpace(body)
	if len(b) < 5 {
		return false
	}
	if len(b) > 256 {
		b = b[:256]
	}
	return strings.HasPrefix(strings.ToLower(string(b)), "<?xml")
}
