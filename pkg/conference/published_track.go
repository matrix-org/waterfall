package conference

import (
	"time"

	"github.com/matrix-org/waterfall/pkg/peer"
	"github.com/thoas/go-funk"
)

type TrackID = string

// Represents a track that a peer has published (has already started sending to the SFU).
type PublishedTrack struct {
	// Owner of a published track.
	owner ParticipantID
	// Info about the track.
	info peer.TrackInfo
	// Available simulcast layers.
	layers []peer.SimulcastLayer
	// Track metadata.
	metadata TrackMetadata
	// The timestamp at which we are allowed to send the FIR or PLI request. We don't want to send them
	// too often, so we introduce some trivial rate limiting to not "enforce" too many key frames.
	canSendKeyframeAt time.Time
}

type TrackMetadata struct {
	maxWidth, maxHeight int
}

func (t TrackMetadata) fullResolution() int {
	return t.maxWidth * t.maxHeight
}

func (t TrackMetadata) isVideoTrack() bool {
	return t.fullResolution() > 0
}

// Calculate the layer that we can use based on the requirements passed as parameters and available layers.
func (p *PublishedTrack) getDesiredLayer(requestedWidth, requestedHeight int) peer.SimulcastLayer {
	// Audio track. For them we don't have any simulcast. We also don't have any simulcast for video
	// if there was no simulcast enabled at all.
	if !p.metadata.isVideoTrack() || len(p.layers) == 0 {
		return peer.SimulcastLayerNone
	}

	// Video track. Calculate it's full resolution based on a metadata.
	fullResolution := p.metadata.fullResolution()

	// If no explicit resolution specified, subscribe to the lowest layer.
	desiredLayer := peer.SimulcastLayerLow

	// Determine which simulcast desiredLayer to subscribe to based on the requested resolution.
	if requestedWidth != 0 && requestedHeight != 0 {
		desiredResolution := requestedWidth * requestedHeight
		if ratio := float32(fullResolution) / float32(desiredResolution); ratio <= 1 {
			desiredLayer = peer.SimulcastLayerHigh
		} else if ratio <= 2 {
			desiredLayer = peer.SimulcastLayerMedium
		}
	}

	// Check if the desired layer available at all.
	// If the desired layer is not available, we'll find the closest one.
	if funk.Contains(p.layers, desiredLayer) {
		return desiredLayer
	}

	// If we wanted high, but high is not available, let's try to see if medium is there.
	if desiredLayer == peer.SimulcastLayerHigh {
		if funk.Contains(p.layers, peer.SimulcastLayerMedium) {
			return peer.SimulcastLayerMedium
		}

		// Low is always there, otherwise the `availableLayers` would be empty and we would have returned earlier.
		return peer.SimulcastLayerLow
	}

	// If we requested medium and it's not available, we return low (unless the only available layer is high).
	if desiredLayer == peer.SimulcastLayerMedium {
		if funk.Contains(p.layers, peer.SimulcastLayerLow) {
			return peer.SimulcastLayerLow
		}

		// Apparently there is only single layer available: high, then we must send it. Maybe others has not yet
		// been published - the client can always re-request a different quality later if needed.
		return peer.SimulcastLayerHigh
	}

	// If we got here, then the low layer was requested, but it's not available.
	// Let's try to return medium then if it's available.
	if funk.Contains(p.layers, peer.SimulcastLayerMedium) {
		return peer.SimulcastLayerMedium
	}

	// No other choice rather than sending low.
	return peer.SimulcastLayerLow
}
