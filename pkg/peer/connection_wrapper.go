package peer

import (
	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/pion/webrtc/v3"
)

type ConnectionWrapper struct {
	connection      *webrtc.PeerConnection
	requestKeyFrame func(track common.TrackInfo, simulcast common.SimulcastLayer)
}

func NewConnectionWrapper(
	connection *webrtc.PeerConnection,
	requestKeyFrame func(common.TrackInfo, common.SimulcastLayer),
) ConnectionWrapper {
	return ConnectionWrapper{connection, requestKeyFrame}
}

func (c ConnectionWrapper) Subscribe(track *webrtc.TrackLocalStaticRTP) (*webrtc.RTPSender, error) {
	return c.connection.AddTrack(track)
}

func (c ConnectionWrapper) Unsubscribe(sender *webrtc.RTPSender) error {
	return c.connection.RemoveTrack(sender)
}

func (c ConnectionWrapper) RequestKeyFrame(track common.TrackInfo, simulcast common.SimulcastLayer) {
	c.requestKeyFrame(track, simulcast)
}
