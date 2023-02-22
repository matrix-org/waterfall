package publisher

import (
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

// Wrapper for the `webrtc.TrackRemote`.
type RemoteTrack struct {
	// The underlying `webrtc.TrackRemote`.
	Track *webrtc.TrackRemote
}

// Implement the `Track` interface for the `webrtc.TrackRemote`.
func (t *RemoteTrack) ReadPacket() (*rtp.Packet, error) {
	packet, _, err := t.Track.ReadRTP()
	return packet, err
}
