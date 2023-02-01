package conference

import (
	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/matrix-org/waterfall/pkg/conference/participant"
	"github.com/matrix-org/waterfall/pkg/peer"
	"github.com/matrix-org/waterfall/pkg/webrtc_ext"
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix/event"
)

// A single conference. Call and conference mean the same in context of Matrix.
type Conference struct {
	id             string
	config         Config
	logger         *logrus.Entry
	conferenceDone chan<- struct{}

	connectionFactory *webrtc_ext.PeerConnectionFactory
	matrixWorker      *matrixWorker

	tracker         participant.Tracker
	streamsMetadata event.CallSDPStreamMetadata

	peerMessages chan common.Message[participant.ID, peer.MessageContent]
	matrixEvents <-chan MatrixMessage
}

func (c *Conference) getParticipant(id participant.ID) *participant.Participant {
	if participant := c.tracker.GetParticipant(id); participant != nil {
		return participant
	}

	c.logger.Errorf("Participant not found: %s (%s)", id.UserID, id.DeviceID)
	return nil
}

// Helper to terminate and remove a participant from the conference.
func (c *Conference) removeParticipant(id participant.ID) {
	// Remove the participant and then remove its streams from the map.
	for streamID := range c.tracker.RemoveParticipant(id) {
		delete(c.streamsMetadata, streamID)
	}

	// Inform the other participants about updated metadata (since the participant left
	// the corresponding streams of the participant are no longer available, so we're informing
	// others about it).
	c.resendMetadataToAllExcept(id)
}

// Helper to get the list of available streams for a given participant, i.e. the list of streams
// that a given participant **can subscribe to**. Each stream may have multiple tracks.
func (c *Conference) getAvailableStreamsFor(forParticipant participant.ID) event.CallSDPStreamMetadata {
	streamsMetadata := make(event.CallSDPStreamMetadata)

	c.tracker.ForEachPublishedTrack(func(trackID participant.TrackID, track participant.PublishedTrack) {
		// Skip us. As we know about our own tracks.
		if track.Owner != forParticipant {
			streamID := track.Info.StreamID
			kind := track.Info.Kind.String()

			if metadata, ok := streamsMetadata[streamID]; ok {
				metadata.Tracks[trackID] = event.CallSDPStreamMetadataTrack{
					Kind: kind,
				}
				streamsMetadata[streamID] = metadata
			} else if metadata, ok := c.streamsMetadata[streamID]; ok {
				metadata.Tracks = event.CallSDPStreamMetadataTracks{
					trackID: event.CallSDPStreamMetadataTrack{
						Kind: kind,
					},
				}
				streamsMetadata[streamID] = metadata
			} else {
				c.logger.Warnf("Don't have metadata for %s", trackID)
			}
		}
	})

	return streamsMetadata
}

// Helper that returns the list of published tracks inside this conference that match the given track IDs.
func (c *Conference) findPublishedTracks(trackIDs []string) map[string]participant.PublishedTrack {
	tracks := make(map[string]participant.PublishedTrack)
	for _, identifier := range trackIDs {
		// Check if this participant has any of the tracks that we're looking for.
		if track := c.tracker.FindPublishedTrack(identifier); track != nil {
			tracks[identifier] = *track
		} else {
			c.logger.Warnf("track not found: %s", identifier)
		}
	}

	return tracks
}

// Helper that sends current metadata about all available tracks to all participants except a given one.
func (c *Conference) resendMetadataToAllExcept(exceptMe participant.ID) {
	c.tracker.ForEachParticipant(func(id participant.ID, participant *participant.Participant) {
		if id != exceptMe {
			participant.SendDataChannelMessage(event.Event{
				Type: event.FocusCallSDPStreamMetadataChanged,
				Content: event.Content{
					Parsed: event.FocusCallSDPStreamMetadataChangedEventContent{
						SDPStreamMetadata: c.getAvailableStreamsFor(id),
					},
				},
			})
		}
	})
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
		c.tracker.UpdatePublishedTrackMetadata(trackID, metadata)
	}
}

func streamIntoTrackMetadata(
	streamMetadata event.CallSDPStreamMetadata,
) map[participant.TrackID]participant.TrackMetadata {
	tracksMetadata := make(map[participant.TrackID]participant.TrackMetadata)
	for _, metadata := range streamMetadata {
		for id, track := range metadata.Tracks {
			tracksMetadata[id] = participant.TrackMetadata{
				MaxWidth:  track.Width,
				MaxHeight: track.Height,
			}
		}
	}

	return tracksMetadata
}

func (c *Conference) newLogger(id participant.ID) *logrus.Entry {
	return c.logger.WithFields(logrus.Fields{
		"user_id":   id.UserID,
		"device_id": id.DeviceID,
	})
}
