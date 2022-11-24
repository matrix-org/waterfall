package conference

import (
	"encoding/json"
	"errors"

	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/matrix-org/waterfall/pkg/peer"
	"maunium.net/go/mautrix/event"
)

func (c *Conference) processMessages() {
	for {
		// Read a message from the stream (of type peer.Message) and process it.
		message := <-c.peerEvents
		c.processPeerMessage(message)
	}
}

func (c *Conference) processPeerMessage(message common.Message[ParticipantID, peer.MessageContent]) {
	participant := c.getParticipant(message.Sender, errors.New("received a message from a deleted participant"))
	if participant == nil {
		return
	}

	// Since Go does not support ADTs, we have to use a switch statement to
	// determine the actual type of the message.
	switch msg := message.Content.(type) {
	case peer.JoinedTheCall:
	case peer.LeftTheCall:
		delete(c.participants, message.Sender)
		// TODO: Send new metadata about available streams to all participants.
		// TODO: Send the hangup event over the Matrix back to the user.

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

		// TODO: Should we remove the local tracks from every subscriber as well? Or will it happen automatically?

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
		toSend := event.SFUMessage{
			Op:       event.SFUOperationOffer,
			SDP:      msg.Offer.SDP,
			Metadata: c.getStreamsMetadata(participant.id),
		}

		participant.sendDataChannelMessage(toSend)

	case peer.DataChannelMessage:
		var sfuMessage event.SFUMessage
		if err := json.Unmarshal([]byte(msg.Message), &sfuMessage); err != nil {
			c.logger.Errorf("Failed to unmarshal SFU message: %v", err)
			return
		}

		c.handleDataChannelMessage(participant, sfuMessage)

	case peer.DataChannelAvailable:
		toSend := event.SFUMessage{
			Op:       event.SFUOperationMetadata,
			Metadata: c.getStreamsMetadata(participant.id),
		}

		if err := participant.sendDataChannelMessage(toSend); err != nil {
			c.logger.Errorf("Failed to send SFU message to open data channel: %v", err)
			return
		}

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
			if err := participant.peer.SubscribeToTrack(track); err != nil {
				c.logger.Errorf("Failed to subscribe to track: %v", err)
				return
			}
		}

	case event.SFUOperationAnswer:
		if err := participant.peer.NewSDPAnswerReceived(sfuMessage.SDP); err != nil {
			c.logger.Errorf("Failed to set SDP answer: %v", err)
			return
		}

	// TODO: Clarify the semantics of publish (just a new sdp offer?).
	case event.SFUOperationPublish:
	// TODO: Clarify the semantics of publish (how is it different from unpublish?).
	case event.SFUOperationUnpublish:
	// TODO: Handle the heartbeat message here (updating the last timestamp etc).
	case event.SFUOperationAlive:
	case event.SFUOperationMetadata:
		participant.streamMetadata = sfuMessage.Metadata

		// Inform all participants about new metadata available.
		for id, participant := range c.participants {
			// Skip ourselves.
			if id == participant.id {
				continue
			}

			toSend := event.SFUMessage{
				Op:       event.SFUOperationMetadata,
				Metadata: c.getStreamsMetadata(id),
			}

			if err := participant.sendDataChannelMessage(toSend); err != nil {
				c.logger.Errorf("Failed to send SFU message: %v", err)
				return
			}
		}
	}
}
