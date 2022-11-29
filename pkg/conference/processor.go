package conference

import (
	"encoding/json"
	"errors"

	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/matrix-org/waterfall/pkg/peer"
	"github.com/pion/webrtc/v3"
	"golang.org/x/exp/slices"
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
			unreadMessages := c.matrixMessages.Close()

			// Send the information that we ended to the owner and pass the message
			// that we did not process (so that we don't drop it silently).
			c.endNotifier.Notify(unreadMessages)
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
		participant.logger.Info("Joined the call")
		c.resendMetadataToAllExcept(participant.id)

	case peer.LeftTheCall:
		participant.logger.Info("Left the call")
		c.removeParticipant(message.Sender)
		c.signaling.SendHangup(participant.asMatrixRecipient(), event.CallHangupUnknownError)

	case peer.NewTrackPublished:
		participant.logger.Infof("Published new track: %s", msg.Track.ID())
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
		participant.logger.Infof("Failed published track: %s", msg.Track.ID())
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
		participant.logger.Debug("Received a new local ICE candidate")

		// Convert WebRTC ICE candidate to Matrix ICE candidate.
		jsonCandidate := msg.Candidate.ToJSON()
		candidates := []event.CallCandidate{{
			Candidate:     jsonCandidate.Candidate,
			SDPMLineIndex: int(*jsonCandidate.SDPMLineIndex),
			SDPMID:        *jsonCandidate.SDPMid,
		}}
		c.signaling.SendICECandidates(participant.asMatrixRecipient(), candidates)

	case peer.ICEGatheringComplete:
		participant.logger.Info("Completed local ICE gathering")

		// Send an empty array of candidates to indicate that ICE gathering is complete.
		c.signaling.SendCandidatesGatheringFinished(participant.asMatrixRecipient())

	case peer.RenegotiationRequired:
		participant.logger.Info("Started renegotiation")
		participant.sendDataChannelMessage(event.SFUMessage{
			Op:       event.SFUOperationOffer,
			SDP:      msg.Offer.SDP,
			Metadata: c.getAvailableStreamsFor(participant.id),
		})

	case peer.DataChannelMessage:
		participant.logger.Debug("Received data channel message")
		var sfuMessage event.SFUMessage
		if err := json.Unmarshal([]byte(msg.Message), &sfuMessage); err != nil {
			c.logger.Errorf("Failed to unmarshal SFU message: %v", err)
			return
		}

		c.handleDataChannelMessage(participant, sfuMessage)

	case peer.DataChannelAvailable:
		participant.logger.Info("Connected data channel")
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
		participant.logger.Info("Received select request over DC")

		// Find tracks based on what we were asked for.
		tracks := c.getTracks(sfuMessage.Start)

		// Let's check if we have all the tracks that we were asked for are there.
		// If not, we will list which are not available (later on we must inform participant
		// about it unless the participant retries it).
		if len(tracks) != len(sfuMessage.Start) {
			for _, expected := range sfuMessage.Start {
				found := slices.IndexFunc(tracks, func(track *webrtc.TrackLocalStaticRTP) bool {
					return track.StreamID() == expected.StreamID && track.ID() == expected.TrackID
				})

				if found == -1 {
					c.logger.Warnf("Track not found: %s", expected.TrackID)
				}
			}
		}

		// Subscribe to the found tracks.
		for _, track := range tracks {
			if err := participant.peer.SubscribeTo(track); err != nil {
				participant.logger.Errorf("Failed to subscribe to track: %v", err)
				return
			}
		}

	case event.SFUOperationAnswer:
		participant.logger.Info("Received SDP answer over DC")

		if err := participant.peer.ProcessSDPAnswer(sfuMessage.SDP); err != nil {
			participant.logger.Errorf("Failed to set SDP answer: %v", err)
			return
		}

	case event.SFUOperationPublish:
		participant.logger.Info("Received SDP offer over DC")

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
		participant.logger.Info("Received unpublish over DC")

		// TODO: Clarify the semantics of unpublish.
	case event.SFUOperationAlive:
		// FIXME: Handle the heartbeat message here (updating the last timestamp etc).
	case event.SFUOperationMetadata:
		participant.logger.Info("Received metadata over DC")

		participant.streamMetadata = sfuMessage.Metadata
		c.resendMetadataToAllExcept(participant.id)
	}
}

func (c *Conference) processMatrixMessage(msg MatrixMessage) {
	switch ev := msg.Content.(type) {
	case *event.CallInviteEventContent:
		c.onNewParticipant(msg.Sender, ev)
	case *event.CallCandidatesEventContent:
		c.onCandidates(msg.Sender, ev)
	case *event.CallSelectAnswerEventContent:
		c.onSelectAnswer(msg.Sender, ev)
	case *event.CallHangupEventContent:
		c.onHangup(msg.Sender, ev)
	default:
		c.logger.Errorf("Unexpected event type: %T", ev)
	}
}
