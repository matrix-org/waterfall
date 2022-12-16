package conference

import (
	"time"

	"github.com/matrix-org/waterfall/pkg/peer"
	"github.com/thoas/go-funk"
	"maunium.net/go/mautrix/event"
)

func (c *Conference) processJoinedTheCallMessage(participant *Participant, message peer.JoinedTheCall) {
	participant.logger.Info("Joined the call")
}

func (c *Conference) processLeftTheCallMessage(participant *Participant, msg peer.LeftTheCall) {
	participant.logger.Infof("Left the call: %s", msg.Reason)
	c.removeParticipant(participant.id)
	c.signaling.SendHangup(participant.asMatrixRecipient(), msg.Reason)
}

func (c *Conference) processNewTrackPublishedMessage(participant *Participant, msg peer.NewTrackPublished) {
	participant.logger.Infof("Published new track: %s (%s)", msg.TrackID, msg.Layer.String())

	if track, ok := participant.publishedTracks[msg.TrackID]; !ok {
		participant.publishedTracks[msg.TrackID] = PublishedTrack{
			info:   msg.TrackInfo,
			layers: []peer.SimulcastLayer{msg.Layer},
		}
	} else if !funk.Contains(track.layers, msg.Layer) {
		track.layers = append(track.layers, msg.Layer)
		participant.publishedTracks[msg.TrackID] = track
	}

	// TODO: Oops, that's not very efficient with the simulcast since it means that when 3
	// layers are published, we will send 3 times the same metadata.
	c.resendMetadataToAllExcept(participant.id)
}

func (c *Conference) processRTPPacketReceivedMessage(participant *Participant, msg peer.RTPPacketReceived) {
	// For now we just forward the lowest layer always and assume that others don't exist.
	if msg.Layer != peer.SimulcastLayerLow {
		// TODO: Very inefficient, use map later on a conference level to improve performance.
		for _, participant := range c.participants {
			tracks := participant.peer.GetSubscribedTracks()
			if funk.Contains(tracks, func(info peer.TrackInfo) bool { return info.TrackID == msg.TrackID }) {
				participant.peer.WriteRTP(msg.TrackID, msg.Packet)
			}
		}
	}
}

func (c *Conference) processPublishedTrackFailedMessage(participant *Participant, msg peer.PublishedTrackFailed) {
	participant.logger.Infof("Failed published track: %s", msg.TrackID)
	delete(participant.publishedTracks, msg.TrackID)

	for _, otherParticipant := range c.participants {
		if otherParticipant.id == participant.id {
			continue
		}

		otherParticipant.peer.UnsubscribeFrom([]peer.TrackInfo{msg.TrackInfo})
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
	var focusEvent event.Event
	if err := focusEvent.UnmarshalJSON([]byte(msg.Message)); err != nil {
		c.logger.Errorf("Failed to unmarshal data channel message: %v", err)
		return
	}

	participant.logger.Debugf("Received data channel message: %v", focusEvent.Type.Type)

	// FIXME: We should be able to do
	// focusEvent.Content.ParseRaw(focusEvent.Type) but it throws an error.
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
				} else {
					c.logger.Errorf("Failed to send RTCP packets: %v", err)
				}
			}
		}
	}
}
