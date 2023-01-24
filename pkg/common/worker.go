package common

import (
	"sync"
	"time"
)

// Configuration for the worker.
type WorkerConfig struct {
	// Timeout after which `OnTimeout` is called.
	Timeout time.Duration
	// A closure that is called once `Timeout` is reached.
	OnTimeout func()
}

// We need to wrap the channel in a struct so that we can close it from the outside and
// check by the sender if the channel is closed (there is no elegant way to do it in Go).
type Worker struct {
	channel chan<- struct{}
	mutex   sync.Mutex
	closed  bool
}

// Stop the channel unless already closed.
func (c *Worker) Stop() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if !c.closed {
		close(c.channel)
		c.closed = true
	}
}

// Send a task to the worker. Returns `true` if the task
// has been sent, `false` if the channel is already closed.
func (c *Worker) Send() bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if !c.closed {
		c.channel <- struct{}{}
		return true
	}

	return false
}

// Starts a worker that periodically (specified by the configuration) executes a `c.OnTimeout` closure if
// no tasks have been received on a channel for a `c.Timeout`.
func StartWorker(c WorkerConfig) *Worker {
	// The channel that will be used to inform the worker about the reception of a task.
	// The worker will be stopped once the channel is closed.
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

	return &Worker{incoming, sync.Mutex{}, false}
}
