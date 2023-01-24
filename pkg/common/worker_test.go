package common_test

import (
	"testing"
	"time"

	"github.com/matrix-org/waterfall/pkg/common"
)

func BenchmarkWorker(b *testing.B) {
	workerConfig := common.WorkerConfig[struct{}]{
		Timeout:   2 * time.Second,
		OnTimeout: func() {},
		OnTask:    func(struct{}) {},
	}
	w := common.StartWorker(workerConfig)

	for n := 0; n < b.N; n++ {
		w.Send(struct{}{})
	}

	w.Stop()
}
