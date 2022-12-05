package peer

import "time"

type HeartBeat struct{}

// Starts a goroutine that will execute `onDeadLine` closure in case nothing has been published
// to the `heartBeat` channel for `deadline` duration. The goroutine stops once the channel is closed.
func startKeepAlive(deadline time.Duration, heartBeat <-chan HeartBeat, onDeadLine func()) {
	go func() {
		for {
			select {
			case <-time.After(deadline):
				onDeadLine()
				return
			case _, ok := <-heartBeat:
				if !ok {
					return
				}
			}
		}
	}()
}
