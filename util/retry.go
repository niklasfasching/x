package util

import (
	"context"
	"fmt"
	"time"
)

func Retry[T any](f func() (T, error), maxRetries int, d time.Duration) (v T, err error) {
	return RetryContext(context.Background(), func(context.Context) (T, error) { return f() }, maxRetries, d)
}

func RetryContext[T any](ctx context.Context, f func(context.Context) (T, error), maxRetries int, d time.Duration) (v T, err error) {
	for i := 0; i <= maxRetries; i++ {
		if v, err = f(ctx); err == nil {
			return v, nil
		}
		select {
		case <-ctx.Done():
			t := new(T)
			return *t, ctx.Err()
		case <-time.NewTimer(d).C:
			continue
		}
	}
	return v, fmt.Errorf("max retries reached: %w", err)
}
