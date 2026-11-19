package courier

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewConfigWACThreadOwnerFlags(t *testing.T) {
	cfg := NewConfig()
	assert.False(t, cfg.EnableWACStandbyContext)
	assert.False(t, cfg.EnableWACSendOwnershipBlock)
}
