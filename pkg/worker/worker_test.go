package worker_test

import (
	"testing"
	"time"

	"github.com/matrix-org/waterfall/pkg/worker"
)

func BenchmarkWorker(b *testing.B) {
	workerConfig := worker.Config[struct{}]{
		ChannelSize: 1,
		Timeout:     2 * time.Second,
		OnTimeout:   func() {},
		OnTask:      func(struct{}) {},
	}
	w := worker.StartWorker(workerConfig)

	for n := 0; n < b.N; n++ {
		w.Send(struct{}{})
	}

	w.Stop()
}
