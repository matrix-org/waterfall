package conference

import (
	"time"

	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/matrix-org/waterfall/pkg/signaling"
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix/id"
)

type matrixWorker struct {
	worker   *common.Worker[signaling.MatrixMessage]
	deviceID id.DeviceID
}

func newMatrixWorker(handler signaling.MatrixSignaler) *matrixWorker {
	workerConfig := common.WorkerConfig[signaling.MatrixMessage]{
		ChannelSize: 128,
		Timeout:     time.Hour,
		OnTimeout:   func() {},
		OnTask:      func(msg signaling.MatrixMessage) { handler.SendMessage(msg) },
	}

	matrixWorker := &matrixWorker{
		worker:   common.StartWorker(workerConfig),
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
