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
	if isXMLMedia(declaredMIME, fileName, body) {
		return metaWACDocumentUploadMIME
	}

	declaredMIME = strings.TrimSpace(declaredMIME)
	if base, _, err := mime.ParseMediaType(declaredMIME); err == nil {
		declaredMIME = base
	}

	fileType := http.DetectContentType(body)
	if base, _, err := mime.ParseMediaType(fileType); err == nil {
		fileType = base
	}

	if fileType != declaredMIME || fileType == "application/octet-stream" || fileType == "application/zip" {
		detected := mimetype.Detect(body).String()
		if base, _, err := mime.ParseMediaType(detected); err == nil {
			fileType = base
		} else {
			fileType = detected
		}
	}

	if isXMLMedia(fileType, fileName, body) {
		return metaWACDocumentUploadMIME
	}

	if declaredMIME != "" && declaredMIME != "text" && declaredMIME != "application" {
		return declaredMIME
	}

	return fileType
}

// isXMLMedia reports whether the attachment should be treated as XML for routing (document message).
func isXMLMedia(declaredMIME, fileName string, body []byte) bool {
	declaredMIME = strings.TrimSpace(declaredMIME)
	if base, _, err := mime.ParseMediaType(declaredMIME); err == nil {
		declaredMIME = base
	}
	switch declaredMIME {
	case "application/xml", "text/xml":
		return true
	}
	if isXMLFileName(fileName) || looksLikeXML(body) {
		return true
	}
	return false
}

// wacPayloadMediaType maps a declared attachment MIME and media URL to a WhatsApp Cloud message type
// (image, video, audio, document, sticker, or text).
func wacPayloadMediaType(declaredMIME, mediaURL string) (waType string, format string) {
	declaredMIME = strings.TrimSpace(declaredMIME)
	if base, _, err := mime.ParseMediaType(declaredMIME); err == nil {
		declaredMIME = base
	}

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
