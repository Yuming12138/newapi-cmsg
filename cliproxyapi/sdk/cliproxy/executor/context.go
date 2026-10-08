package executor

import "context"

type downstreamWebsocketContextKey struct{}
type rateLimitFailoverContextKey struct{}

// WithDownstreamWebsocket marks the current request as coming from a downstream websocket connection.
func WithDownstreamWebsocket(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, downstreamWebsocketContextKey{}, true)
}

// DownstreamWebsocket reports whether the current request originates from a downstream websocket connection.
func DownstreamWebsocket(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	raw := ctx.Value(downstreamWebsocketContextKey{})
	enabled, ok := raw.(bool)
	return ok && enabled
}

// WithRateLimitFailover signals that the manager can try another credential.
// Executors should return 429 errors without retrying the same credential.
func WithRateLimitFailover(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, rateLimitFailoverContextKey{}, true)
}

// RateLimitFailoverEnabled reports whether credential failover is available.
func RateLimitFailoverEnabled(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	enabled, _ := ctx.Value(rateLimitFailoverContextKey{}).(bool)
	return enabled
}
