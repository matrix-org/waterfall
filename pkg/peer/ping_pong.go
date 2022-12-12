package peer

import "time"

type Pong struct{}

type PingPongConfig struct {
	Interval    time.Duration
	Deadline    time.Duration
	PongChannel chan Pong
	SendPing    func()
	OnDeadLine  func()
}

// Starts a goroutine that will execute `onDeadLine` closure in case nothing has been published
// to the `heartBeat` channel for `deadline` duration. The goroutine stops once the channel is closed.
func startPingPong(config PingPongConfig) {
	go func() {
		for range time.Tick(config.Interval) {
			config.SendPing()

			select {
			case <-time.After(config.Deadline):
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
