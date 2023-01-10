package subscription

import (
	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

type Subscription interface {
	Unsubscribe() error
	WriteRTP(packet *rtp.Packet) error
	SwitchLayer(simulcast common.SimulcastLayer)
	Simulcast() common.SimulcastLayer
}

type SubscriptionController interface {
	AddTrack(track *webrtc.TrackLocalStaticRTP) (*webrtc.RTPSender, error)
	RemoveTrack(sender *webrtc.RTPSender) error
}
