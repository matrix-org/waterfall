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

type TrackInfo struct {
	TrackID  string
	StreamID string
	Codec    webrtc.RTPCodecCapability
}

type SimulcastTrackInfo struct {
	TrackInfo
	Layer SimulcastLayer
}

func trackInfoFromTrack(track *webrtc.TrackRemote) TrackInfo {
	return TrackInfo{
		TrackID:  track.ID(),
		StreamID: track.StreamID(),
		Codec:    track.Codec().RTPCodecCapability,
	}
}
