package conference

import (
	"github.com/matrix-org/waterfall/pkg/channel"
	"github.com/matrix-org/waterfall/pkg/conference/participant"
	"github.com/matrix-org/waterfall/pkg/peer"
	"maunium.net/go/mautrix/event"
)

// Listen on messages from incoming channels and process them.
// This is essentially the main loop of the conference.
// If this function returns, the conference is over.
func (c *Conference) processMessages(signalDone chan struct{}) {
	// When the main loop of the conference ends, clean up the resources.
	defer close(signalDone)
	defer c.matrixWorker.stop()
	defer c.telemetry.End()

	for {
		select {
		case msg := <-c.peerMessages:
			c.processPeerMessage(msg)
		case msg := <-c.matrixEvents:
			c.processMatrixMessage(msg)
		case msg := <-c.publishedTrackStopped:
			c.processPublishedTrackFailedMessage(msg.OwnerID, msg.TrackID)
		}

		// If there are no more participants, stop the conference.
		if !c.tracker.HasParticipants() {
			c.logger.Info("No more participants, stopping the conference")
			return
		}
	}
}

// Process a message from a local peer.
func (c *Conference) processPeerMessage(message channel.Message[participant.ID, peer.MessageContent]) {
	// Since Go does not support ADTs, we have to use a switch statement to
	// determine the actual type of the message.
	switch msg := message.Content.(type) {
	case peer.JoinedTheCall:
		c.processJoinedTheCallMessage(message.Sender, msg)
	case peer.LeftTheCall:
		c.processLeftTheCallMessage(message.Sender, msg)
	case peer.NewTrackPublished:
		c.processNewTrackPublishedMessage(message.Sender, msg)
	case peer.NewICECandidate:
		c.processNewICECandidateMessage(message.Sender, msg)
	case peer.ICEGatheringComplete:
		c.processICEGatheringCompleteMessage(message.Sender, msg)
	case peer.RenegotiationRequired:
		c.processRenegotiationRequiredMessage(message.Sender, msg)
	case peer.DataChannelMessage:
		c.processDataChannelMessage(message.Sender, msg)
	case peer.DataChannelAvailable:
		c.processDataChannelAvailableMessage(message.Sender, msg)
	default:
		c.logger.Errorf("Unknown message type: %T", msg)
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
