package courier

import (
	"regexp"
	"strings"
)

var whatsAppPhonePathPattern = regexp.MustCompile(`^[0-9]+$`)

// IsWhatsAppParentBSUIDPath reports whether path is a parent BSUID (e.g. US.ENT.xxx).
func IsWhatsAppParentBSUIDPath(path string) bool {
	return strings.HasPrefix(path, "US.ENT.")
}

// IsWhatsAppBSUIDPath reports whether path is a regular WhatsApp BSUID (e.g. US.xxx),
// excluding phone numbers and parent BSUIDs.
func IsWhatsAppBSUIDPath(path string) bool {
	return path != "" && !whatsAppPhonePathPattern.MatchString(path) && !IsWhatsAppParentBSUIDPath(path)
}

// IsWhatsAppBSUIDOrParentPath reports whether path is any non-phone WhatsApp identifier
// (regular BSUID or parent BSUID).
func IsWhatsAppBSUIDOrParentPath(path string) bool {
	return IsWhatsAppBSUIDPath(path) || IsWhatsAppParentBSUIDPath(path)
}

// SameWhatsAppBSUIDCategory reports whether two paths belong to the same BSUID category
// (regular BSUID vs parent BSUID). Phone numbers are not considered a BSUID category.
func SameWhatsAppBSUIDCategory(pathA, pathB string) bool {
	return IsWhatsAppParentBSUIDPath(pathA) == IsWhatsAppParentBSUIDPath(pathB)
}
