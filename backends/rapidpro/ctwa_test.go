package rapidpro

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestShouldQueueConversationStarted(t *testing.T) {
	assert.True(t, shouldQueueConversationStarted(1))
	assert.False(t, shouldQueueConversationStarted(0))
	assert.False(t, shouldQueueConversationStarted(2))
}
