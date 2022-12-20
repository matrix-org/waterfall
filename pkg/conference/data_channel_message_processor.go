package conference

import (
	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/matrix-org/waterfall/pkg/peer"
	"github.com/thoas/go-funk"
	"maunium.net/go/mautrix/event"
)

// Handle the `FocusEvent` from the DataChannel message.
func (c *Conference) processTrackSubscriptionDCMessage(
	participant *Participant, msg event.FocusCallTrackSubscriptionEventContent,
) {
	participant.logger.Debug("Received track subscription request over DC")

	// Extract IDs of the tracks we wish to unsubscribe from.
	toUnsubscribeTrackIDs := make([]string, 0, len(msg.Unsubscribe))
	for _, track := range msg.Unsubscribe {
		toUnsubscribeTrackIDs = append(toUnsubscribeTrackIDs, track.TrackID)
	}

	// Extract IDs and desired resolution of tracks we want to subscribe to.
	toSubscribeTrackIDs := make([]string, 0, len(msg.Subscribe))
	toSubscribeRequirements := make(map[string]TrackMetadata, len(msg.Subscribe))
	for _, track := range msg.Subscribe {
		toSubscribeTrackIDs = append(toSubscribeTrackIDs, track.TrackID)
		toSubscribeRequirements[track.TrackID] = TrackMetadata{track.Width, track.Height}
	}

	// Calculate the list of tracks we need to subscribe and unsubscribe from based on the requirements.
	subscribeTo := []peer.ExtendedTrackInfo{}

	// Iterate over all published tracks that correspond to the track IDs we want to subscribe to.
	for id, track := range c.getTracksInfo(toSubscribeTrackIDs) {
		// Get tre tracks we're subscribed to.
		alreadySubscribedTo := participant.peer.GetSubscribedTracks()

		// Let's find out if we're already subscribed to a given track.
		subscribedTrack, alreadySubscribed := alreadySubscribedTo[id]

		// Calculate the desired simulcast layer if any.
		requirements := toSubscribeRequirements[id]
		desiredLayer := track.getDesiredLayer(requirements.maxWidth, requirements.maxHeight)

		// If we're not subscribed to the track, let's subscribe to it respecting
		// the desired track parameters that the user specified in a request.
		if !alreadySubscribed {
			subscribeTo = append(subscribeTo, peer.ExtendedTrackInfo{track.TrackInfo, desiredLayer})
			continue
		}

		// If we're already subscribed to a given track ID, then we can ignore the request, unless
		// we're subscribed to a different simulcast layer of the track, in which case we know that
		// the user wants to switch to a different simulcast layer: then we check if the given simulcast
		// layer is available at all and only if it's available, we switch, otherwise we ignore the request.
		if subscribedTrack.Layer != desiredLayer && funk.Contains(track.availableLayers, desiredLayer) {
			// If we're already subscribed, but to a different simulcast layer, then we need to remove the track.
			toUnsubscribeTrackIDs = append(toUnsubscribeTrackIDs, id)
			// And add again, this time with a proper simulcast layer.
			subscribeTo = append(subscribeTo, peer.ExtendedTrackInfo{track.TrackInfo, desiredLayer})
			continue
		}

		participant.logger.Warnf("Ignoring track subscription request for %s: already subscribed", id)
	}

	participant.peer.UnsubscribeFrom(toUnsubscribeTrackIDs)
	participant.peer.SubscribeTo(subscribeTo)
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
	// New heartbeat received (keep-alive message that is periodically sent by the remote peer).
	// We need to update the last heartbeat time. If the peer is not active for too long, we will
	// consider peer's connection as stalled and will close it.
	participant.heartbeatPong <- common.Pong{}
}

func (c *Conference) processMetadataDCMessage(
	participant *Participant, msg event.FocusCallSDPStreamMetadataChangedEventContent,
) {
	participant.streamMetadata = msg.SDPStreamMetadata
	c.resendMetadataToAllExcept(participant.id)
}
