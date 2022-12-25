package peer

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
	Codec    webrtc.RTPCodecCapability
}

// Information with some extended (optional fields).
// Ideally we would want to have different types for different tracks, but Go does not have ADTs,
// so it's the only way to do it without writing too much of a boilerplate.
type ExtendedTrackInfo struct {
	// Information about a track.
	TrackInfo
	// Optional simulcast layer (if any). Would be nil for non-simulcast or audio tracks.
	Layer SimulcastLayer
}

func trackInfoFromTrack(track *webrtc.TrackRemote) TrackInfo {
	return TrackInfo{
		TrackID:  track.ID(),
		StreamID: track.StreamID(),
		Codec:    track.Codec().RTPCodecCapability,
	}
}

type ConnectionWrapper struct {
	connection *webrtc.PeerConnection
}

func (c ConnectionWrapper) Subscribe(track *webrtc.TrackLocalStaticRTP) (*webrtc.RTPSender, error) {
	return c.connection.AddTrack(track)
}

func (c ConnectionWrapper) Unsubscribe(sender *webrtc.RTPSender) error {
	return c.connection.RemoveTrack(sender)
}
