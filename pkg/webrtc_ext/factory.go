package webrtc_ext

import (
	"fmt"

	"github.com/pion/webrtc/v3"
)

// Peer connection factory is used to construct new (pre-configured) peer connections.
type PeerConnectionFactory struct {
	api *webrtc.API
}

func NewPeerConnectionFactory(config Config) (*PeerConnectionFactory, error) {
	api, err := createWebRTCAPI(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create WebRTC API: %w", err)
	}

	return &PeerConnectionFactory{api}, nil
}

// Creates a peer connection with a specifically configured API (with simulcast etc).
func (f *PeerConnectionFactory) CreatePeerConnection() (*webrtc.PeerConnection, error) {
	return f.api.NewPeerConnection(webrtc.Configuration{})
}
