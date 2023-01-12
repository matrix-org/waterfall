package common_test

import (
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/stretchr/testify/assert"
)

func testSetup(t *testing.T) *common.Watchdog {
	t.Helper()
	w := common.NewWatchdog(2*time.Second, func() {})

	t.Cleanup(func() {
		w.Close()
	})
	return w
}

func TestWatchdog_Start(t *testing.T) {
	w := testSetup(t)
	terminated := w.Start()
	select {
	case <-terminated:
		t.Fatal("Should terminated only after call Close")
	default:
		assert.True(t, true)
	}
}

func TestWatchdog_Close(t *testing.T) {
	w := testSetup(t)
	var terminated chan struct{}
	terminated = w.Start()

	select {
	case <-terminated:
		t.Fatal("Should terminated after call Close")
	default:
	}

	w.Close()
	assert.Empty(t, <-terminated)
}

func TestWatchdog_Notify(t *testing.T) {
	w := testSetup(t)
	w.Start()
	// Take care: Notify is blocking and this lines running sequential.
	// The previous implementation was blocking too. I'll leave it like this
	assert.True(t, w.Notify())
	assert.True(t, w.Notify())
	assert.True(t, w.Notify())

	w.Close()
	assert.False(t, w.Notify())
	assert.False(t, w.Notify())

	w.Close()
	assert.False(t, w.Notify())
	assert.False(t, w.Notify())
}

func TestWatchdog_Close_before_Start(t *testing.T) {
	// You can close the watchdog before starting it. I don't know if this is a feature ;-)
	w := testSetup(t)
	w.Close()
	assert.Empty(t, <-w.Start())
}

func TestWatchdog_Multiple_Start(t *testing.T) {
	// You can start Watchdog several times.
	// I think this is a bug, but I didn't want to change the interface.
	// I would prefer make start private and start the watchdog immediately upon creation to prevent this.
	w := testSetup(t)
	t1 := w.Start()
	t2 := w.Start()
	w.Close()
	assert.Empty(t, <-t1)
	assert.Empty(t, <-t2)
}

func TestWatchdog_Multithreading(t *testing.T) {
	w := testSetup(t)
	runtime.Gosched()
	w.Start()
	var wg sync.WaitGroup
	loopA := make(chan struct{})
	go func() {
		defer close(loopA)
		for i := 1; i < 1000; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				w.Notify()
			}()
		}
	}()
	loopB := make(chan struct{})
	go func() {
		defer close(loopB)
		for i := 1; i < 1000; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				w.Notify()
			}()
		}
	}()
	<-loopA
	w.Close()
	<-loopB
	finish := make(chan struct{})
	go func() {
		defer close(finish)
		wg.Wait()
	}()

	select {
	case <-finish:
		assert.True(t, true)
	case <-time.After(100 * time.Second):
		t.Fatal("Should finish in time")
	}
}
