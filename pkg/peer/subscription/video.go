package subscription

import (
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
)

type RequestKeyFrameFn = func(track common.TrackInfo, simulcast common.SimulcastLayer)

type VideoSubscription struct {
	rtpSender *webrtc.RTPSender
	rtpTrack  *webrtc.TrackLocalStaticRTP

	info           common.TrackInfo
	currentLayer   atomic.Int32 // atomic common.SimulcastLayer
	packetRewriter *PacketRewriter

	controller        SubscriptionController
	requestKeyFrameFn RequestKeyFrameFn
	watchdog          *common.WatchdogChannel
	logger            *logrus.Entry
}

func NewVideoSubscription(
	info common.TrackInfo,
	simulcast common.SimulcastLayer,
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

	// This is the SSRC that all outgoing (rewritten) packets will have.
	outgoingSSRC := uint32(rtpSender.GetParameters().Encodings[0].SSRC)

	// Atomic version of the common.SimulcastLayer.
	var currentLayer atomic.Int32
	currentLayer.Store(int32(simulcast))

	// Create a subscription.
	subscription := &VideoSubscription{
		rtpSender,
		rtpTrack,
		info,
		currentLayer,
		NewPacketRewriter(outgoingSSRC),
		controller,
		requestKeyFrameFn,
		nil,
		logger,
	}

	// Configure watchdog for the subscription so that we know when we don't receive any new frames.
	watchdogConfig := common.WatchdogConfig{
		Timeout: 2 * time.Second,
		OnTimeout: func() {
			logger.Warnf("No RTP on subscription for %s (%s)", subscription.info.TrackID, subscription.currentLayer.Load())
			subscription.requestKeyFrame()
		},
	}

	// Start a watchdog for the subscription and create a subsription.
	subscription.watchdog = common.StartWatchdog(watchdogConfig)

	// Start reading and forwarding RTCP packets.
	go subscription.readRTCP()

	// Request a key frame, so that we can get it from the publisher right after subscription.
	subscription.requestKeyFrame()

	return subscription, nil
}

func (s *VideoSubscription) Unsubscribe() error {
	s.watchdog.Close()
	s.logger.Infof("Unsubscribing from %s (%s)", s.info.TrackID, s.currentLayer)
	return s.controller.RemoveTrack(s.rtpSender)
}

func (s *VideoSubscription) WriteRTP(packet rtp.Packet) error {
	if !s.watchdog.Notify() {
		return fmt.Errorf("Ignoring RTP, subscription %s is dead", s.info.TrackID)
	}

	rewrittenPacket, err := s.packetRewriter.ProcessIncoming(packet)
	if err != nil {
		return err
	}

	return s.rtpTrack.WriteRTP(rewrittenPacket)
}

func (s *VideoSubscription) SwitchLayer(simulcast common.SimulcastLayer) {
	s.logger.Infof("Switching layer on %s to %s", s.info.TrackID, simulcast)
	s.currentLayer.Store(int32(simulcast))
	s.requestKeyFrame()
}

func (s *VideoSubscription) TrackInfo() common.TrackInfo {
	return s.info
}

func (s *VideoSubscription) Simulcast() common.SimulcastLayer {
	return common.SimulcastLayer(s.currentLayer.Load())
}

// Read incoming RTCP packets. Before these packets are returned they are processed by interceptors.
func (s *VideoSubscription) readRTCP() {
	for {
		packets, _, err := s.rtpSender.ReadRTCP()
		if err != nil {
			if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, io.EOF) {
				s.logger.Warnf("failed to read RTCP on track: %s (%s): %s", s.info.TrackID, s.currentLayer, err)
				s.watchdog.Close()
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
	s.requestKeyFrameFn(s.info, common.SimulcastLayer(s.currentLayer.Load()))
}
