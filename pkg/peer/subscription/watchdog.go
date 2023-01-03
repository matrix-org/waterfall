package subscription

import (
	"time"

	"github.com/matrix-org/waterfall/pkg/common"
)

type WatchdogConfig struct {
	// Timeout after which `OnTimeout` is called.
	Timeout time.Duration
	// A closure that is called once `Timeout` is reached.
	OnTimeout func()
}

// Starts a watchdog that periodically (specified by the configuration) prints a warning in case
// no RTP packets have been received for a while. Each time the RTP packet is received, watchdog is
// informed by a message on a channel. The watchdog is stopped when the channel is closed.
func (c *WatchdogConfig) Start() chan<- struct{} {
	// The channel that will be used to inform the watchdog about the reception of a packet.
	// The watchdog will be stopped once the channel is closed.
	incoming := make(chan struct{}, common.UnboundedChannelSize)

	go func() {
		ticker := time.NewTicker(c.Timeout)
		defer ticker.Stop()

		for range ticker.C {
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

	return incoming
}
