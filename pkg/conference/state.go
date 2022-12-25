package conference

import (
	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/matrix-org/waterfall/pkg/peer"
	"github.com/matrix-org/waterfall/pkg/signaling"
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix/event"
)

// A single conference. Call and conference mean the same in context of Matrix.
type Conference struct {
	id          string
	config      Config
	logger      *logrus.Entry
	endNotifier ConferenceEndNotifier

	signaling       signaling.MatrixSignaling
	tracker         ParticipantTracker
	streamsMetadata event.CallSDPStreamMetadata

	peerMessages   chan common.Message[ParticipantID, peer.MessageContent]
	matrixMessages common.Receiver[MatrixMessage]
}

func (c *Conference) getParticipant(participantID ParticipantID, optionalErrorMessage error) *Participant {
	participant := c.tracker.getParticipant(participantID)

	if participant == nil {
		if optionalErrorMessage != nil {
			c.logger.WithError(optionalErrorMessage)
		} else {
			c.logger.Error("Participant not found")
		}
	}

	return participant
}

// Helper to terminate and remove a participant from the conference.
func (c *Conference) removeParticipant(participantID ParticipantID) {
	// Remove the participant and then remove its streams from the map.
	for streamID := range c.tracker.removeParticipant(participantID) {
		delete(c.streamsMetadata, streamID)
	}

	// Inform the other participants about updated metadata (since the participant left
	// the corresponding streams of the participant are no longer available, so we're informing
	// others about it).
	c.resendMetadataToAllExcept(participantID)
}

// Helper to get the list of available streams for a given participant, i.e. the list of streams
// that a given participant **can subscribe to**. Each stream may have multiple tracks.
func (c *Conference) getAvailableStreamsFor(forParticipant ParticipantID) event.CallSDPStreamMetadata {
	streamsMetadata := make(event.CallSDPStreamMetadata)
	for trackID, track := range c.tracker.publishedTracks {
		// Skip us. As we know about our own tracks.
		if track.owner != forParticipant {
			streamID := track.info.StreamID

			if metadata, ok := streamsMetadata[streamID]; ok {
				metadata.Tracks[trackID] = event.CallSDPStreamMetadataTrack{}
				streamsMetadata[streamID] = metadata
			} else if metadata, ok := c.streamsMetadata[streamID]; ok {
				metadata.Tracks = event.CallSDPStreamMetadataTracks{trackID: event.CallSDPStreamMetadataTrack{}}
				streamsMetadata[streamID] = metadata
			} else {
				c.logger.Warnf("Don't have metadata for stream %s", streamID)
			}
		}
	}

	return streamsMetadata
}

// Helper that returns the list of published tracks inside this conference that match the given track IDs.
func (c *Conference) findPublishedTracks(trackIDs []string) map[string]PublishedTrack {
	tracks := make(map[string]PublishedTrack)
	for _, identifier := range trackIDs {
		// Check if this participant has any of the tracks that we're looking for.
		if track, ok := c.tracker.publishedTracks[identifier]; ok {
			tracks[identifier] = track
			continue
		}

		c.logger.Warnf("track not found: %s", identifier)
	}

	return tracks
}

// Helper that sends current metadata about all available tracks to all participants except a given one.
func (c *Conference) resendMetadataToAllExcept(exceptMe ParticipantID) {
	for participantID, participant := range c.tracker.participants {
		if participantID != exceptMe {
			participant.sendDataChannelMessage(event.Event{
				Type: event.FocusCallSDPStreamMetadataChanged,
				Content: event.Content{
					Parsed: event.FocusCallSDPStreamMetadataChangedEventContent{
						SDPStreamMetadata: c.getAvailableStreamsFor(participantID),
					},
				},
			})
		}
	}
}

// Helper that updates the metadata each time the metadata is received.
func (c *Conference) updateMetadata(metadata event.CallSDPStreamMetadata) {
	// Note that this assumes that the stream IDs are unique, which is not always so!
	// Yet, our previous implementation of the SFU has always combined the metadata for all available
	// streams when notifying other participants in a call about any changes, so it implicitly expected
	// them to be unique. This is something that we may want to change when switching to the mid-based
	// signaling in the future.
	for stream, content := range metadata {
		c.streamsMetadata[stream] = content
	}

	for trackID, metadata := range streamIntoTrackMetadata(metadata) {
		if track, found := c.tracker.publishedTracks[trackID]; found {
			track.metadata = metadata
			c.tracker.publishedTracks[trackID] = track
		}
	}
}

func streamIntoTrackMetadata(streamMetadata event.CallSDPStreamMetadata) map[TrackID]TrackMetadata {
	tracksMetadata := make(map[string]TrackMetadata)
	for _, metadata := range streamMetadata {
		for id, track := range metadata.Tracks {
			tracksMetadata[id] = TrackMetadata{
				maxWidth:  track.Width,
				maxHeight: track.Height,
			}
		}
	}

	return tracksMetadata
}
