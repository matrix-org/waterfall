package participant_test

import (
	"testing"

	"github.com/matrix-org/waterfall/pkg/conference/participant"
	"github.com/matrix-org/waterfall/pkg/webrtc_ext"
	"github.com/pion/webrtc/v3"
)

func TestGetOptimalLayer(t *testing.T) {
	// Helper function for a quick an descriptive test case definition.
	layers := func(layers ...webrtc_ext.SimulcastLayer) []webrtc_ext.SimulcastLayer {
		return layers
	}

	// Shortcuts for easy and descriptive test case definition.
	low, mid, high := webrtc_ext.SimulcastLayerLow, webrtc_ext.SimulcastLayerMedium, webrtc_ext.SimulcastLayerHigh

	cases := []struct {
		availableLayers             []webrtc_ext.SimulcastLayer
		fullWidth, fullHeight       int
		desiredWidth, desiredHeight int
		expectedOptimalLayer        webrtc_ext.SimulcastLayer
	}{
		{layers(low, mid, high), 1728, 1056, 878, 799, mid},   // Screen sharing (Dave's case).
		{layers(low, mid, high), 1920, 1080, 320, 240, low},   // max=1080p, desired=240p, result=low.
		{layers(low, mid, high), 1920, 1080, 1900, 1000, mid}, // max=1080p, desired=1080pish, result=mid.
		{layers(low, mid, high), 1920, 1080, 0, 0, low},       // max=1080p, desired=undefined, result=low.

		{layers(low, mid, high), 1280, 720, 1280, 720, high}, // max=720p, desired=720p, result=high.
		{layers(low, mid, high), 1280, 720, 640, 480, mid},   // max=720p, desired=480p, result=mid.
		{layers(low, mid, high), 1280, 720, 320, 240, low},   // max=720p, desired=240p, result=low.

		{layers(low, mid), 1280, 720, 1600, 1000, mid},
		{layers(low, mid), 1280, 720, 500, 500, mid},
		{layers(low), 1280, 720, 1600, 1000, low},
		{layers(low), 1280, 720, 500, 500, low},
		{layers(high, mid, low), 0, 0, 1600, 1000, low},
		{layers(high, mid, low), 0, 0, 0, 0, low},
		{layers(high, mid, low), 600, 400, 0, 0, low},

		{layers(high), 1280, 720, 200, 200, high},
	}

	mock := participant.PublishedTrack{
		Info: webrtc_ext.TrackInfo{
			Kind: webrtc.RTPCodecTypeVideo,
		},
	}

	for _, c := range cases {
		mock.Layers = c.availableLayers
		mock.Metadata.MaxWidth = c.fullWidth
		mock.Metadata.MaxHeight = c.fullHeight

		optimalLayer := mock.GetOptimalLayer(c.desiredWidth, c.desiredHeight)
		if optimalLayer != c.expectedOptimalLayer {
			t.Errorf("Expected optimal layer %s, got %s", c.expectedOptimalLayer, optimalLayer)
		}
	}
}

func TestGetOptimalLayerAudio(t *testing.T) {
	mock := participant.PublishedTrack{
		Info: webrtc_ext.TrackInfo{
			Kind: webrtc.RTPCodecTypeAudio,
		},
	}

	mock.Layers = []webrtc_ext.SimulcastLayer{webrtc_ext.SimulcastLayerLow}
	if mock.GetOptimalLayer(100, 100) != webrtc_ext.SimulcastLayerNone {
		t.Fatal("Expected no simulcast layer for audio")
	}
}
