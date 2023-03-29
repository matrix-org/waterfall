package conference

import (
	"github.com/matrix-org/waterfall/pkg/channel"
	"github.com/matrix-org/waterfall/pkg/conference/participant"
	published "github.com/matrix-org/waterfall/pkg/conference/track"
	"github.com/matrix-org/waterfall/pkg/peer"
	"github.com/matrix-org/waterfall/pkg/telemetry"
	"github.com/matrix-org/waterfall/pkg/webrtc_ext"
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix/event"
)

// A single conference. Call and conference mean the same in context of Matrix.
type Conference struct {
	id     string
	config Config

	logger    *logrus.Entry
	telemetry *telemetry.Telemetry

	connectionFactory *webrtc_ext.PeerConnectionFactory
	matrixWorker      *matrixWorker

	tracker         *participant.Tracker
	streamsMetadata event.CallSDPStreamMetadata

	peerMessages          chan channel.Message[participant.ID, peer.MessageContent]
	matrixEvents          <-chan MatrixMessage
	publishedTrackStopped <-chan participant.TrackStoppedMessage
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

	c.tracker.ForEachPublishedTrackInfo(func(owner participant.ID, info webrtc_ext.TrackInfo) {
		// Skip us. As we know about our own tracks.
		if owner != forParticipant {
			streamID := info.StreamID
			kind := info.Kind.String()

			if metadata, ok := streamsMetadata[streamID]; ok {
				metadata.Tracks[info.TrackID] = event.CallSDPStreamMetadataTrack{
					Kind: kind,
				}
				streamsMetadata[streamID] = metadata
			} else if metadata, ok := c.streamsMetadata[streamID]; ok {
				metadata.Tracks = event.CallSDPStreamMetadataTracks{
					info.TrackID: event.CallSDPStreamMetadataTrack{
						Kind: kind,
					},
				}
				streamsMetadata[streamID] = metadata
			} else {
				c.logger.Warnf("Don't have metadata for %s", info.TrackID)
			}
		}
	})

	return streamsMetadata
}

// Helper that sends current metadata about all available tracks to all participants except a given one.
func (c *Conference) resendMetadataToAllExcept(exceptMe participant.ID) {
	c.tracker.ForEachParticipant(func(id participant.ID, participant *participant.Participant) {
		metadataEvent := event.Event{
			Type: event.FocusCallSDPStreamMetadataChanged,
			Content: event.Content{
				Parsed: event.FocusCallSDPStreamMetadataChangedEventContent{
					SDPStreamMetadata: c.getAvailableStreamsFor(id),
				},
			},
		}

		if id != exceptMe {
			if err := participant.SendOverDataChannel(metadataEvent); err != nil {
				c.logger.WithError(err).Errorf("Failed to send metadata to %s", id)
			}
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
) map[published.TrackID]published.TrackMetadata {
	tracksMetadata := make(map[published.TrackID]published.TrackMetadata)
	for _, metadata := range streamMetadata {
		for id, track := range metadata.Tracks {
			// Determine if a given track is muted.
			var muted bool
			switch track.Kind {
			case "audio":
				muted = metadata.AudioMuted
			case "video":
				muted = metadata.VideoMuted
			}

			tracksMetadata[id] = published.TrackMetadata{
				MaxWidth:  track.Width,
				MaxHeight: track.Height,
				Muted:     muted,
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
