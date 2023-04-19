package publisher

import (
	"sync/atomic"
	"time"

	"github.com/matrix-org/waterfall/pkg/worker"
)

type Status int

const (
	StatusStalled Status = iota + 1
	StatusRecovered
)

// `statusObserver` is a helper that observes the status of the publisher.
// Essentially it's a simple worker that expects to be informed about new packet
// arrivals. If no packets are received for N seconds, the worker will report the
// stalled status over the `statusCh` channel. Likewise, it'll update the status to
// recovered once a new packet is received.
type statusObserver struct {
	worker   *worker.Worker[struct{}]
	statusCh chan Status
	stalled  atomic.Bool
}

func newStatusObserver(timeout time.Duration) *statusObserver {
	statusCh := make(chan Status, 1)
	stalled := atomic.Bool{}

	worker := worker.StartWorker(worker.Config[struct{}]{
		ChannelSize: 1,
		Timeout:     timeout,
		OnTimeout: func() {
			if stalled.CompareAndSwap(false, true) {
				statusCh <- StatusStalled
			}
		},
		OnTask: func(struct{}) {
			if stalled.CompareAndSwap(true, false) {
				statusCh <- StatusRecovered
			}
		},
	})

	return &statusObserver{worker, statusCh, stalled}
}

func (o *statusObserver) packetArrived() {
	o.worker.Send(struct{}{})
}

func (o *statusObserver) stop() {
	o.worker.Stop()
	close(o.statusCh)
}
