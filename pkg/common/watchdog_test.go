package common

import (
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func testSetup(t *testing.T) *Watchdog {
	t.Helper()
	w := NewWatchdog(2*time.Second, func() {})

	t.Cleanup(func() {
		w.Close()
	})
	return w
}

func TestStartWatchdog(t *testing.T) {
	w := testSetup(t)
	terminated := w.Start()
	select {
	case <-terminated:
		t.Fatal("Should terminated only after call Close")
	default:
		assert.True(t, true)
	}

}

func TestCloseWatchdog(t *testing.T) {
	w := testSetup(t)
	var terminated chan struct{}
	terminated = w.Start()

	select {
	case <-terminated:
		t.Fatal("Should teminated after call Close")
	default:
	}

	w.Close()
	assert.Empty(t, <-terminated)
}
