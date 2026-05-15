//go:build test

package notify

import "time"

// WithRequestTimeout returns a copy of opts with the per-request context
// deadline overridden. Intended for use in tests only.
func WithRequestTimeout(opts SlackHandlerOptions, d time.Duration) SlackHandlerOptions {
	opts2 := opts
	opts2.testReqTimeout = d
	return opts2
}
