package conference

import (
	"encoding/json"
	"errors"

	"github.com/matrix-org/waterfall/src/peer"
	"maunium.net/go/mautrix/event"
)

func (c *Conference) processMessages() {
	for {
		// Read a message from the stream (of type peer.Message) and process it.
		message := <-c.peerEventsStream
		c.processPeerMessage(message)
	}
}

//nolint:funlen
func (c *Conference) processPeerMessage(message peer.Message) {
	// Since Go does not support ADTs, we have to use a switch statement to
	// determine the actual type of the message.
	switch msg := message.(type) {
	case peer.JoinedTheCall:
	case peer.LeftTheCall:
		delete(c.participants, msg.Sender)
		// TODO: Send new metadata about available streams to all participants.
		// TODO: Send the hangup event over the Matrix back to the user.

	case peer.NewTrackPublished:
		participant := c.getParticipant(msg.Sender, errors.New("New track published from unknown participant"))
		if participant == nil {
			return
		}

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
		participant := c.getParticipant(msg.Sender, errors.New("Published track failed from unknown participant"))
		if participant == nil {
			return
		}

		delete(participant.publishedTracks, event.SFUTrackDescription{
			StreamID: msg.Track.StreamID(),
			TrackID:  msg.Track.ID(),
		})

		// TODO: Should we remove the local tracks from every subscriber as well? Or will it happen automatically?

	case peer.NewICECandidate:
		participant := c.getParticipant(msg.Sender, errors.New("ICE candidate from unknown participant"))
		if participant == nil {
			return
		}

		// Convert WebRTC ICE candidate to Matrix ICE candidate.
		jsonCandidate := msg.Candidate.ToJSON()
		candidates := []event.CallCandidate{{
			Candidate:     jsonCandidate.Candidate,
			SDPMLineIndex: int(*jsonCandidate.SDPMLineIndex),
			SDPMID:        *jsonCandidate.SDPMid,
		}}
		c.signaling.SendICECandidates(participant.asMatrixRecipient(), candidates)

	case peer.ICEGatheringComplete:
		participant := c.getParticipant(msg.Sender, errors.New("Received ICE complete from unknown participant"))
		if participant == nil {
			return
		}

		// Send an empty array of candidates to indicate that ICE gathering is complete.
		c.signaling.SendCandidatesGatheringFinished(participant.asMatrixRecipient())

	case peer.RenegotiationRequired:
		participant := c.getParticipant(msg.Sender, errors.New("Renegotiation from unknown participant"))
		if participant == nil {
			return
		}

		toSend := event.SFUMessage{
			Op:       event.SFUOperationOffer,
			SDP:      msg.Offer.SDP,
			Metadata: c.getStreamsMetadata(participant.id),
		}

		participant.sendDataChannelMessage(toSend)

	case peer.DataChannelMessage:
		participant := c.getParticipant(msg.Sender, errors.New("Data channel message from unknown participant"))
		if participant == nil {
			return
		}

		var sfuMessage event.SFUMessage
		if err := json.Unmarshal([]byte(msg.Message), &sfuMessage); err != nil {
			c.logger.Errorf("Failed to unmarshal SFU message: %v", err)
			return
		}

		c.handleDataChannelMessage(participant, sfuMessage)

	case peer.DataChannelAvailable:
		participant := c.getParticipant(msg.Sender, errors.New("Data channel available from unknown participant"))
		if participant == nil {
			return
		}

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
