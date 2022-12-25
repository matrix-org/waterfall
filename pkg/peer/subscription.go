package peer

import (
	"fmt"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

type ConnectionController interface {
	Subscribe(track *webrtc.TrackLocalStaticRTP) (*webrtc.RTPSender, error)
	Unsubscribe(sender *webrtc.RTPSender) error
}

type Subscription struct {
	rtpSender  *webrtc.RTPSender
	rtpTrack   *webrtc.TrackLocalStaticRTP
	info       ExtendedTrackInfo
	connection ConnectionController
}

func NewSubscription(info ExtendedTrackInfo, connection ConnectionController) (*Subscription, error) {
	// Set the RID if any (would be "" if no simulcast is used).
	setRid := webrtc.WithRTPStreamID(SimulcastLayerToRID(info.Layer))

	// Create a new track.
	rtpTrack, err := webrtc.NewTrackLocalStaticRTP(info.Codec, info.TrackID, info.StreamID, setRid)
	if err != nil {
		return nil, fmt.Errorf("Failed to create track: %s", err)
	}

	rtpSender, err := connection.Subscribe(rtpTrack)
	if err != nil {
		return nil, fmt.Errorf("Failed to add track: %s", err)
	}

	return &Subscription{rtpSender, rtpTrack, info, connection}, nil
}

func (s *Subscription) Unsubscribe() error {
	return s.connection.Unsubscribe(s.rtpSender)
}

func (s *Subscription) WriteRTP(packet *rtp.Packet) error {
	return s.rtpTrack.WriteRTP(packet)
}

func (s *Subscription) TrackInfo() ExtendedTrackInfo {
	return s.info
}
