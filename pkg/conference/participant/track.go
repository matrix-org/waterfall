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
func (p *PublishedTrack) GetOptimalLayer(requestedWidth, requestedHeight int) common.SimulcastLayer {
	// Audio track. For them we don't have any simulcast. We also don't have any simulcast for video
	// if there was no simulcast enabled at all.
	if p.Info.Kind == webrtc.RTPCodecTypeAudio || len(p.Layers) == 0 {
		return common.SimulcastLayerNone
	}

	// Video track. Calculate the optimal layer closest to the requested resolution.
	desiredLayer := calculateDesiredLayer(p.Metadata.MaxWidth, p.Metadata.MaxHeight, requestedWidth, requestedHeight)

	// Ideally, here we would need to send an error if the desired layer is not available, but we don't
	// have a way to do it. So we just return the closest available layer.
	priority := []common.SimulcastLayer{desiredLayer, common.SimulcastLayerMedium, common.SimulcastLayerLow}

	// More Go boilerplate.
	for _, desiredLayer := range priority {
		layerIndex := slices.IndexFunc(p.Layers, func(simulcast common.SimulcastLayer) bool {
			return simulcast == desiredLayer
		})

		if layerIndex != -1 {
			return p.Layers[layerIndex]
		}
	}

	// Actually this part will never be executed, because if we got to this point,
	// we know that we at least have one layer available.
	return common.SimulcastLayerLow
}

// Metadata that we have received about this track from a user.
// This metadata is only set for video tracks at the moment.
type TrackMetadata struct {
	MaxWidth, MaxHeight int
}

// Calculates the optimal layer closest to the requested resolution. We assume that the full resolution is the
// maximum resolution that we can get from the user. We assume that a medium quality layer is half the size of
// the video (**but not half of the resolution**). I.e. medium quality is high quality divided by 4. And low
// quality is medium quality divided by 4 (which is the same as the high quality dividied by 16).
func calculateDesiredLayer(fullWidth, fullHeight int, desiredWidth, desiredHeight int) common.SimulcastLayer {
	// Calculate combined length of width and height for the full and desired size videos.
	fullSize := fullWidth + fullHeight
	desiredSize := desiredWidth + desiredHeight

	if fullSize == 0 || desiredSize == 0 {
		return common.SimulcastLayerLow
	}

	// Determine which simulcast desiredLayer to subscribe to based on the requested resolution.
	if ratio := float32(fullSize) / float32(desiredSize); ratio <= 1 {
		return common.SimulcastLayerHigh
	} else if ratio <= 2 {
		return common.SimulcastLayerMedium
	}

	// We can't get here actually.
	return common.SimulcastLayerLow
}
