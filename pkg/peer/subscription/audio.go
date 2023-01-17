package subscription

import (
	"errors"
	"fmt"
	"io"

	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

type AudioSubscription struct {
	sender     *webrtc.RTPSender
	controller SubscriptionController
}

func NewAudioSubscription(
	outputTrack *webrtc.TrackLocalStaticRTP,
	controller SubscriptionController,
) (*AudioSubscription, error) {
	if outputTrack == nil {
		return nil, fmt.Errorf("Output track is nil")
	}

	sender, err := controller.AddTrack(outputTrack)
	if err != nil {
		return nil, fmt.Errorf("Failed to add track: %s", err)
	}

	subscription := &AudioSubscription{sender, controller}
	go subscription.readRTCP()

	return subscription, nil
}

func (s *AudioSubscription) Unsubscribe() error {
	return s.controller.RemoveTrack(s.sender)
}

func (s *AudioSubscription) WriteRTP(packet rtp.Packet) error {
	return fmt.Errorf("Bug: no write RTP logic for an audio subscription!")
}

func (s *AudioSubscription) SwitchLayer(simulcast common.SimulcastLayer) {
}

func (s *AudioSubscription) Simulcast() common.SimulcastLayer {
	return common.SimulcastLayerNone
}

func (s *AudioSubscription) readRTCP() {
	// Read incoming RTCP packets. Before these packets are returned they are processed by interceptors.
	// For things like NACK this needs to be called.
	for {
		if _, _, err := s.sender.ReadRTCP(); err != nil {
			if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, io.EOF) {
				return
			}
		}
	}
}
