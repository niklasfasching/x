package util

import (
	"fmt"
	"time"
)

func Retry[T any](f func() (T, error), maxRetries int, d time.Duration) (v T, err error) {
	for i := 0; i <= maxRetries; i++ {
		if v, err = f(); err == nil {
			return v, nil
		}
		time.Sleep(d)
	}
	return v, fmt.Errorf("max retries reached: %w", err)
}
