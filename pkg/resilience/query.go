package resilience

import (
	"context"
	"errors"
	"time"
)

const (
	queryTimeout  = 250 * time.Millisecond
	retryBackoff  = 100 * time.Millisecond
)

// WithQueryTimeout executes fn with a 250 ms deadline.
// On a single DeadlineExceeded it retries once after 100 ms backoff.
// All other errors are returned immediately without retry.
func WithQueryTimeout(ctx context.Context, fn func(ctx context.Context) error) error {
	attempt := func() error {
		qctx, cancel := context.WithTimeout(ctx, queryTimeout)
		defer cancel()
		return fn(qctx)
	}

	err := attempt()
	if err == nil {
		return nil
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	// One retry after backoff.
	select {
	case <-time.After(retryBackoff):
	case <-ctx.Done():
		return ctx.Err()
	}
	return attempt()
}
