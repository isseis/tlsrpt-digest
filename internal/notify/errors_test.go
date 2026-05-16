package notify_test

import (
	"errors"
	"testing"

	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebhookValidationError_AsType(t *testing.T) {
	err := &notify.WebhookValidationError{Msg: "test"}
	target, ok := errors.AsType[*notify.WebhookValidationError](err)
	require.True(t, ok)
	assert.Equal(t, "test", target.Msg)
}

func TestSlackServerError_AsType(t *testing.T) {
	cause := errors.New("connection refused")
	err := &notify.SlackServerError{StatusCode: 503, Cause: cause}
	target, ok := errors.AsType[*notify.SlackServerError](err)
	require.True(t, ok)
	assert.Equal(t, 503, target.StatusCode)
	assert.ErrorIs(t, err, cause)
}

func TestSlackClientError_AsType(t *testing.T) {
	err := &notify.SlackClientError{StatusCode: 400}
	target, ok := errors.AsType[*notify.SlackClientError](err)
	require.True(t, ok)
	assert.Equal(t, 400, target.StatusCode)
}
