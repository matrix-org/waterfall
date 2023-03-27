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
	"go.opentelemetry.io/otel/attribute"
)

type RequestKeyFrameFn = func(simulcast webrtc_ext.SimulcastLayer) error

type VideoSubscription struct {
	rtpSender *webrtc.RTPSender

	info webrtc_ext.TrackInfo

	currentLayer atomic.Int32 // atomic webrtc_ext.SimulcastLayer
	muted        atomic.Bool  // we don't expect any RTP packets
	stalled      atomic.Bool  // we do expect RTP packets, but haven't received for a while

	controller        SubscriptionController
	requestKeyFrameFn RequestKeyFrameFn
	worker            *worker.Worker[rtp.Packet]

	logger    *logrus.Entry
	telemetry *telemetry.Telemetry
}

func NewVideoSubscription(
	info webrtc_ext.TrackInfo,
	simulcast webrtc_ext.SimulcastLayer,
	muted bool,
	controller SubscriptionController,
	requestKeyFrameFn RequestKeyFrameFn,
	logger *logrus.Entry,
	telemetryBuilder *telemetry.ChildBuilder,
) (*VideoSubscription, error) {
	// Create a new track.
	rtpTrack, err := webrtc.NewTrackLocalStaticRTP(info.Codec, info.TrackID, info.StreamID)
	if err != nil {
		return nil, fmt.Errorf("Failed to create track: %s", err)
	}

	rtpSender, err := controller.AddTrack(rtpTrack)
	if err != nil {
		return nil, fmt.Errorf("Failed to add track: %s", err)
	}

	// Atomic version of the webrtc_ext.SimulcastLayer.
	var currentLayer atomic.Int32
	currentLayer.Store(int32(simulcast))

	// By default we assume that the track is not muted.
	var mutedState atomic.Bool
	mutedState.Store(muted)

	// Also, the track is not stalled by default.
	var stalled atomic.Bool

	// Create a subscription.
	subscription := &VideoSubscription{
		rtpSender,
		info,
		currentLayer,
		mutedState,
		stalled,
		controller,
		requestKeyFrameFn,
		nil,
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
		ChannelSize: 16,              // We really don't need a large buffer here, just to account for spikes.
		Timeout:     3 * time.Second, // When do we assume the subscription is stalled.
		OnTimeout: func() {
			// Not receiving RTP packets for 3 seconds can happen either if we're muted (not an error),
			// or if the peer does not send any data (that's a problem that potentially means a freeze).
			// Also, we don't want to execute this part if the subscription has already been marked as stalled.
			if !subscription.muted.Load() && !subscription.stalled.Load() {
				layer := webrtc_ext.SimulcastLayer(subscription.currentLayer.Load())
				logger.Warnf("No RTP on subscription to %s (%s) for 3 seconds", subscription.info.TrackID, layer)
				subscription.telemetry.Fail(fmt.Errorf("No incoming RTP packets for 3 seconds on %s", layer))
				subscription.stalled.Store(true)
			}
		},
		OnTask: workerState.handlePacket,
	}

	// Start a worker for the subscription and create a subsription.
	subscription.worker = worker.StartWorker(workerConfig)

	// Start reading and forwarding RTCP packets.
	go subscription.readRTCP()

	// Request a key frame, so that we can get it from the publisher right after subscription.
	subscription.requestKeyFrame()

	subscription.telemetry.AddEvent("subscribed", attribute.String("layer", simulcast.String()))

	return subscription, nil
}

func (s *VideoSubscription) Unsubscribe() error {
	s.worker.Stop()
	s.logger.Infof("Unsubscribing from %s (%s)", s.info.TrackID, webrtc_ext.SimulcastLayer(s.currentLayer.Load()))
	s.telemetry.End()
	return s.controller.RemoveTrack(s.rtpSender)
}

func (s *VideoSubscription) WriteRTP(packet rtp.Packet) error {
	if s.stalled.CompareAndSwap(true, false) {
		simulcast := webrtc_ext.SimulcastLayer(s.currentLayer.Load())
		s.logger.Infof("Recovered subscription to %s (%s)", s.info.TrackID, simulcast)
		s.telemetry.AddEvent("subscription recovered")
	}

	// Send the packet to the worker.
	return s.worker.Send(packet)
}

func (s *VideoSubscription) SwitchLayer(simulcast webrtc_ext.SimulcastLayer) {
	s.logger.Infof("Switching layer on %s to %s", s.info.TrackID, simulcast)
	s.telemetry.AddEvent("switching simulcast layer", attribute.String("layer", simulcast.String()))
	s.currentLayer.Store(int32(simulcast))
	s.requestKeyFrameFn(simulcast)
}

func (s *VideoSubscription) TrackInfo() webrtc_ext.TrackInfo {
	return s.info
}

func (s *VideoSubscription) Simulcast() webrtc_ext.SimulcastLayer {
	return webrtc_ext.SimulcastLayer(s.currentLayer.Load())
}

func (s *VideoSubscription) UpdateMuteState(muted bool) {
	s.muted.Store(muted)
}

// Read incoming RTCP packets. Before these packets are returned they are processed by interceptors.
func (s *VideoSubscription) readRTCP() {
	for {
		packets, _, err := s.rtpSender.ReadRTCP()
		if err != nil {
			if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, io.EOF) {
				layer := webrtc_ext.SimulcastLayer(s.currentLayer.Load())
				s.logger.Debugf("failed to read RTCP on track: %s (%s): %s", s.info.TrackID, layer, err)
				s.telemetry.AddEvent("subscription stopped")
				s.worker.Stop()
				return
			}
		}

		// We only want to inform others about PLIs and FIRs. We skip the rest of the packets for now.
		for _, packet := range packets {
			switch packet.(type) {
			// For simplicity we assume that any of the key frame requests is just a key frame request.
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				s.requestKeyFrame()
			}
		}
	}
}

func (s *VideoSubscription) requestKeyFrame() {
	s.requestKeyFrameFn(webrtc_ext.SimulcastLayer(s.currentLayer.Load()))
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
