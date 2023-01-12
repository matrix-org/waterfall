package common

import (
	"sync"
	"time"
)

// Configuration for the watchdog.
type WatchdogConfig struct {
	// Timeout after which `OnTimeout` is called.
	Timeout time.Duration
	// A closure that is called once `Timeout` is reached.
	OnTimeout func()
}

// We need to wrap the channel in a struct so that we can close it from the outside and
// check by the sender if the channel is closed (there is no elegant way to do it in Go).
type WatchdogChannel struct {
	channel chan<- struct{}
	mutex   sync.Mutex
	closed  bool
}

// Close the channel unless already closed.
func (c *WatchdogChannel) Close() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if !c.closed {
		close(c.channel)
		c.closed = true
	}
}

// Notify the watchdog that a packet has been received. Return `true` if the notification
// has been sent, `false` if the channel is already closed.
func (c *WatchdogChannel) Notify() bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if !c.closed {
		c.channel <- struct{}{}
		return true
	}

	return false
}

// Starts a watchdog that periodically (specified by the configuration) executes a `c.OnTimeout` closure if
// no messages have been received on a channel for a `c.Timeout`.
func StartWatchdog(c WatchdogConfig) *WatchdogChannel {
	// The channel that will be used to inform the watchdog about the reception of a packet.
	// The watchdog will be stopped once the channel is closed.
	incoming := make(chan struct{}, UnboundedChannelSize)

	go func() {
		for {
			select {
			case _, ok := <-incoming:
				if !ok {
					return
				}
			case <-time.After(c.Timeout):
				c.OnTimeout()
			}
		}
	}()

	return &WatchdogChannel{incoming, sync.Mutex{}, false}
}

type Watchdog struct {
	// Timeout after which `OnTimeout` is called.
	timeout time.Duration
	// A closure that is called once `Timeout` is reached.
	onTimeout   func()
	notify      chan struct{}
	closeSignal chan struct{}
}

func NewWatchdog(timeout time.Duration, onTimeout func()) *Watchdog {
	return &Watchdog{timeout: timeout, onTimeout: onTimeout, notify: make(chan struct{}), closeSignal: make(chan struct{})}
}

func (w Watchdog) Start() chan struct{} {
	terminate := make(chan struct{})

	go func() {
		for {
			select {
			case <-w.notify:
				// do nothing
			case <-w.closeSignal:
				// clean up notifications after terminate so sender are not blocking endless
				go func() {
					for {
						select {
						case <-w.notify:
						default:
							return
						}
					}
				}()
				close(terminate)
				return
			case <-time.After(w.timeout):
				w.onTimeout()
			}
		}
	}()

	return terminate
}

func (w Watchdog) Close() {
	select {
	case <-w.closeSignal:
		return
	default:
		close(w.closeSignal)
	}
}

func (w Watchdog) Notify() bool {
	select {
	case <-w.closeSignal:
		return false
	default:
		w.notify <- struct{}{}
		return true
	}
}