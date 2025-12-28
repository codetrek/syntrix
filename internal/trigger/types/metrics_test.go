package types

import (
	"testing"
	"time"
)

func TestNoopMetrics(t *testing.T) {
	m := &NoopMetrics{}

	// Call all methods to ensure coverage
	m.IncPublishSuccess("tenant", "collection", true)
	m.IncPublishFailure("tenant", "collection", "reason")
	m.ObservePublishLatency("tenant", "collection", time.Second)

	m.IncConsumeSuccess("tenant", "collection", true)
	m.IncConsumeFailure("tenant", "collection", "reason")
	m.ObserveConsumeLatency("tenant", "collection", time.Second)
	m.IncHashCollision("tenant", "collection")

	m.IncDeliverySuccess("tenant", "collection")
	m.IncDeliveryFailure("tenant", "collection", 500, true)
	m.IncDeliveryRetry("tenant", "collection")
	m.ObserveDeliveryLatency("tenant", "collection", time.Second)
}
