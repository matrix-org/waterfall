package conference

import (
	"time"

	"github.com/matrix-org/waterfall/pkg/signaling"
	"github.com/matrix-org/waterfall/pkg/worker"
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix/id"
)

type matrixWorker struct {
	worker   *worker.Worker[signaling.MatrixMessage]
	deviceID id.DeviceID
}

func newMatrixWorker(handler signaling.MatrixSignaler) *matrixWorker {
	workerConfig := worker.Config[signaling.MatrixMessage]{
		ChannelSize: 128,
		Timeout:     time.Hour,
		OnTimeout:   func() {},
		OnTask:      func(msg signaling.MatrixMessage) { handler.SendMessage(msg) },
	}

	matrixWorker := &matrixWorker{
		worker:   worker.StartWorker(workerConfig),
		deviceID: handler.DeviceID(),
	}

	return matrixWorker
}

func (w *matrixWorker) stop() {
	w.worker.Stop()
}

func (w *matrixWorker) sendSignalingMessage(recipient signaling.MatrixRecipient, content interface{}) {
	msg := signaling.MatrixMessage{
		Recipient: recipient,
		Message:   content,
	}

	if err := w.worker.Send(msg); err != nil {
		logrus.Errorf("Really bad, dropping matrix message since the matrix queue is full! Home server down? %s", err)
	}
}
