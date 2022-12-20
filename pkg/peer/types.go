package peer

import (
	"fmt"

	"github.com/pion/webrtc/v3"
)

type RTCPPacketType int

const (
	PictureLossIndicator RTCPPacketType = iota + 1
	FullIntraRequest
)

type SimulcastLayer int

const (
	SimulcastLayerLow SimulcastLayer = iota
	SimulcastLayerMedium
	SimulcastLayerHigh
)

func RIDToSimulcastLayer(rid string) (SimulcastLayer, error) {
	switch rid {
	case "q": // quarter
		return SimulcastLayerLow, nil
	case "h": // half
		return SimulcastLayerMedium, nil
	case "f": // full
		return SimulcastLayerHigh, nil
	default:
		return 0, fmt.Errorf("unknown rid: %s", rid)
	}
}

func SimulcastLayerToRID(layer SimulcastLayer) (string, error) {
	switch layer {
	case SimulcastLayerLow:
		return "q", nil
	case SimulcastLayerMedium:
		return "h", nil
	case SimulcastLayerHigh:
		return "f", nil
	default:
		return "", fmt.Errorf("unknown layer: %d", layer)
	}
}

func (s SimulcastLayer) String() string {
	switch s {
	case SimulcastLayerLow:
		return "low"
	case SimulcastLayerMedium:
		return "medium"
	case SimulcastLayerHigh:
		return "high"
	default:
		return "unknown"
	}
}

// Basic information about a track.
type TrackInfo struct {
	TrackID  string
	StreamID string
	Codec    webrtc.RTPCodecCapability
}

// Information with some extended (optional fields).
// Ideally we would want to have different types for different tracks, but Go does not have ADTs,
// so it's the only way to do it without writing too much of a boilerplate.
type ExtendedTrackInfo struct {
	// Information about a track.
	TrackInfo
	// Optional simulcast layer (if any). Would be nil for non-simulcast or audio tracks.
	Layer *SimulcastLayer
}

func trackInfoFromTrack(track *webrtc.TrackRemote) TrackInfo {
	return TrackInfo{
		TrackID:  track.ID(),
		StreamID: track.StreamID(),
		Codec:    track.Codec().RTPCodecCapability,
	}
}
