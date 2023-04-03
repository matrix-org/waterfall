package subscription

import (
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/matrix-org/waterfall/pkg/conference/subscription/rewriter"
	"github.com/matrix-org/waterfall/pkg/telemetry"
	"github.com/matrix-org/waterfall/pkg/webrtc_ext"
	"github.com/matrix-org/waterfall/pkg/worker"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
)

type VideoSubscription struct {
	rtpSender *webrtc.RTPSender

	info webrtc_ext.TrackInfo

	controller SubscriptionController
	worker     *worker.Worker[rtp.Packet]
	stopped    atomic.Bool

	logger    *logrus.Entry
	telemetry *telemetry.Telemetry
}

type KeyFrameRequest struct{}

// Creates a new video subscription. Returns a subscription along with a channel
// that informs the parent about key frame requests from the subscriptions. When the
// channel is closed, the subscription's go-routine is stopped.
func NewVideoSubscription(
	info webrtc_ext.TrackInfo,
	controller SubscriptionController,
	logger *logrus.Entry,
	telemetryBuilder *telemetry.ChildBuilder,
) (*VideoSubscription, <-chan KeyFrameRequest, error) {
	// Create a new track.
	rtpTrack, err := webrtc.NewTrackLocalStaticRTP(info.Codec, info.TrackID, info.StreamID)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to create track: %v", err)
	}

	rtpSender, err := controller.AddTrack(rtpTrack)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to add track: %v", err)
	}

	// Create a subscription.
	subscription := &VideoSubscription{
		rtpSender,
		info,
		controller,
		nil,
		atomic.Bool{},
		logger,
		telemetryBuilder.Create("VideoSubscription"),
	}

	// Create a worker state.
	workerState := workerState{
		packetRewriter: rewriter.NewPacketRewriter(),
		rtpTrack:       rtpTrack,
	}

	// Configure the worker for the subscription.
	workerConfig := worker.Config[rtp.Packet]{
		ChannelSize: 16, // We really don't need a large buffer here, just to account for spikes.
		Timeout:     1 * time.Hour,
		OnTimeout:   func() {},
		OnTask:      workerState.handlePacket,
	}

	// Start a worker for the subscription and create a subsription.
	subscription.worker = worker.StartWorker(workerConfig)

	// Start reading and forwarding RTCP packets goroutine.
	ch := subscription.startReadRTCP()

	return subscription, ch, nil
}

func (s *VideoSubscription) Unsubscribe() error {
	if !s.stopped.CompareAndSwap(false, true) {
		return fmt.Errorf("Already stopped")
	}

	s.worker.Stop()
	s.logger.Info("Unsubscribed")
	s.telemetry.End()
	return s.controller.RemoveTrack(s.rtpSender)
}

func (s *VideoSubscription) WriteRTP(packet rtp.Packet) error {
	// Send the packet to the worker.
	return s.worker.Send(packet)
}

// Read incoming RTCP packets. Before these packets are returned they are processed by interceptors.
func (s *VideoSubscription) startReadRTCP() <-chan KeyFrameRequest {
	ch := make(chan KeyFrameRequest)

	go func() {
		defer close(ch)
		defer s.Unsubscribe()
		defer s.telemetry.AddEvent("Stopped")
		defer s.logger.Info("Stopped")

		for {
			packets, _, err := s.rtpSender.ReadRTCP()
			if err != nil {
				if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, io.EOF) {
					s.logger.Debugf("Failed to read RTCP: %v", err)
					return
				}
			}

			// We only want to inform others about PLIs and FIRs. We skip the rest of the packets for now.
			for _, packet := range packets {
				switch packet.(type) {
				// For simplicity we assume that any of the key frame requests is just a key frame request.
				case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
					ch <- KeyFrameRequest{}
				}
			}
		}
	}()

	return ch
}

// Internal state of a worker that runs in its own goroutine.
type workerState struct {
	// Rewriter of the packet IDs.
	packetRewriter *rewriter.PacketRewriter
	// Undelying output track.
	rtpTrack *webrtc.TrackLocalStaticRTP
}

func (w *workerState) handlePacket(packet rtp.Packet) {
	w.rtpTrack.WriteRTP(w.packetRewriter.ProcessIncoming(packet))
}
