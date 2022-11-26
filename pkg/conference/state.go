package conference

import (
	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/matrix-org/waterfall/pkg/peer"
	"github.com/matrix-org/waterfall/pkg/signaling"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
	"golang.org/x/exp/maps"
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
		logEntry := c.logger.WithFields(logrus.Fields{
			"user_id":   participantID.UserID,
			"device_id": participantID.DeviceID,
		})

		if optionalErrorMessage != nil {
			logEntry.WithError(optionalErrorMessage)
		} else {
			logEntry.Error("Participant not found")
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
	obsoleteTracks := maps.Values(participant.publishedTracks)
	for _, otherParticipant := range c.participants {
		otherParticipant.peer.UnsubscribeFrom(obsoleteTracks)
	}
}

// Helper to get the list of available streams for a given participant, i.e. the list of streams
// that a given participant **can subscribe to**.
func (c *Conference) getAvailableStreamsFor(forParticipant ParticipantID) event.CallSDPStreamMetadata {
	streamsMetadata := make(event.CallSDPStreamMetadata)
	for id, participant := range c.participants {
		if forParticipant != id {
			for streamID, metadata := range participant.streamMetadata {
				streamsMetadata[streamID] = metadata
			}
		}
	}

	return streamsMetadata
}

// Helper that returns the list of streams inside this conference that match the given stream IDs.
func (c *Conference) getTracks(identifiers []event.SFUTrackDescription) []*webrtc.TrackLocalStaticRTP {
	tracks := make([]*webrtc.TrackLocalStaticRTP, len(identifiers))
	for _, participant := range c.participants {
		// Check if this participant has any of the tracks that we're looking for.
		for _, identifier := range identifiers {
			if track, ok := participant.publishedTracks[identifier]; ok {
				tracks = append(tracks, track)
			}
		}
	}
	return tracks
}

// Helper that sends current metadata about all available tracks to all participants except a given one.
func (c *Conference) resendMetadataToAllExcept(exceptMe ParticipantID) {
	for participantID, participant := range c.participants {
		if participantID != exceptMe {
			participant.sendDataChannelMessage(event.SFUMessage{
				Op:       event.SFUOperationMetadata,
				Metadata: c.getAvailableStreamsFor(participantID),
			})
		}
	}
}
