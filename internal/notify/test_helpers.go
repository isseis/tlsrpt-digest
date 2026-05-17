//go:build test

package notify

import (
	"context"
	"time"
)

// WithRequestTimeout returns a copy of opts with the per-request context
// deadline overridden. Intended for use in tests only.
func WithRequestTimeout(opts SlackHandlerOptions, d time.Duration) SlackHandlerOptions {
	opts2 := opts
	opts2.testReqTimeout = d
	return opts2
}

// WithNoOpSleep returns a copy of opts with a no-op sleep function injected.
// Use this in tests to avoid real timer delays while keeping realistic
// BackoffConfig values (e.g. Base: 2*time.Second).
func WithNoOpSleep(opts SlackHandlerOptions) SlackHandlerOptions {
	opts2 := opts
	opts2.testSleepFunc = func(_ context.Context, _ time.Duration) error { return nil }
	return opts2
}

// WithSleepFunc returns a copy of opts with a custom sleep function injected.
// The function receives the context and the requested duration, allowing tests
// to record calls, advance fake clocks, or simulate context cancellation.
func WithSleepFunc(opts SlackHandlerOptions, fn func(ctx context.Context, d time.Duration) error) SlackHandlerOptions {
	opts2 := opts
	opts2.testSleepFunc = fn
	return opts2
}
