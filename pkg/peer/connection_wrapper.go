package peer

import "github.com/pion/webrtc/v3"

type ConnectionWrapper struct {
	connection *webrtc.PeerConnection
}

func NewConnectionWrapper(connection *webrtc.PeerConnection) ConnectionWrapper {
	return ConnectionWrapper{
		connection: connection,
	}
}

func (c ConnectionWrapper) Subscribe(track *webrtc.TrackLocalStaticRTP) (*webrtc.RTPSender, error) {
	return c.connection.AddTrack(track)
}

func (c ConnectionWrapper) Unsubscribe(sender *webrtc.RTPSender) error {
	return c.connection.RemoveTrack(sender)
}
