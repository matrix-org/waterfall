package conference

import (
	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/matrix-org/waterfall/pkg/conference/participant"
	"github.com/matrix-org/waterfall/pkg/peer"
	"maunium.net/go/mautrix/event"
)

// Handle the `FocusEvent` from the DataChannel message.
func (c *Conference) processTrackSubscriptionDCMessage(
	p *participant.Participant,
	msg event.FocusCallTrackSubscriptionEventContent,
) {
	p.Logger.Debug("Received track subscription request over DC")

	// Extract IDs of the tracks we wish to unsubscribe from.
	toUnsubscribeTrackIDs := make([]string, 0, len(msg.Unsubscribe))
	for _, track := range msg.Unsubscribe {
		toUnsubscribeTrackIDs = append(toUnsubscribeTrackIDs, track.TrackID)
	}

	// Extract IDs and desired resolution of tracks we want to subscribe to.
	toSubscribeTrackIDs := make([]string, 0, len(msg.Subscribe))
	toSubscribeRequirements := make(map[string]participant.TrackMetadata)
	for _, track := range msg.Subscribe {
		toSubscribeTrackIDs = append(toSubscribeTrackIDs, track.TrackID)
		toSubscribeRequirements[track.TrackID] = participant.TrackMetadata{track.Width, track.Height}
	}

	// Calculate the list of tracks we need to subscribe and unsubscribe from based on the requirements.
	subscribeTo := []peer.ExtendedTrackInfo{}

	// Iterate over all published tracks that correspond to the track IDs we want to subscribe to.
	for id, track := range c.findPublishedTracks(toSubscribeTrackIDs) {
		// Check if we have a subscription for this track already.
		subscription := c.tracker.GetSubscriber(id, p.ID)

		// Calculate the desired simulcast layer if any.
		requirements := toSubscribeRequirements[id]
		desiredLayer := track.GetDesiredLayer(requirements.MaxWidth, requirements.MaxHeight)

		// If we're not subscribed to the track, let's subscribe to it respecting
		// the desired track parameters that the user specified in a request.
		if subscription == nil {
			subscribeTo = append(subscribeTo, peer.ExtendedTrackInfo{track.Info, desiredLayer})
			continue
		}

		// If we're already subscribed to a given track ID, then we can ignore the request, unless
		// we're subscribed to a different simulcast layer of the track, in which case we know that
		// the user wants to switch to a different simulcast layer: then we check if the given simulcast
		// layer is available at all and only if it's available, we switch, otherwise we ignore the request.
		if subscription.TrackInfo().Layer != desiredLayer {
			// If we're already subscribed, but to a different simulcast layer, then we need to remove the track.
			toUnsubscribeTrackIDs = append(toUnsubscribeTrackIDs, id)
			// And add again, this time with a proper simulcast layer.
			subscribeTo = append(subscribeTo, peer.ExtendedTrackInfo{track.Info, desiredLayer})
			continue
		}

		p.Logger.Warnf("Ignoring track subscription request for %s: already subscribed", id)
	}

	c.tracker.Unsubscribe(p.ID, toUnsubscribeTrackIDs)
	c.tracker.Subscribe(p.ID, subscribeTo)
}

func (c *Conference) processNegotiateDCMessage(p *participant.Participant, msg event.FocusCallNegotiateEventContent) {
	c.updateMetadata(msg.SDPStreamMetadata)

	switch msg.Description.Type {
	case event.CallDataTypeOffer:
		p.Logger.Info("New offer from peer received")
		p.Logger.WithField("SDP", msg.Description.SDP).Trace("Received SDP offer over DC")

		answer, err := p.Peer.ProcessSDPOffer(msg.Description.SDP)
		if err != nil {
			p.Logger.Errorf("Failed to set SDP offer: %v", err)
			return
		}

		p.SendDataChannelMessage(event.Event{
			Type: event.FocusCallNegotiate,
			Content: event.Content{
				Parsed: event.FocusCallNegotiateEventContent{
					Description: event.CallData{
						Type: event.CallDataType(answer.Type.String()),
						SDP:  answer.SDP,
					},
					SDPStreamMetadata: c.getAvailableStreamsFor(p.ID),
				},
			},
		})
	case event.CallDataTypeAnswer:
		p.Logger.Info("Renegotiation answer received")
		p.Logger.WithField("SDP", msg.Description.SDP).Trace("Received SDP answer over DC")

		if err := p.Peer.ProcessSDPAnswer(msg.Description.SDP); err != nil {
			p.Logger.Errorf("Failed to set SDP answer: %v", err)
			return
		}
	default:
		p.Logger.Errorf("Unknown SDP description type")
	}
}

func (c *Conference) processPongDCMessage(p *participant.Participant) {
	// New heartbeat received (keep-alive message that is periodically sent by the remote peer).
	// We need to update the last heartbeat time. If the peer is not active for too long, we will
	// consider peer's connection as stalled and will close it.
	p.HeartbeatPong <- common.Pong{}
}

func (c *Conference) processMetadataDCMessage(
	p *participant.Participant,
	msg event.FocusCallSDPStreamMetadataChangedEventContent,
) {
	c.updateMetadata(msg.SDPStreamMetadata)
	c.resendMetadataToAllExcept(p.ID)
}
