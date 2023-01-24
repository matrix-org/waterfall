package common

import (
	"errors"
	"sync"
	"time"
)

// Errors that may occur when sending tasks to a worker.
var (
	ErrWorkerClosed  = errors.New("worker is closed")
	ErrWorkerTooBusy = errors.New("worker is already overloaded")
)

// Configuration for the worker.
type WorkerConfig[T any] struct {
	// The size of the bounded channel.
	ChannelSize int
	// Timeout after which `OnTimeout` is called.
	Timeout time.Duration
	// A closure that is called once `Timeout` is reached.
	OnTimeout func()
	// A closure that is executed upon reception of a task.
	OnTask func(T)
}

// We need to wrap the channel in a struct so that we can close it from the outside and
// check by the sender if the channel is closed (there is no elegant way to do it in Go).
type Worker[T any] struct {
	channel chan<- T
	mutex   sync.Mutex
	closed  bool
}

// Stop the channel unless already closed.
func (c *Worker[T]) Stop() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if !c.closed {
		close(c.channel)
		c.closed = true
	}
}

// Send a task to the worker. Returns `true` if the task
// has been sent, `false` if the channel is already closed.
func (c *Worker[T]) Send(task T) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// If the channel is not closed, check if we can send the task.
	if !c.closed {
		// We don't want to block here since it's the whole point of this
		// component (that the CPU bound tasks are handled by the worker).
		select {
		case c.channel <- task:
			return nil
		default:
			return ErrWorkerTooBusy
		}
	}

	// Otherwise, the channel is closed.
	return ErrWorkerClosed
}

// Starts a worker that periodically (specified by the configuration) executes a `c.OnTimeout` closure if
// no tasks have been received on a channel for a `c.Timeout`. The worker will stop once the channel is closed,
// i.e. once the user calls `Stop` explicitly.
func StartWorker[T any](c WorkerConfig[T]) *Worker[T] {
	// The channel that will be used to inform the worker about the reception of a task.
	// The worker will be stopped once the channel is closed.
	incoming := make(chan T, c.ChannelSize)

	go func() {
		for {
			select {
			case task, ok := <-incoming:
				if !ok {
					return
				}
				c.OnTask(task)
			case <-time.After(c.Timeout):
				c.OnTimeout()
			}
		}
	}()

	return &Worker[T]{incoming, sync.Mutex{}, false}
}
