package facebookapp

import (
	"bytes"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/gabriel-vasile/mimetype"
)

// wacUploadContentType picks the Content-Type for WhatsApp Cloud media upload.
// declaredMIME is the attachment prefix from RapidPro (e.g. text/plain, application/xml).
func wacUploadContentType(declaredMIME, fileName string, body []byte) string {
	declaredMIME = strings.TrimSpace(declaredMIME)
	if base, _, err := mime.ParseMediaType(declaredMIME); err == nil {
		declaredMIME = base
	}

	switch declaredMIME {
	case "application/xml", "text/xml":
		return declaredMIME
	}

	if declaredMIME == "text/plain" && isXMLFileName(fileName) {
		return "application/xml"
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

	if fileType == "text/plain" && (isXMLFileName(fileName) || looksLikeXML(body)) {
		return "application/xml"
	}

	if fileType == "text/xml" {
		return "application/xml"
	}

	if declaredMIME != "" && declaredMIME != "text" && declaredMIME != "application" {
		return declaredMIME
	}

	return fileType
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
	if len(b) == 0 {
		return false
	}
	if b[0] == '<' {
		return true
	}
	prefix := string(b)
	if len(prefix) > 256 {
		prefix = prefix[:256]
	}
	prefix = strings.TrimSpace(prefix)
	return strings.HasPrefix(prefix, "<?xml")
}
