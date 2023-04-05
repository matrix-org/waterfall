package track

import (
	"github.com/matrix-org/waterfall/pkg/webrtc_ext"
	"github.com/pion/webrtc/v3"
)

// Metadata that we have received about this track from a user.
// This metadata is only set for video tracks at the moment.
type TrackMetadata struct {
	MaxWidth, MaxHeight int
	Muted               bool
}

// Calculate the layer that we can use based on the requirements passed as parameters and available layers.
func getOptimalLayer(
	layers map[webrtc_ext.SimulcastLayer]struct{},
	metadata TrackMetadata,
	requestedWidth, requestedHeight int,
) webrtc_ext.SimulcastLayer {
	// If we don't have any layers available, then there is no simulcast.
	if _, found := layers[webrtc_ext.SimulcastLayerNone]; found || len(layers) == 0 {
		return webrtc_ext.SimulcastLayerNone
	}

	// Video track. Calculate the optimal layer closest to the requested resolution.
	desiredLayer := calculateDesiredLayer(metadata.MaxWidth, metadata.MaxHeight, requestedWidth, requestedHeight)

	// Ideally, here we would need to send an error if the desired layer is not available, but we don't
	// have a way to do it. So we just return the closest available layer.
	priority := []webrtc_ext.SimulcastLayer{
		desiredLayer,
		webrtc_ext.SimulcastLayerMedium,
		webrtc_ext.SimulcastLayerLow,
		webrtc_ext.SimulcastLayerHigh,
	}

	// More Go boilerplate.
	for _, desiredLayer := range priority {
		if _, found := layers[desiredLayer]; found {
			return desiredLayer
		}
	}

	// Actually this part will never be executed, because if we got to this point,
	// we know that we at least have one layer available.
	return webrtc_ext.SimulcastLayerLow
}

// Calculates the optimal layer closest to the requested resolution. We assume that the full resolution is the
// maximum resolution that we can get from the user. We assume that a medium quality layer is half the size of
// the video (**but not half of the resolution**). I.e. medium quality is high quality divided by 4. And low
// quality is medium quality divided by 4 (which is the same as the high quality dividied by 16).
func calculateDesiredLayer(fullWidth, fullHeight int, desiredWidth, desiredHeight int) webrtc_ext.SimulcastLayer {
	// Calculate combined length of width and height for the full and desired size videos.
	fullSize := fullWidth + fullHeight
	desiredSize := desiredWidth + desiredHeight

	if fullSize == 0 || desiredSize == 0 {
		return webrtc_ext.SimulcastLayerLow
	}

	// Determine which simulcast desiredLayer to subscribe to based on the requested resolution.
	if ratio := float32(fullSize) / float32(desiredSize); ratio <= 1 {
		return webrtc_ext.SimulcastLayerHigh
	} else if ratio <= 2 {
		return webrtc_ext.SimulcastLayerMedium
	}

	return webrtc_ext.SimulcastLayerLow
}

// Does this published track contain any simulcast tracks or is it a non-simulcast published track.
func (p *PublishedTrack[SubscriberID]) isSimulcast() bool {
	// The track is a video track.
	video := p.info.Kind == webrtc.RTPCodecTypeVideo

	// There is at least a single published track.
	hasPublishers := len(p.video.publishers) > 0

	// We have a track without the RID (simulcast tracks do have RID extension).
	hasNonSimulcastLayer := p.video.publishers[webrtc_ext.SimulcastLayerNone] != nil

	return video && hasPublishers && !hasNonSimulcastLayer
}
