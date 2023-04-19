package subscription

import (
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

type Subscription interface {
	Unsubscribe() error
	WriteRTP(packet rtp.Packet) error
}

type SubscriptionController interface {
	AddTrack(track *webrtc.TrackLocalStaticRTP) (*webrtc.RTPSender, error)
	RemoveTrack(sender *webrtc.RTPSender) error
}
