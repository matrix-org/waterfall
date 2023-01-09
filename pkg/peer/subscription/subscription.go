package subscription

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
)

type ConnectionController interface {
	Subscribe(track *webrtc.TrackLocalStaticRTP) (*webrtc.RTPSender, error)
	Unsubscribe(sender *webrtc.RTPSender) error
	RequestKeyFrame(track common.TrackInfo)
}

type Subscription struct {
	rtpSender      *webrtc.RTPSender
	rtpTrack       *webrtc.TrackLocalStaticRTP
	info           common.TrackInfo
	connection     ConnectionController
	watchdog       *common.WatchdogChannel
	logger         *logrus.Entry
	packetRewriter *PacketRewriter
}

func NewSubscription(
	info common.TrackInfo,
	connection ConnectionController,
	logger *logrus.Entry,
) (*Subscription, error) {
	// Create a new track.
	rtpTrack, err := webrtc.NewTrackLocalStaticRTP(info.Codec, info.TrackID, info.StreamID)
	if err != nil {
		return nil, fmt.Errorf("Failed to create track: %s", err)
	}

	rtpSender, err := connection.Subscribe(rtpTrack)
	if err != nil {
		return nil, fmt.Errorf("Failed to add track: %s", err)
	}

	// This is the SSRC that all outgoing (rewritten) packets will have.
	outgoingSSRC := uint32(rtpSender.GetParameters().Encodings[0].SSRC)

	// This is the SSRC of the incoming packets that we expect (i.e. SSRC of the currently selected layer).
	selectedSSRC := info.Simulcast.SSRC

	// Create a subscription.
	subscription := &Subscription{
		rtpSender,
		rtpTrack,
		info,
		connection,
		nil,
		logger,
		NewPacketRewriter(outgoingSSRC, selectedSSRC),
	}

	// Configure watchdog for the subscription so that we know when we don't receive any new frames.
	watchdogConfig := common.WatchdogConfig{
		Timeout: 2 * time.Second,
		OnTimeout: func() {
			ti := subscription.info
			logger.Warnf("No RTP on subscription for %s (%s)", ti.TrackID, ti.Simulcast)
			subscription.connection.RequestKeyFrame(ti)
		},
	}

	// Start a watchdog for the subscription and create a subsription.
	subscription.watchdog = common.StartWatchdog(watchdogConfig)

	// Start reading and forwarding RTCP packets.
	go subscription.readRTCP()

	// Request a key frame, so that we can get it from the publisher right after subscription.
	connection.RequestKeyFrame(info)

	return subscription, nil
}

func (s *Subscription) Unsubscribe() error {
	s.watchdog.Close()
	s.logger.Infof("Unsubscribing from %s (%s)", s.info.TrackID, s.info.Simulcast)
	return s.connection.Unsubscribe(s.rtpSender)
}

func (s *Subscription) WriteRTP(packet *rtp.Packet) error {
	if !s.watchdog.Notify() {
		return fmt.Errorf("Ignoring RTP, subscription %s is dead", s.info.TrackID)
	}

	rewrittenPacket, err := s.packetRewriter.ProcessIncoming(packet)
	if err != nil {
		return err
	}

	return s.rtpTrack.WriteRTP(rewrittenPacket)
}

func (s *Subscription) SwitchLayer(simulcast common.Simulcast) {
	s.logger.Infof("Switching layer on %s to %s", s.info.TrackID, simulcast)
	s.info.Simulcast = simulcast
	s.packetRewriter.SwitchLayer(simulcast.SSRC)
	s.connection.RequestKeyFrame(s.info)
}

func (s *Subscription) TrackInfo() common.TrackInfo {
	return s.info
}

// Read incoming RTCP packets. Before these packets are returned they are processed by interceptors.
func (s *Subscription) readRTCP() {
	for {
		packets, _, err := s.rtpSender.ReadRTCP()
		if err != nil {
			if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, io.EOF) {
				s.logger.Warnf("failed to read RTCP on track: %s (%s): %s", s.info.TrackID, s.info.Simulcast, err)
				s.watchdog.Close()
				return
			}
		}

		// We only want to inform others about PLIs and FIRs. We skip the rest of the packets for now.
		for _, packet := range packets {
			switch packet.(type) {
			// For simplicity we assume that any of the key frame requests is just a key frame request.
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				s.connection.RequestKeyFrame(s.info)
			}
		}
	}
}
