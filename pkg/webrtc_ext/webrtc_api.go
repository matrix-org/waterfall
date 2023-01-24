package webrtc_ext

import (
	"fmt"

	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v3"
)

// Creates a peer connection with a specifically configured API (with simulcast etc).
func CreatePeerConnection() (*webrtc.PeerConnection, error) {
	// TODO: Could we actually reuse the same API configuration for all peer connections?
	api, err := CreateWebRTCAPI()
	if err != nil {
		return nil, fmt.Errorf("failed to create WebRTC API: %w", err)
	}

	return api.NewPeerConnection(webrtc.Configuration{})
}

// Creates Pion's WebRTC API that has all required extensions configured (such as simulcast).
func CreateWebRTCAPI() (*webrtc.API, error) {
	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterDefaultCodecs(); err != nil {
		return nil, fmt.Errorf("failed to register default codecs: %w", err)
	}

	// Enable extension headers needed for simulcast.
	for _, extension := range []string{
		"urn:ietf:params:rtp-hdrext:sdes:mid",
		"urn:ietf:params:rtp-hdrext:sdes:rtp-stream-id",
		"urn:ietf:params:rtp-hdrext:sdes:repaired-rtp-stream-id",
	} {
		if err := mediaEngine.RegisterHeaderExtension(
			webrtc.RTPHeaderExtensionCapability{URI: extension},
			webrtc.RTPCodecTypeVideo,
		); err != nil {
			return nil, fmt.Errorf("failed to register simulcast extension: %w", err)
		}
	}

	// Create a InterceptorRegistry. This is the user configurable RTP/RTCP
	// Pipeline. This provides NACKs, RTCP Reports and other features. If
	// `webrtc.NewPeerConnection` is used, then it is enabled by default. If
	// it's managed manually, one must create an InterceptorRegistry for each
	// PeerConnection.
	interceptor := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(mediaEngine, interceptor); err != nil {
		return nil, fmt.Errorf("failed to set default interceptors: %w", err)
	}

	return webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine), webrtc.WithInterceptorRegistry(interceptor)), nil
}
