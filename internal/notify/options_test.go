package notify_test

import (
	"testing"

	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSlackHandlerOptions_DryRun(t *testing.T) {
	h, err := notify.NewSlackHandler(notify.SlackHandlerOptions{
		RunID:     "run-opt-1",
		LevelMode: notify.LevelModeExactInfo,
		IsDryRun:  true,
	})
	require.NoError(t, err)
	assert.True(t, h.IsDryRun())
}

func TestBuildHandlers_DryRunNoURL_PropagatesIsDryRun(t *testing.T) {
	handlers, err := notify.BuildHandlers("", "", "", notify.SlackHandlerOptions{
		RunID:    "run-opt-2",
		IsDryRun: true,
	})
	require.NoError(t, err)
	require.Len(t, handlers, 2)

	for _, h := range handlers {
		assert.True(t, h.IsDryRun())
	}

	gotModes := []notify.LevelMode{handlers[0].LevelMode(), handlers[1].LevelMode()}
	assert.ElementsMatch(t, []notify.LevelMode{notify.LevelModeExactInfo, notify.LevelModeWarnAndAbove}, gotModes)
}
