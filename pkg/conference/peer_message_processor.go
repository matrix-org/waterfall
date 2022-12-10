package conference

import (
	"time"

	"github.com/matrix-org/waterfall/pkg/peer"
	"github.com/pion/webrtc/v3"
	"maunium.net/go/mautrix/event"
)

func (c *Conference) processJoinedTheCallMessage(participant *Participant, message peer.JoinedTheCall) {
	participant.logger.Info("Joined the call")
}

func (c *Conference) processLeftTheCallMessage(participant *Participant, msg peer.LeftTheCall) {
	participant.logger.Info("Left the call: %s", msg.Reason)
	c.removeParticipant(participant.id)
	c.signaling.SendHangup(participant.asMatrixRecipient(), msg.Reason)
}

func (c *Conference) processNewTrackPublishedMessage(participant *Participant, msg peer.NewTrackPublished) {
	participant.logger.Infof("Published new track: %s", msg.Track.ID())

	if _, ok := participant.publishedTracks[msg.Track.ID()]; ok {
		c.logger.Errorf("Track already published: %v", msg.Track.ID())
		return
	}

	participant.publishedTracks[msg.Track.ID()] = PublishedTrack{track: msg.Track}
	c.resendMetadataToAllExcept(participant.id)
}

func (c *Conference) processPublishedTrackFailedMessage(participant *Participant, msg peer.PublishedTrackFailed) {
	participant.logger.Infof("Failed published track: %s", msg.Track.ID())
	delete(participant.publishedTracks, msg.Track.ID())

	for _, otherParticipant := range c.participants {
		if otherParticipant.id == participant.id {
			continue
		}

		otherParticipant.peer.UnsubscribeFrom([]*webrtc.TrackLocalStaticRTP{msg.Track})
	}

	c.resendMetadataToAllExcept(participant.id)
}

func (c *Conference) processNewICECandidateMessage(participant *Participant, msg peer.NewICECandidate) {
	participant.logger.Debug("Received a new local ICE candidate")

	// Convert WebRTC ICE candidate to Matrix ICE candidate.
	jsonCandidate := msg.Candidate.ToJSON()
	candidates := []event.CallCandidate{{
		Candidate:     jsonCandidate.Candidate,
		SDPMLineIndex: int(*jsonCandidate.SDPMLineIndex),
		SDPMID:        *jsonCandidate.SDPMid,
	}}
	c.signaling.SendICECandidates(participant.asMatrixRecipient(), candidates)
}

func (c *Conference) processICEGatheringCompleteMessage(participant *Participant, msg peer.ICEGatheringComplete) {
	participant.logger.Info("Completed local ICE gathering")

	// Send an empty array of candidates to indicate that ICE gathering is complete.
	c.signaling.SendCandidatesGatheringFinished(participant.asMatrixRecipient())
}

func (c *Conference) processRenegotiationRequiredMessage(participant *Participant, msg peer.RenegotiationRequired) {
	participant.logger.Info("Started renegotiation")
	participant.sendDataChannelMessage(event.Event{
		Type: event.FocusCallNegotiate,
		Content: event.Content{
			Parsed: event.FocusCallNegotiateEventContent{
				Description: event.CallData{
					Type: event.CallDataType(msg.Offer.Type.String()),
					SDP:  msg.Offer.SDP,
				},
				SDPStreamMetadata: c.getAvailableStreamsFor(participant.id),
			},
		},
	})
}

func (c *Conference) processDataChannelMessage(participant *Participant, msg peer.DataChannelMessage) {
	participant.logger.Debug("Received data channel message")
	var focusEvent event.Event
	if err := focusEvent.UnmarshalJSON([]byte(msg.Message)); err != nil {
		c.logger.Errorf("Failed to unmarshal SFU message: %v", err)
		return
	}

	switch focusEvent.Type.Type {
	case event.FocusCallTrackSubscription.Type:
		focusEvent.Content.ParseRaw(event.FocusCallTrackSubscription)
		c.processTrackSubscriptionDCMessage(participant, *focusEvent.Content.AsFocusCallTrackSubscription())
	case event.FocusCallNegotiate.Type:
		focusEvent.Content.ParseRaw(event.FocusCallNegotiate)
		c.processNegotiateDCMessage(participant, *focusEvent.Content.AsFocusCallNegotiate())
	case event.FocusCallPong.Type:
		focusEvent.Content.ParseRaw(event.FocusCallPong)
		c.processPongDCMessage(participant)
	case event.FocusCallSDPStreamMetadataChanged.Type:
		focusEvent.Content.ParseRaw(event.FocusCallSDPStreamMetadataChanged)
		c.processMetadataDCMessage(participant, *focusEvent.Content.AsFocusCallSDPStreamMetadataChanged())
	default:
		participant.logger.WithField("type", focusEvent.Type.Type).Warn("Received data channel message of unknown type")
	}
}

func (c *Conference) processDataChannelAvailableMessage(participant *Participant, msg peer.DataChannelAvailable) {
	participant.logger.Info("Connected data channel")
	participant.sendDataChannelMessage(event.Event{
		Type: event.FocusCallSDPStreamMetadataChanged,
		Content: event.Content{
			Parsed: event.FocusCallSDPStreamMetadataChangedEventContent{
				SDPStreamMetadata: c.getAvailableStreamsFor(participant.id),
			},
		},
	})
}

func (c *Conference) processRTCPPackets(msg peer.RTCPReceived) {
	const sendKeyFrameInterval = 500 * time.Millisecond

	for _, participant := range c.participants {
		if published, ok := participant.publishedTracks[msg.TrackID]; ok {
			if published.canSendKeyframeAt.Before(time.Now()) {
				if err := participant.peer.WriteRTCP(msg.TrackID, msg.Packets); err == nil {
					published.canSendKeyframeAt = time.Now().Add(sendKeyFrameInterval)
				}
			}
		}
	}
}
