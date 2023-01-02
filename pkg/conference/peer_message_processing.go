package conference

import (
	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/matrix-org/waterfall/pkg/conference/participant"
	"github.com/matrix-org/waterfall/pkg/peer"
	"maunium.net/go/mautrix/event"
)

func (c *Conference) processJoinedTheCallMessage(p *participant.Participant, message peer.JoinedTheCall) {
	p.Logger.Info("Joined the call")
}

func (c *Conference) processLeftTheCallMessage(p *participant.Participant, msg peer.LeftTheCall) {
	p.Logger.Infof("Left the call: %s", msg.Reason)
	c.removeParticipant(p.ID)
	c.signaling.SendHangup(p.AsMatrixRecipient(), msg.Reason)
}

func (c *Conference) processNewTrackPublishedMessage(p *participant.Participant, msg peer.NewTrackPublished) {
	p.Logger.Infof("Published new track: %s (%v)", msg.TrackID, msg.Layer)

	// Find metadata for a given track.
	trackMetadata := streamIntoTrackMetadata(c.streamsMetadata)[msg.TrackID]

	// If a new track has been published, we inform everyone about new track available.
	c.tracker.AddPublishedTrack(p.ID, msg.TrackInfo, trackMetadata)
	c.resendMetadataToAllExcept(p.ID)
}

func (c *Conference) processRTPPacketReceivedMessage(p *participant.Participant, msg peer.RTPPacketReceived) {
	c.tracker.ProcessRTP(msg.TrackInfo, msg.Packet)
}

func (c *Conference) processPublishedTrackFailedMessage(p *participant.Participant, msg peer.PublishedTrackFailed) {
	p.Logger.Infof("Failed published track: %s", msg.TrackID)
	c.tracker.RemovePublishedTrack(msg.TrackID)
	c.resendMetadataToAllExcept(p.ID)
}

func (c *Conference) processNewICECandidateMessage(p *participant.Participant, msg peer.NewICECandidate) {
	p.Logger.Debug("Received a new local ICE candidate")

	// Convert WebRTC ICE candidate to Matrix ICE candidate.
	jsonCandidate := msg.Candidate.ToJSON()
	candidates := []event.CallCandidate{{
		Candidate:     jsonCandidate.Candidate,
		SDPMLineIndex: int(*jsonCandidate.SDPMLineIndex),
		SDPMID:        *jsonCandidate.SDPMid,
	}}
	c.signaling.SendICECandidates(p.AsMatrixRecipient(), candidates)
}

func (c *Conference) processICEGatheringCompleteMessage(p *participant.Participant, msg peer.ICEGatheringComplete) {
	p.Logger.Debug("Local ICE gathering completed")

	// Send an empty array of candidates to indicate that ICE gathering is complete.
	c.signaling.SendCandidatesGatheringFinished(p.AsMatrixRecipient())
}

func (c *Conference) processRenegotiationRequiredMessage(p *participant.Participant, msg peer.RenegotiationRequired) {
	p.Logger.Info("Renegotiation started, sending SDP offer")
	p.SendDataChannelMessage(event.Event{
		Type: event.FocusCallNegotiate,
		Content: event.Content{
			Parsed: event.FocusCallNegotiateEventContent{
				Description: event.CallData{
					Type: event.CallDataType(msg.Offer.Type.String()),
					SDP:  msg.Offer.SDP,
				},
				SDPStreamMetadata: c.getAvailableStreamsFor(p.ID),
			},
		},
	})
}

func (c *Conference) processDataChannelMessage(p *participant.Participant, msg peer.DataChannelMessage) {
	var focusEvent event.Event
	if err := focusEvent.UnmarshalJSON([]byte(msg.Message)); err != nil {
		c.logger.Errorf("Failed to unmarshal data channel message: %v", err)
		return
	}

	p.Logger.Debugf("Received data channel message: %v", focusEvent.Type.Type)

	// FIXME: We should be able to do
	// focusEvent.Content.ParseRaw(focusEvent.Type) but it throws an error.
	switch focusEvent.Type.Type {
	case event.FocusCallTrackSubscription.Type:
		focusEvent.Content.ParseRaw(event.FocusCallTrackSubscription)
		c.processTrackSubscriptionMessage(p, *focusEvent.Content.AsFocusCallTrackSubscription())
	case event.FocusCallNegotiate.Type:
		focusEvent.Content.ParseRaw(event.FocusCallNegotiate)
		c.processNegotiateMessage(p, *focusEvent.Content.AsFocusCallNegotiate())
	case event.FocusCallPong.Type:
		focusEvent.Content.ParseRaw(event.FocusCallPong)
		c.processPongMessage(p)
	case event.FocusCallSDPStreamMetadataChanged.Type:
		focusEvent.Content.ParseRaw(event.FocusCallSDPStreamMetadataChanged)
		c.processMetadataMessage(p, *focusEvent.Content.AsFocusCallSDPStreamMetadataChanged())
	default:
		p.Logger.WithField("type", focusEvent.Type.Type).Warn("Received data channel message of unknown type")
	}
}

func (c *Conference) processDataChannelAvailableMessage(p *participant.Participant, msg peer.DataChannelAvailable) {
	p.Logger.Info("Connected data channel")
	p.SendDataChannelMessage(event.Event{
		Type: event.FocusCallSDPStreamMetadataChanged,
		Content: event.Content{
			Parsed: event.FocusCallSDPStreamMetadataChangedEventContent{
				SDPStreamMetadata: c.getAvailableStreamsFor(p.ID),
			},
		},
	})
}

func (c *Conference) processRTCPPackets(p *participant.Participant, msg peer.RTCPReceived) {
	if err := c.tracker.ProcessRTCP(msg.TrackInfo, msg.Packets); err != nil {
		p.Logger.Errorf("Failed to process RTCP on %s (%v): %v", msg.TrackID, msg.Layer, err)
	}
}

// Handle the `FocusEvent` from the DataChannel message.
func (c *Conference) processTrackSubscriptionMessage(
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
	subscribeTo := []peer.TrackInfo{}

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
			track.Info.Layer = desiredLayer
			subscribeTo = append(subscribeTo, track.Info)
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
			track.Info.Layer = desiredLayer
			subscribeTo = append(subscribeTo, track.Info)
			continue
		}

		p.Logger.Warnf("Ignoring track subscription request for %s: already subscribed", id)
	}

	c.tracker.Unsubscribe(p.ID, toUnsubscribeTrackIDs)
	c.tracker.Subscribe(p.ID, subscribeTo)
}

func (c *Conference) processNegotiateMessage(p *participant.Participant, msg event.FocusCallNegotiateEventContent) {
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

func (c *Conference) processPongMessage(p *participant.Participant) {
	// New heartbeat received (keep-alive message that is periodically sent by the remote peer).
	// We need to update the last heartbeat time. If the peer is not active for too long, we will
	// consider peer's connection as stalled and will close it.
	p.HeartbeatPong <- common.Pong{}
}

func (c *Conference) processMetadataMessage(
	p *participant.Participant,
	msg event.FocusCallSDPStreamMetadataChangedEventContent,
) {
	c.updateMetadata(msg.SDPStreamMetadata)
	c.resendMetadataToAllExcept(p.ID)
}
