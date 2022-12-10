package peer

import "time"

type Pong struct{}

// Starts a goroutine that will execute `onDeadLine` closure in case nothing has been published
// to the `heartBeat` channel for `deadline` duration. The goroutine stops once the channel is closed.
func startKeepAlive(
	interval time.Duration,
	deadline time.Duration,
	pong <-chan Pong,
	sendPing func(),
	onDeadLine func(),
) {
	go func() {
		for range time.Tick(interval) {
			sendPing()

			select {
			case <-time.After(deadline):
				onDeadLine()
				return
			case _, ok := <-pong:
				if !ok {
					return
				}
			}
		}
	}()
}
