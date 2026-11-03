package service

import "context"

type mediaSettlementContextKey struct{}

// Client disconnects must not abandon an accepted media job. Durable workers
// keep their attempt deadline so processing finishes before the queue lease.
func mediaUpstreamContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx != nil {
		if worker, ok := ctx.Value(mediaSettlementContextKey{}).(bool); ok && worker {
			return context.WithCancel(ctx)
		}
	}
	return detachUpstreamContext(ctx)
}
