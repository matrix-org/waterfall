package conference

import (
	"encoding/json"
	"errors"

	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/matrix-org/waterfall/pkg/peer"
	"github.com/pion/webrtc/v3"
	"maunium.net/go/mautrix/event"
)

// Listen on messages from incoming channels and process them.
// This is essentially the main loop of the conference.
// If this function returns, the conference is over.
func (c *Conference) processMessages() {
	for {
		select {
		case msg := <-c.peerMessages:
			c.processPeerMessage(msg)
		case msg := <-c.matrixMessages.Channel:
			c.processMatrixMessage(msg)
		}

		// If there are no more participants, stop the conference.
		if len(c.participants) == 0 {
			c.logger.Info("No more participants, stopping the conference")
			// Close the channel so that the sender can't push any messages.
			c.matrixMessages.Close()

			// Let's read remaining messages from the channel (otherwise the caller will be
			// blocked in case of unbuffered channels).
			var message *MatrixMessage
			select {
			case msg := <-c.matrixMessages.Channel:
				*message = msg
			default:
				// Ok, no messages in the queue, nice.
			}

			// Send the information that we ended to the owner and pass the message
			// that we did not process (so that we don't drop it silently).
			c.endNotifier.Notify(message)
			return
		}
	}
}

// Process a message from a local peer.
func (c *Conference) processPeerMessage(message common.Message[ParticipantID, peer.MessageContent]) {
	participant := c.getParticipant(message.Sender, errors.New("received a message from a deleted participant"))
	if participant == nil {
		return
	}

	// Since Go does not support ADTs, we have to use a switch statement to
	// determine the actual type of the message.
	switch msg := message.Content.(type) {
	case peer.JoinedTheCall:
		c.resendMetadataToAllExcept(participant.id)

	case peer.LeftTheCall:
		c.removeParticipant(message.Sender)
		c.signaling.SendHangup(participant.asMatrixRecipient(), event.CallHangupUnknownError)

	case peer.NewTrackPublished:
		key := event.SFUTrackDescription{
			StreamID: msg.Track.StreamID(),
			TrackID:  msg.Track.ID(),
		}

		if _, ok := participant.publishedTracks[key]; ok {
			c.logger.Errorf("Track already published: %v", key)
			return
		}

		participant.publishedTracks[key] = msg.Track

	case peer.PublishedTrackFailed:
		delete(participant.publishedTracks, event.SFUTrackDescription{
			StreamID: msg.Track.StreamID(),
			TrackID:  msg.Track.ID(),
		})

		for _, otherParticipant := range c.participants {
			if otherParticipant.id == participant.id {
				continue
			}

			otherParticipant.peer.UnsubscribeFrom([]*webrtc.TrackLocalStaticRTP{msg.Track})
		}

	case peer.NewICECandidate:
		// Convert WebRTC ICE candidate to Matrix ICE candidate.
		jsonCandidate := msg.Candidate.ToJSON()
		candidates := []event.CallCandidate{{
			Candidate:     jsonCandidate.Candidate,
			SDPMLineIndex: int(*jsonCandidate.SDPMLineIndex),
			SDPMID:        *jsonCandidate.SDPMid,
		}}
		c.signaling.SendICECandidates(participant.asMatrixRecipient(), candidates)

	case peer.ICEGatheringComplete:
		// Send an empty array of candidates to indicate that ICE gathering is complete.
		c.signaling.SendCandidatesGatheringFinished(participant.asMatrixRecipient())

	case peer.RenegotiationRequired:
		participant.sendDataChannelMessage(event.SFUMessage{
			Op:       event.SFUOperationOffer,
			SDP:      msg.Offer.SDP,
			Metadata: c.getAvailableStreamsFor(participant.id),
		})

	case peer.DataChannelMessage:
		var sfuMessage event.SFUMessage
		if err := json.Unmarshal([]byte(msg.Message), &sfuMessage); err != nil {
			c.logger.Errorf("Failed to unmarshal SFU message: %v", err)
			return
		}

		c.handleDataChannelMessage(participant, sfuMessage)

	case peer.DataChannelAvailable:
		participant.sendDataChannelMessage(event.SFUMessage{
			Op:       event.SFUOperationMetadata,
			Metadata: c.getAvailableStreamsFor(participant.id),
		})

	default:
		c.logger.Errorf("Unknown message type: %T", msg)
	}
}

// Handle the `SFUMessage` event from the DataChannel message.
func (c *Conference) handleDataChannelMessage(participant *Participant, sfuMessage event.SFUMessage) {
	switch sfuMessage.Op {
	case event.SFUOperationSelect:
		// Get the tracks that correspond to the tracks that the participant wants to receive.
		for _, track := range c.getTracks(sfuMessage.Start) {
			if err := participant.peer.SubscribeTo(track); err != nil {
				participant.logger.Errorf("Failed to subscribe to track: %v", err)
				return
			}
		}

	case event.SFUOperationAnswer:
		if err := participant.peer.ProcessSDPAnswer(sfuMessage.SDP); err != nil {
			participant.logger.Errorf("Failed to set SDP answer: %v", err)
			return
		}

	case event.SFUOperationPublish:
		answer, err := participant.peer.ProcessSDPOffer(sfuMessage.SDP)
		if err != nil {
			participant.logger.Errorf("Failed to set SDP offer: %v", err)
			return
		}

		participant.sendDataChannelMessage(event.SFUMessage{
			Op:  event.SFUOperationAnswer,
			SDP: answer.SDP,
		})

	case event.SFUOperationUnpublish:
		// TODO: Clarify the semantics of unpublish.
	case event.SFUOperationAlive:
		// FIXME: Handle the heartbeat message here (updating the last timestamp etc).
	case event.SFUOperationMetadata:
		participant.streamMetadata = sfuMessage.Metadata
		c.resendMetadataToAllExcept(participant.id)
	}
}

func (c *Conference) processMatrixMessage(msg MatrixMessage) {
	switch ev := msg.Content.(type) {
	case *event.CallInviteEventContent:
		c.onNewParticipant(ParticipantID{UserID: msg.UserID, DeviceID: ev.DeviceID}, ev)
	case *event.CallCandidatesEventContent:
		c.onCandidates(ParticipantID{UserID: msg.UserID, DeviceID: ev.DeviceID}, ev)
	case *event.CallSelectAnswerEventContent:
		c.onSelectAnswer(ParticipantID{UserID: msg.UserID, DeviceID: ev.DeviceID}, ev)
	case *event.CallHangupEventContent:
		c.onHangup(ParticipantID{UserID: msg.UserID, DeviceID: ev.DeviceID}, ev)
	default:
		c.logger.Errorf("Unexpected event type: %T", ev)
	}
}
