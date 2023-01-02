package participant

import (
	"time"

	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/thoas/go-funk"
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
	// The timestamp at which we are allowed to send the FIR or PLI request. We don't want to send them
	// too often, so we introduce some trivial rate limiting to not "enforce" too many key frames.
	canRequestKeyframeAt time.Time
}

// Calculate the layer that we can use based on the requirements passed as parameters and available layers.
func (p *PublishedTrack) GetDesiredLayer(requestedWidth, requestedHeight int) common.SimulcastLayer {
	// Audio track. For them we don't have any simulcast. We also don't have any simulcast for video
	// if there was no simulcast enabled at all.
	if !p.Metadata.IsVideoTrack() || len(p.Layers) == 0 {
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
	if funk.Contains(p.Layers, desiredLayer) {
		return desiredLayer
	}

	// If we wanted high, but high is not available, let's try to see if medium is there.
	if desiredLayer == common.SimulcastLayerHigh {
		if funk.Contains(p.Layers, common.SimulcastLayerMedium) {
			return common.SimulcastLayerMedium
		}

		// Low is always there, otherwise the `availableLayers` would be empty and we would have returned earlier.
		return common.SimulcastLayerLow
	}

	// If we requested medium and it's not available, we return low (unless the only available layer is high).
	if desiredLayer == common.SimulcastLayerMedium {
		if funk.Contains(p.Layers, common.SimulcastLayerLow) {
			return common.SimulcastLayerLow
		}

		// Apparently there is only single layer available: high, then we must send it. Maybe others has not yet
		// been published - the client can always re-request a different quality later if needed.
		return common.SimulcastLayerHigh
	}

	// If we got here, then the low layer was requested, but it's not available.
	// Let's try to return medium then if it's available.
	if funk.Contains(p.Layers, common.SimulcastLayerMedium) {
		return common.SimulcastLayerMedium
	}

	// No other choice rather than sending low.
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

func (t TrackMetadata) IsVideoTrack() bool {
	return t.FullResolution() > 0
}
