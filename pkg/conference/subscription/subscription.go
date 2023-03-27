package subscription

import (
	"github.com/matrix-org/waterfall/pkg/webrtc_ext"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

type Subscription interface {
	Unsubscribe() error
	WriteRTP(packet rtp.Packet) error
	SwitchLayer(simulcast webrtc_ext.SimulcastLayer)
	Simulcast() webrtc_ext.SimulcastLayer
	UpdateMuteState(muted bool)
}

type SubscriptionController interface {
	AddTrack(track *webrtc.TrackLocalStaticRTP) (*webrtc.RTPSender, error)
	RemoveTrack(sender *webrtc.RTPSender) error
}
