package conference

import (
	"maunium.net/go/mautrix/event"
)

// Handle the `FocusEvent` from the DataChannel message.
func (c *Conference) processTrackSubscriptionDCMessage(
	participant *Participant, msg event.FocusCallTrackSubscriptionEventContent,
) {
	participant.logger.Debug("Received track subscription request over DC")

	if len(msg.Unsubscribe) != 0 {
		participant.peer.UnsubscribeFrom(c.getTracks(msg.Unsubscribe))
	}
	if len(msg.Subscribe) != 0 {
		participant.peer.SubscribeTo(c.getTracks(msg.Subscribe))
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
