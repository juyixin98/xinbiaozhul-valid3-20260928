package broker

import "sync/atomic"

// Metrics are the broker's observable counters, atomically updated. They let
// tests assert resource-limit and slow-subscriber behavior precisely.
type Metrics struct {
	ConnectsAccepted     atomic.Int64
	ConnectsRejected     atomic.Int64
	ConnectionsActive    atomic.Int64
	SubscriptionsCurrent atomic.Int64
	RetainedCurrent      atomic.Int64

	PublishReceivedQoS0 atomic.Int64
	PublishReceivedQoS1 atomic.Int64
	PubackReceived      atomic.Int64
	DeliveredQoS0       atomic.Int64
	DeliveredQoS1       atomic.Int64
	Retransmits         atomic.Int64
	InflightCurrent     atomic.Int64

	DroppedQueueFull atomic.Int64
	RejectedInflight atomic.Int64
	RejectedSubLimit atomic.Int64
	RejectedSessions atomic.Int64

	FramesInvalid     atomic.Int64
	FramesUnsupported atomic.Int64
	TakenOver         atomic.Int64
	WillPublished     atomic.Int64
}

// Snapshot returns a plain value copy suitable for logging/assertions.
type Snapshot struct {
	ConnectsAccepted     int64
	ConnectsRejected     int64
	ConnectionsActive    int64
	SubscriptionsCurrent int64
	RetainedCurrent      int64
	PublishReceivedQoS0  int64
	PublishReceivedQoS1  int64
	PubackReceived       int64
	DeliveredQoS0        int64
	DeliveredQoS1        int64
	Retransmits          int64
	InflightCurrent      int64
	DroppedQueueFull     int64
	RejectedInflight     int64
	RejectedSubLimit     int64
	RejectedSessions     int64
	FramesInvalid        int64
	FramesUnsupported    int64
	TakenOver            int64
	WillPublished        int64
}

func (m *Metrics) snapshot() Snapshot {
	return Snapshot{
		ConnectsAccepted:     m.ConnectsAccepted.Load(),
		ConnectsRejected:     m.ConnectsRejected.Load(),
		ConnectionsActive:    m.ConnectionsActive.Load(),
		SubscriptionsCurrent: m.SubscriptionsCurrent.Load(),
		RetainedCurrent:      m.RetainedCurrent.Load(),
		PublishReceivedQoS0:  m.PublishReceivedQoS0.Load(),
		PublishReceivedQoS1:  m.PublishReceivedQoS1.Load(),
		PubackReceived:       m.PubackReceived.Load(),
		DeliveredQoS0:        m.DeliveredQoS0.Load(),
		DeliveredQoS1:        m.DeliveredQoS1.Load(),
		Retransmits:          m.Retransmits.Load(),
		InflightCurrent:      m.InflightCurrent.Load(),
		DroppedQueueFull:     m.DroppedQueueFull.Load(),
		RejectedInflight:     m.RejectedInflight.Load(),
		RejectedSubLimit:     m.RejectedSubLimit.Load(),
		RejectedSessions:     m.RejectedSessions.Load(),
		FramesInvalid:        m.FramesInvalid.Load(),
		FramesUnsupported:    m.FramesUnsupported.Load(),
		TakenOver:            m.TakenOver.Load(),
		WillPublished:        m.WillPublished.Load(),
	}
}
