package metacommons

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
)

var (
	SignatureHeader = "X-Hub-Signature"
)

func FBCalculateSignature(appSecret string, body []byte) (string, error) {
	var buffer bytes.Buffer
	buffer.Write(body)

	// hash with SHA1
	mac := hmac.New(sha1.New, []byte(appSecret))
	mac.Write(buffer.Bytes())

	return hex.EncodeToString(mac.Sum(nil)), nil
}
