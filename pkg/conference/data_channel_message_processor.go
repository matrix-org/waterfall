package conference

import (
	"github.com/pion/webrtc/v3"
	"golang.org/x/exp/slices"
	"maunium.net/go/mautrix/event"
)

// Handle the `FocusEvent` from the DataChannel message.
func (c *Conference) processTrackSubscriptionDCMessage(
	participant *Participant, msg event.FocusCallTrackSubscriptionEventContent,
) {
	participant.logger.Debug("Received track subscription request over DC")

	if len(msg.Unsubscribe) != 0 {
		c.unsubscribeFromTracks(participant, msg.Unsubscribe)
	}
	if len(msg.Subscribe) != 0 {
		c.subscribeToTracks(participant, msg.Subscribe)
	}
}

func (c *Conference) unsubscribeFromTracks(participant *Participant, unsubscribe []event.FocusTrackDescription) {
	// Find tracksToUnsubscribeFrom based on what we were asked for.
	tracksToUnsubscribeFrom := c.getTracks(unsubscribe)

	// Let's check if we have all the tracks that we were asked for are there.
	// If not, we will list which are not available (later on we must inform participant
	// about it unless the participant retries it).
	if len(tracksToUnsubscribeFrom) != len(unsubscribe) {
		for _, expected := range unsubscribe {
			found := slices.IndexFunc(tracksToUnsubscribeFrom, func(track *webrtc.TrackLocalStaticRTP) bool {
				return track.ID() == expected.TrackID
			})

			if found == -1 {
				c.logger.Warnf("Track to unsubscribe from not found: %s", expected.TrackID)
			}
		}
	}

	// Unsubscribe from the found tracks.
	for _, track := range tracksToUnsubscribeFrom {
		participant.logger.WithField("track_id", track.ID()).Debug("Subscribing to track")
	}
	participant.peer.UnsubscribeFrom(tracksToUnsubscribeFrom)
}

func (c *Conference) subscribeToTracks(participant *Participant, subscribe []event.FocusTrackDescription) {
	// Find tracksToSubscribeTo based on what we were asked for.
	tracksToSubscribeTo := c.getTracks(subscribe)

	// Let's check if we have all the tracks that we were asked for are there.
	// If not, we will list which are not available (later on we must inform participant
	// about it unless the participant retries it).
	if len(tracksToSubscribeTo) != len(subscribe) {
		for _, expected := range subscribe {
			found := slices.IndexFunc(tracksToSubscribeTo, func(track *webrtc.TrackLocalStaticRTP) bool {
				return track.ID() == expected.TrackID
			})

			if found == -1 {
				c.logger.Warnf("Track to subscribe to found: %s", expected.TrackID)
			}
		}
	}

	// Subscribe to the found tracks.
	for _, track := range tracksToSubscribeTo {
		participant.logger.WithField("track_id", track.ID()).Debug("Subscribing to track")
		if err := participant.peer.SubscribeTo(track); err != nil {
			participant.logger.Errorf("Failed to subscribe to track: %v", err)
			return
		}
	}
}

func (c *Conference) processNegotiateDCMessage(participant *Participant, msg event.FocusCallNegotiateEventContent) {
	participant.streamMetadata = msg.SDPStreamMetadata

	switch msg.Description.Type {
	case event.CallDataTypeOffer:
		participant.logger.WithField("SDP", msg.Description.SDP).Trace("Received SDP offer over DC")

		answer, err := participant.peer.ProcessSDPOffer(msg.Description.SDP)
		if err != nil {
			participant.logger.Errorf("Failed to set SDP offer: %v", err)
			return
		}

		participant.sendDataChannelMessage(event.Event{
			Type: event.FocusCallNegotiate,
			Content: event.Content{
				Parsed: event.FocusCallNegotiateEventContent{
					Description: event.CallData{
						Type: event.CallDataType(answer.Type.String()),
						SDP:  answer.SDP,
					},
					SDPStreamMetadata: c.getAvailableStreamsFor(participant.id),
				},
			},
		})
	case event.CallDataTypeAnswer:
		participant.logger.WithField("SDP", msg.Description.SDP).Trace("Received SDP answer over DC")

		if err := participant.peer.ProcessSDPAnswer(msg.Description.SDP); err != nil {
			participant.logger.Errorf("Failed to set SDP answer: %v", err)
			return
		}
	default:
		participant.logger.Errorf("Unknown SDP description type")
	}
}

func (c *Conference) processPongDCMessage(participant *Participant) {
	participant.peer.ProcessPong()
}

func (c *Conference) processMetadataDCMessage(
	participant *Participant, msg event.FocusCallSDPStreamMetadataChangedEventContent,
) {
	participant.streamMetadata = msg.SDPStreamMetadata
	c.resendMetadataToAllExcept(participant.id)
}
