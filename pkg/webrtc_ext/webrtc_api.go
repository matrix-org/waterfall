package webrtc_ext

import (
	"fmt"

	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v3"
)

// Creates Pion's WebRTC API that has all required extensions configured (such as simulcast).
func createWebRTCAPI(config Config) (*webrtc.API, error) {
	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterDefaultCodecs(); err != nil {
		return nil, fmt.Errorf("failed to register default codecs: %w", err)
	}

	// Enable extension headers needed for simulcast (if enabled).
	if config.EnableSimulcast {
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
	}

	// Configure the custom IP address of the SFU (if set).
	settingsEngine := webrtc.SettingEngine{}
	if config.PublicIP != "" {
		settingsEngine.SetNAT1To1IPs([]string{config.PublicIP}, webrtc.ICECandidateTypeHost)
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

	// Finally, construct the API with the configured media and settings engines.
	api := webrtc.NewAPI(
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithSettingEngine(settingsEngine),
		webrtc.WithInterceptorRegistry(interceptor),
	)

	return api, nil
}
