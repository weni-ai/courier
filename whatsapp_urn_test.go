package courier

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsWhatsAppBSUIDPath(t *testing.T) {
	require.True(t, IsWhatsAppBSUIDPath("US.13491208655302741918"))
	require.False(t, IsWhatsAppBSUIDPath("US.ENT.11815799212886844830"))
	require.False(t, IsWhatsAppBSUIDPath("5678"))
	require.False(t, IsWhatsAppBSUIDPath("5511987654321"))
	require.False(t, IsWhatsAppBSUIDPath(""))
}

func TestIsWhatsAppParentBSUIDPath(t *testing.T) {
	require.True(t, IsWhatsAppParentBSUIDPath("US.ENT.11815799212886844830"))
	require.False(t, IsWhatsAppParentBSUIDPath("US.13491208655302741918"))
	require.False(t, IsWhatsAppParentBSUIDPath("5678"))
}

func TestSameWhatsAppBSUIDCategory(t *testing.T) {
	require.True(t, SameWhatsAppBSUIDCategory("US.13491208655302741918", "US.98765432100000000001"))
	require.True(t, SameWhatsAppBSUIDCategory("US.ENT.11815799212886844830", "US.ENT.99999999999999999999"))
	require.False(t, SameWhatsAppBSUIDCategory("US.13491208655302741918", "US.ENT.11815799212886844830"))
}
