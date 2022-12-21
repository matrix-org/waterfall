package conference

import (
	"fmt"
	"time"

	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/matrix-org/waterfall/pkg/peer"
	"github.com/matrix-org/waterfall/pkg/signaling"
	"github.com/sirupsen/logrus"
	"github.com/thoas/go-funk"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// Things that we assume as identifiers for the participants in the call.
// There could be no 2 participants in the room with identical IDs.
type ParticipantID struct {
	UserID   id.UserID
	DeviceID id.DeviceID
	CallID   string
}

// Represents a track that a peer has published (has already started sending to the SFU).
type PublishedTrack struct {
	// Info about the track.
	info peer.TrackInfo
	// Available simulcast layers.
	layers []peer.SimulcastLayer
	// The timestamp at which we are allowed to send the FIR or PLI request. We don't want to send them
	// too often, so we introduce some trivial rate limiting to not "enforce" too many key frames.
	canSendKeyframeAt time.Time
}

// Participant represents a participant in the conference.
type Participant struct {
	id              ParticipantID
	logger          *logrus.Entry
	peer            *peer.Peer[ParticipantID]
	remoteSessionID id.SessionID
	streamMetadata  event.CallSDPStreamMetadata
	publishedTracks map[string]PublishedTrack
	heartbeatPong   chan<- common.Pong
}

func (p *Participant) asMatrixRecipient() signaling.MatrixRecipient {
	return signaling.MatrixRecipient{
		UserID:          p.id.UserID,
		DeviceID:        p.id.DeviceID,
		CallID:          p.id.CallID,
		RemoteSessionID: p.remoteSessionID,
	}
}

func (p *Participant) sendDataChannelMessage(toSend event.Event) error {
	jsonToSend, err := toSend.MarshalJSON()
	if err != nil {
		return fmt.Errorf("Failed to marshal data channel message: %w", err)
	}

	if err := p.peer.SendOverDataChannel(string(jsonToSend)); err != nil {
		// TODO: We must buffer the message in this case and re-send it once the data channel is recovered!
		return fmt.Errorf("Failed to send data channel message: %w", err)
	}

	return nil
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

type PublishedTrackInfo struct {
	peer.TrackInfo
	availableLayers []peer.SimulcastLayer
	metadata        TrackMetadata
}

func (p *Participant) getPublishedTracksInfo() map[string]PublishedTrackInfo {
	tracksMetadata := make(map[string]TrackMetadata)
	for _, metadata := range p.streamMetadata {
		for id, track := range metadata.Tracks {
			tracksMetadata[id] = TrackMetadata{
				maxWidth:  track.Width,
				maxHeight: track.Height,
			}
		}
	}

	publishedTracksMetadata := make(map[string]PublishedTrackInfo)
	for id, track := range p.publishedTracks {
		metadata, ok := tracksMetadata[id]

		if !ok {
			// We don't have metadata for this track, so we can't send it to the client.
			p.logger.Warnf("No metadata for published track %s", id)
			continue
		}

		publishedTracksMetadata[id] = PublishedTrackInfo{
			TrackInfo:       track.info,
			availableLayers: track.layers,
			metadata:        metadata,
		}
	}

	return publishedTracksMetadata
}

// Calculate the layer that we can use based on the requirements passed as parameters and available layers.
func (p *PublishedTrackInfo) getDesiredLayer(requestedWidth, requestedHeight int) peer.SimulcastLayer {
	// Audio track. For them we don't have any simulcast. We also don't have any simulcast for video
	// if there was no simulcast enabled at all.
	if !p.metadata.isVideoTrack() || len(p.availableLayers) == 0 {
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
	if funk.Contains(p.availableLayers, desiredLayer) {
		return desiredLayer
	}

	// If we wanted high, but high is not available, let's try to see if medium is there.
	if desiredLayer == peer.SimulcastLayerHigh {
		if funk.Contains(p.availableLayers, peer.SimulcastLayerMedium) {
			return peer.SimulcastLayerMedium
		}

		// Low is always there, otherwise the `availableLayers` would be empty and we would have returned earlier.
		return peer.SimulcastLayerLow
	}

	// If we requested medium and it's not available, we return low (unless the only available layer is high).
	if desiredLayer == peer.SimulcastLayerMedium {
		if funk.Contains(p.availableLayers, peer.SimulcastLayerLow) {
			return peer.SimulcastLayerLow
		}

		// Apparently there is only single layer available: high, then we must send it. Maybe others has not yet
		// been published - the client can always re-request a different quality later if needed.
		return peer.SimulcastLayerHigh
	}

	// If we got here, then the low layer was requested, but it's not available.
	// Let's try to return medium then if it's available.
	if funk.Contains(p.availableLayers, peer.SimulcastLayerMedium) {
		return peer.SimulcastLayerMedium
	}

	// No other choice rather than sending low.
	return peer.SimulcastLayerLow
}
