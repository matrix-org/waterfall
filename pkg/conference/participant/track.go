package participant

import (
	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/pion/webrtc/v3"
	"golang.org/x/exp/slices"
)

type TrackID = string

// Represents a track that a peer has published (has already started sending to the SFU).
type PublishedTrack struct {
	// Owner of a published track.
	Owner ID
	// Info about the track.
	Info common.TrackInfo
	// Available simulcast Layers.
	Layers []common.SimulcastLayer
	// Track metadata.
	Metadata TrackMetadata
	// Output track (if any). I.e. a track that would contain all RTP packets
	// of the given published track. Currently only audio tracks will have it.
	OutputTrack *webrtc.TrackLocalStaticRTP
}

// Calculate the layer that we can use based on the requirements passed as parameters and available layers.
func (p *PublishedTrack) GetDesiredLayer(requestedWidth, requestedHeight int) common.SimulcastLayer {
	// Audio track. For them we don't have any simulcast. We also don't have any simulcast for video
	// if there was no simulcast enabled at all.
	if p.Info.Kind == webrtc.RTPCodecTypeAudio || len(p.Layers) == 0 {
		return common.SimulcastLayerNone
	}

	// Video track. Calculate it's full resolution based on a metadata.
	fullResolution := p.Metadata.FullResolution()

	// If no explicit resolution specified, subscribe to the lowest layer.
	desiredLayer := common.SimulcastLayerLow

	// Determine which simulcast desiredLayer to subscribe to based on the requested resolution.
	if requestedWidth != 0 && requestedHeight != 0 {
		desiredResolution := requestedWidth * requestedHeight
		if ratio := float32(fullResolution) / float32(desiredResolution); ratio <= 1 {
			desiredLayer = common.SimulcastLayerHigh
		} else if ratio <= 2 {
			desiredLayer = common.SimulcastLayerMedium
		}
	}

	// Check if the desired layer available at all.
	// If the desired layer is not available, we'll find the closest one.
	layerIndex := slices.IndexFunc(p.Layers, func(simulcast common.SimulcastLayer) bool {
		return simulcast == desiredLayer
	})

	if layerIndex != -1 {
		return p.Layers[layerIndex]
	}

	// Ideally, here we would need to send an error if the desired layer is not available, but we don't
	// have a way to do it. So we just return the closest available layer. Handling the closest available
	// layer is somewhat cumbersome, so instead, we just return the lowest layer. It's not ideal, but ok
	// for a quick fix.
	priority := []common.SimulcastLayer{common.SimulcastLayerLow, common.SimulcastLayerMedium, common.SimulcastLayerHigh}

	// More Go boilerplate.
	for _, desiredLayer := range priority {
		layerIndex := slices.IndexFunc(p.Layers, func(simulcast common.SimulcastLayer) bool {
			return simulcast == desiredLayer
		})

		if layerIndex != -1 {
			return p.Layers[layerIndex]
		}
	}

	// Actually this part will never be executed, because we always have at least one layer available.
	return common.SimulcastLayerLow
}

// Metadata that we have received about this track from a user.
// This metadata is only set for video tracks at the moment.
type TrackMetadata struct {
	MaxWidth, MaxHeight int
}

func (t TrackMetadata) FullResolution() int {
	return t.MaxWidth * t.MaxHeight
}
