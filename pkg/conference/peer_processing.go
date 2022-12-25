package conference

import (
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
	c.tracker.AddTrack(p.ID, msg.ExtendedTrackInfo, trackMetadata)
	c.resendMetadataToAllExcept(p.ID)
}

func (c *Conference) processRTPPacketReceivedMessage(p *participant.Participant, msg peer.RTPPacketReceived) {
	c.tracker.ProcessRTP(msg.ExtendedTrackInfo, msg.Packet)
}

func (c *Conference) processPublishedTrackFailedMessage(p *participant.Participant, msg peer.PublishedTrackFailed) {
	p.Logger.Infof("Failed published track: %s", msg.TrackID)
	c.tracker.RemoveTrack(msg.TrackID)
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
		c.processTrackSubscriptionDCMessage(p, *focusEvent.Content.AsFocusCallTrackSubscription())
	case event.FocusCallNegotiate.Type:
		focusEvent.Content.ParseRaw(event.FocusCallNegotiate)
		c.processNegotiateDCMessage(p, *focusEvent.Content.AsFocusCallNegotiate())
	case event.FocusCallPong.Type:
		focusEvent.Content.ParseRaw(event.FocusCallPong)
		c.processPongDCMessage(p)
	case event.FocusCallSDPStreamMetadataChanged.Type:
		focusEvent.Content.ParseRaw(event.FocusCallSDPStreamMetadataChanged)
		c.processMetadataDCMessage(p, *focusEvent.Content.AsFocusCallSDPStreamMetadataChanged())
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
	c.tracker.ProcessRTCP(p, msg.TrackID, msg.Packets)
}
