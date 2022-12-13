package peer

import "time"

type Pong struct{}

type HeartbeatConfig struct {
	Interval    time.Duration
	Timeout     time.Duration
	PongChannel chan Pong
	SendPing    func()
	OnDeadLine  func()
}

// Starts a goroutine that will execute `onDeadLine` closure in case nothing has been published
// to the `heartBeat` channel for `deadline` duration. The goroutine stops once the channel is closed.
func startHeartbeat(config HeartbeatConfig) {
	go func() {
		for range time.Tick(config.Interval) {
			config.SendPing()

			select {
			case <-time.After(config.Timeout):
				config.OnDeadLine()
				return
			case _, ok := <-config.PongChannel:
				if !ok {
					return
				}
			}
		}
	}()
}
