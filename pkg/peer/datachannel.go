package peer

import (
	"time"

	"github.com/matrix-org/waterfall/pkg/peer/state"
	"github.com/matrix-org/waterfall/pkg/worker"
	"github.com/sirupsen/logrus"
)

func newDataChannelWorker(state *state.PeerState, logger *logrus.Entry) *worker.Worker[string] {
	// Create the configuration for the data channel worker.
	workerConfig := worker.Config[string]{
		ChannelSize: 32,
		Timeout:     time.Hour,
		OnTimeout:   func() {},
		OnTask: func(json string) {
			ch := state.GetDataChannel()
			if ch == nil {
				logger.Warn("dropping the message, channel not available")
				return
			}

			if err := ch.SendText(json); err != nil {
				logger.WithError(err).Error("failed to send data channel message")
				return
			}
		},
	}

	// Create the worker.
	return worker.StartWorker(workerConfig)
}
