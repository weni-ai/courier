package facebookapp

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWACUploadContentType_XML(t *testing.T) {
	nfe := []byte(`<?xml version="1.0"?><nfeProc xmlns="http://www.portalfiscal.inf.br/nfe"><NFe></NFe></nfeProc>`)

	// Meta /media accepts text/plain for generic documents, not text/xml or application/xml.
	assert.Equal(t, "text/plain", wacUploadContentType("text/plain", "nota.xml", nfe))
	assert.Equal(t, "text/plain", wacUploadContentType("application/xml", "nota.xml", nfe))
	assert.Equal(t, "text/plain", wacUploadContentType("text/xml", "nota.xml", nfe))
}

func TestWACPayloadMediaType_XML(t *testing.T) {
	waType, _ := wacPayloadMediaType("text/plain", "https://s3.example.com/org/uuid.xml")
	assert.Equal(t, "document", waType)

	waType, _ = wacPayloadMediaType("text/plain", "https://s3.example.com/org/uuid")
	assert.Equal(t, "document", waType)

	waType, _ = wacPayloadMediaType("text/xml", "https://s3.example.com/org/uuid")
	assert.Equal(t, "document", waType)

	waType, _ = wacPayloadMediaType("application/pdf", "https://s3.example.com/doc.pdf")
	assert.Equal(t, "document", waType)
}
