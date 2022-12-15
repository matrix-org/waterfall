package common

import (
	"time"
)

type Pong struct{}

// Heartbeat defines the configuration for a heartbeat.
type Heartbeat struct {
	// How often to send pings.
	Interval time.Duration
	// After which time to consider the communication stalled.
	Timeout time.Duration
	// A closure that is called when ping is to be sent.
	// Returns `false` if an attempt to send a ping failed.
	SendPing func() bool
	// A closure that is called once `Timeout` is reached.
	OnTimeout func()
}

// Starts a goroutine that will send ping messages (using `SendPing`) every `interval` and wait for a response
// on `PongChannel` for `Timeout`. If no response is received within `Timeout`, `OnTimeout` is called.
// The goroutine stops once the channel is closed or upon handling the `OnTimeout`. The returned channel
// is what the caller should use to inform about the reception of a pong.
func (h *Heartbeat) Start() chan<- Pong {
	pong := make(chan Pong, UnboundedChannelSize)

	go func() {
		ticker := time.NewTicker(h.Interval)
		defer ticker.Stop()

		for range ticker.C {
			if !h.sendWithRetry() {
				return
			}

			select {
			case <-time.After(h.Timeout):
				h.OnTimeout()
				return
			case _, ok := <-pong:
				if !ok {
					return
				}
			}
		}
	}()

	return pong
}

// Tries to send a ping message using `SendPing` and retry it if it fails.
// Returns `true` if the ping was sent successfully.
func (h *Heartbeat) sendWithRetry() bool {
	const retries = 3
	retryInterval := h.Timeout / retries

	for i := 0; i < retries; i++ {
		if !h.SendPing() {
			time.Sleep(retryInterval)
			continue
		}
		return true
	}

	return false
}
