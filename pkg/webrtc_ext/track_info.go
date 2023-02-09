package webrtc_ext

import (
	"github.com/pion/webrtc/v3"
)

type RTCPPacketType int

const (
	PictureLossIndicator RTCPPacketType = iota + 1
	FullIntraRequest
)

type SimulcastLayer int

const (
	SimulcastLayerNone SimulcastLayer = iota
	SimulcastLayerLow
	SimulcastLayerMedium
	SimulcastLayerHigh
)

func RIDToSimulcastLayer(rid string) SimulcastLayer {
	switch rid {
	case "q": // quarter
		return SimulcastLayerLow
	case "h": // half
		return SimulcastLayerMedium
	case "f": // full
		return SimulcastLayerHigh
	default:
		return SimulcastLayerNone
	}
}

func SimulcastLayerToRID(layer SimulcastLayer) string {
	switch layer {
	case SimulcastLayerLow:
		return "q"
	case SimulcastLayerMedium:
		return "h"
	case SimulcastLayerHigh:
		return "f"
	default:
		return ""
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
		return ""
	}
}

// Basic information about a track.
type TrackInfo struct {
	TrackID  string
	StreamID string
	Kind     webrtc.RTPCodecType
	Codec    webrtc.RTPCodecCapability
}

func TrackInfoFromTrack(track *webrtc.TrackRemote) TrackInfo {
	return TrackInfo{
		TrackID:  track.ID(),
		StreamID: track.StreamID(),
		Kind:     track.Kind(),
		Codec:    track.Codec().RTPCodecCapability,
	}
}
