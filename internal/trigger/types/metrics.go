package types

import "time"

// Metrics defines the interface for trigger engine telemetry.
type Metrics interface {
	// Publisher metrics
	IncPublishSuccess(tenant, collection string, subjectHashed bool)
	IncPublishFailure(tenant, collection string, reason string)
	ObservePublishLatency(tenant, collection string, duration time.Duration)

	// Consumer metrics
	IncConsumeSuccess(tenant, collection string, subjectHashed bool)
	IncConsumeFailure(tenant, collection string, reason string)
	ObserveConsumeLatency(tenant, collection string, duration time.Duration)
	IncHashCollision(tenant, collection string)

	// Worker metrics
	IncDeliverySuccess(tenant, collection string)
	IncDeliveryFailure(tenant, collection string, status int, fatal bool)
	IncDeliveryRetry(tenant, collection string)
	ObserveDeliveryLatency(tenant, collection string, duration time.Duration)
}

// NoopMetrics is a no-op implementation of Metrics.
type NoopMetrics struct{}

func (m *NoopMetrics) IncPublishSuccess(tenant, collection string, subjectHashed bool) {
	_ = tenant
}
func (m *NoopMetrics) IncPublishFailure(tenant, collection string, reason string) {
	_ = tenant
}
func (m *NoopMetrics) ObservePublishLatency(tenant, collection string, duration time.Duration) {
	_ = tenant
}

func (m *NoopMetrics) IncConsumeSuccess(tenant, collection string, subjectHashed bool) {
	_ = tenant
}
func (m *NoopMetrics) IncConsumeFailure(tenant, collection string, reason string) {
	_ = tenant
}
func (m *NoopMetrics) ObserveConsumeLatency(tenant, collection string, duration time.Duration) {
	_ = tenant
}
func (m *NoopMetrics) IncHashCollision(tenant, collection string) {
	_ = tenant
}

func (m *NoopMetrics) IncDeliverySuccess(tenant, collection string) {
	_ = tenant
}
func (m *NoopMetrics) IncDeliveryFailure(tenant, collection string, status int, fatal bool) {
	_ = tenant
}
func (m *NoopMetrics) IncDeliveryRetry(tenant, collection string) {
	_ = tenant
}
func (m *NoopMetrics) ObserveDeliveryLatency(tenant, collection string, duration time.Duration) {
	_ = tenant
}
