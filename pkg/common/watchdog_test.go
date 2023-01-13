package common_test

import (
	"testing"
	"time"

	"github.com/matrix-org/waterfall/pkg/common"
)

func BenchmarkWatchdogChannel_Notify(b *testing.B) {
	watchdogConfig := common.WatchdogConfig{
		Timeout:   2 * time.Second,
		OnTimeout: func() {},
	}
	w := common.StartWatchdog(watchdogConfig)

	// run the Fib function b.N times
	for n := 0; n < b.N; n++ {
		w.Notify()
	}
	w.Close()
}
