package conference

import (
	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/matrix-org/waterfall/pkg/peer"
	"github.com/matrix-org/waterfall/pkg/signaling"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix/event"
)

// A single conference. Call and conference mean the same in context of Matrix.
type Conference struct {
	id          string
	config      Config
	logger      *logrus.Entry
	endNotifier ConferenceEndNotifier

	signaling    signaling.MatrixSignaling
	participants map[ParticipantID]*Participant

	peerMessages   chan common.Message[ParticipantID, peer.MessageContent]
	matrixMessages common.Receiver[MatrixMessage]
}

func (c *Conference) getParticipant(participantID ParticipantID, optionalErrorMessage error) *Participant {
	participant, ok := c.participants[participantID]
	if !ok {
		if optionalErrorMessage != nil {
			c.logger.WithError(optionalErrorMessage)
		} else {
			c.logger.Error("Participant not found")
		}

		return nil
	}

	return participant
}

// Helper to terminate and remove a participant from the conference.
func (c *Conference) removeParticipant(participantID ParticipantID) {
	participant := c.getParticipant(participantID, nil)
	if participant == nil {
		return
	}

	// Terminate the participant and remove it from the list.
	participant.peer.Terminate()
	delete(c.participants, participantID)

	// Inform the other participants about updated metadata (since the participant left
	// the corresponding streams of the participant are no longer available, so we're informing
	// others about it).
	c.resendMetadataToAllExcept(participantID)

	// Remove the participant's tracks from all participants who might have subscribed to them.
	obsoleteTracks := []*webrtc.TrackLocalStaticRTP{}
	for _, publishedTrack := range participant.publishedTracks {
		obsoleteTracks = append(obsoleteTracks, publishedTrack.track)
	}

	for _, otherParticipant := range c.participants {
		otherParticipant.peer.UnsubscribeFrom(obsoleteTracks)
	}
}

// Helper to get the list of available streams for a given participant, i.e. the list of streams
// that a given participant **can subscribe to**. Each stream may have multiple tracks.
func (c *Conference) getAvailableStreamsFor(forParticipant ParticipantID) event.CallSDPStreamMetadata {
	streamsMetadata := make(event.CallSDPStreamMetadata)
	for id, participant := range c.participants {
		// Skip us. As we know about our own tracks.
		if forParticipant != id {
			// Now, find out which of published tracks belong to the streams for which we have metadata
			// available and construct a metadata map for a given participant based on that.
			for _, track := range participant.publishedTracks {
				trackID, streamID := track.track.ID(), track.track.StreamID()

				if metadata, ok := streamsMetadata[streamID]; ok {
					metadata.Tracks[trackID] = event.CallSDPStreamMetadataTrack{}
					streamsMetadata[streamID] = metadata
				} else if metadata, ok := participant.streamMetadata[streamID]; ok {
					metadata.Tracks = event.CallSDPStreamMetadataTracks{trackID: event.CallSDPStreamMetadataTrack{}}
					streamsMetadata[streamID] = metadata
				} else {
					participant.logger.Warnf("Don't have metadata for stream %s", streamID)
				}
			}
		}
	}

	return streamsMetadata
}

// Helper that returns the list of tracks inside this conference that match the given track IDs.
func (c *Conference) getTracks(identifiers []event.FocusTrackDescription) []*webrtc.TrackLocalStaticRTP {
	tracks := make([]*webrtc.TrackLocalStaticRTP, 0)
	for _, identifier := range identifiers {
		found := false
		// Check if this participant has any of the tracks that we're looking for.
		for _, participant := range c.participants {
			if track, ok := participant.publishedTracks[identifier.TrackID]; ok {
				tracks = append(tracks, track.track)
				found = true
				break
			}
		}
		if !found {
			c.logger.Warnf("track not found: %s", identifier.TrackID)
		}
	}

	return tracks
}

// Helper that sends current metadata about all available tracks to all participants except a given one.
func (c *Conference) resendMetadataToAllExcept(exceptMe ParticipantID) {
	for participantID, participant := range c.participants {
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
