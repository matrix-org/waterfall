package common_test

import (
	"testing"
	"time"

	"github.com/matrix-org/waterfall/pkg/common"
)

func BenchmarkWatchdogChannel_Notify(b *testing.B) {
	workerConfig := common.WorkerConfig{
		Timeout:   2 * time.Second,
		OnTimeout: func() {},
	}
	w := common.StartWorker(workerConfig)

	// Run the Send method b.N times.
	for n := 0; n < b.N; n++ {
		w.Send()
	}
	w.Stop()
}
