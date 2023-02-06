package subscription

import (
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/matrix-org/waterfall/pkg/peer/subscription/rewriter"
	"github.com/matrix-org/waterfall/pkg/webrtc_ext"
	"github.com/matrix-org/waterfall/pkg/worker"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
)

type RequestKeyFrameFn = func(track webrtc_ext.TrackInfo, simulcast webrtc_ext.SimulcastLayer) error

type VideoSubscription struct {
	rtpSender *webrtc.RTPSender

	info         webrtc_ext.TrackInfo
	currentLayer atomic.Int32 // atomic webrtc_ext.SimulcastLayer

	controller        SubscriptionController
	requestKeyFrameFn RequestKeyFrameFn
	worker            *worker.Worker[rtp.Packet]
	logger            *logrus.Entry
}

func NewVideoSubscription(
	info webrtc_ext.TrackInfo,
	simulcast webrtc_ext.SimulcastLayer,
	controller SubscriptionController,
	requestKeyFrameFn RequestKeyFrameFn,
	logger *logrus.Entry,
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

	// Create a subscription.
	subscription := &VideoSubscription{
		rtpSender,
		info,
		currentLayer,
		controller,
		requestKeyFrameFn,
		nil,
		logger,
	}

	// Create a worker state.
	workerState := workerState{
		packetRewriter: rewriter.NewPacketRewriter(),
		rtpTrack:       rtpTrack,
	}

	// Configure the worker for the subscription.
	workerConfig := worker.Config[rtp.Packet]{
		ChannelSize: 32,
		Timeout:     3 * time.Second,
		OnTimeout: func() {
			layer := webrtc_ext.SimulcastLayer(subscription.currentLayer.Load())
			logger.Warnf("No RTP on subscription %s (%s)", subscription.info.TrackID, layer)
			subscription.requestKeyFrame()
		},
		OnTask: workerState.handlePacket,
	}

	// Start a worker for the subscription and create a subsription.
	subscription.worker = worker.StartWorker(workerConfig)

	// Start reading and forwarding RTCP packets.
	go subscription.readRTCP()

	// Request a key frame, so that we can get it from the publisher right after subscription.
	subscription.requestKeyFrame()

	return subscription, nil
}

func (s *VideoSubscription) Unsubscribe() error {
	s.worker.Stop()
	s.logger.Infof("Unsubscribing from %s (%s)", s.info.TrackID, webrtc_ext.SimulcastLayer(s.currentLayer.Load()))
	return s.controller.RemoveTrack(s.rtpSender)
}

func (s *VideoSubscription) WriteRTP(packet rtp.Packet) error {
	// Send the packet to the worker.
	return s.worker.Send(packet)
}

func (s *VideoSubscription) SwitchLayer(simulcast webrtc_ext.SimulcastLayer) {
	s.logger.Infof("Switching layer on %s to %s", s.info.TrackID, simulcast)
	s.currentLayer.Store(int32(simulcast))
	s.requestKeyFrame()
}

func (s *VideoSubscription) TrackInfo() webrtc_ext.TrackInfo {
	return s.info
}

func (s *VideoSubscription) Simulcast() webrtc_ext.SimulcastLayer {
	return webrtc_ext.SimulcastLayer(s.currentLayer.Load())
}

// Read incoming RTCP packets. Before these packets are returned they are processed by interceptors.
func (s *VideoSubscription) readRTCP() {
	for {
		packets, _, err := s.rtpSender.ReadRTCP()
		if err != nil {
			if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, io.EOF) {
				layer := webrtc_ext.SimulcastLayer(s.currentLayer.Load())
				s.logger.Warnf("failed to read RTCP on track: %s (%s): %s", s.info.TrackID, layer, err)
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
	layer := webrtc_ext.SimulcastLayer(s.currentLayer.Load())
	if err := s.requestKeyFrameFn(s.info, layer); err != nil {
		s.logger.Errorf("Failed to request key frame: %s", err)
	}
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
