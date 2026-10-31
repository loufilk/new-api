package common

import "context"

type asyncImageCountLimitKey struct{}

// WithAsyncImageCountLimit carries the worker's already-reserved result
// capacity. It is deliberately an internal context value, not an HTTP header
// that callers could forge or that could be forwarded upstream.
func WithAsyncImageCountLimit(ctx context.Context, count int) context.Context {
	return context.WithValue(ctx, asyncImageCountLimitKey{}, count)
}

func AsyncImageCountLimit(ctx context.Context) (int, bool) {
	count, ok := ctx.Value(asyncImageCountLimitKey{}).(int)
	return count, ok
}
