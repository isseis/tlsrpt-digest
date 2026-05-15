package notify_test

import (
	"testing"

	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/stretchr/testify/assert"
)

func TestSlackHandlerOptions_DryRun(t *testing.T) {
	opts := notify.SlackHandlerOptions{IsDryRun: true}
	assert.True(t, opts.IsDryRun)

	opts2 := notify.SlackHandlerOptions{IsDryRun: false}
	assert.False(t, opts2.IsDryRun)
}
