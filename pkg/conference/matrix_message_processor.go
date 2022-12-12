package conference

import (
	"time"

	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/matrix-org/waterfall/pkg/peer"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix/event"
)

type MessageContent interface{}

type MatrixMessage struct {
	Sender   ParticipantID
	Content  MessageContent
	RawEvent *event.Event
}

// New participant tries to join the conference.
func (c *Conference) onNewParticipant(participantID ParticipantID, inviteEvent *event.CallInviteEventContent) error {
	logger := c.logger.WithFields(logrus.Fields{
		"user_id":   participantID.UserID,
		"device_id": participantID.DeviceID,
	})

	logger.Info("Incoming call invite")

	// As per MSC3401, when the `session_id` field changes from an incoming `m.call.member` event,
	// any existing calls from this device in this call should be terminated.
	if participant := c.participants[participantID]; participant != nil {
		if participant.remoteSessionID == inviteEvent.SenderSessionID {
			c.logger.Errorf("Found existing participant with equal DeviceID and SessionID")
		} else {
			c.removeParticipant(participantID)
		}
	}

	participant := c.participants[participantID]
	var sdpAnswer *webrtc.SessionDescription

	// If participant exists still exists, then it means that the client does not behave properly.
	// In this case we treat this new invitation as a new SDP offer. Otherwise, we create a new one.
	if participant != nil {
		answer, err := participant.peer.ProcessSDPOffer(inviteEvent.Offer.SDP)
		if err != nil {
			logger.WithError(err).Errorf("Failed to process SDP offer")
			return err
		}
		sdpAnswer = answer
	} else {
		messageSink := common.NewMessageSink(participantID, c.peerMessages)

		peer, answer, err := peer.NewPeer(
			inviteEvent.Offer.SDP,
			messageSink,
			logger,
			peer.PingPongConfig{
				Interval:    time.Duration(c.config.PingPongConfig.Interval) * time.Second,
				Timeout:     time.Duration(c.config.PingPongConfig.Timeout) * time.Second,
				PongChannel: make(chan peer.Pong, common.UnboundedChannelSize),
				SendPing: func() {
					participant.sendDataChannelMessage(event.Event{
						Type:    event.FocusCallPing,
						Content: event.Content{},
					})
				},
				OnDeadLine: func() {
					messageSink.Send(peer.LeftTheCall{Reason: event.CallHangupKeepAliveTimeout})
				},
			},
		)
		if err != nil {
			logger.WithError(err).Errorf("Failed to process SDP offer")
			return err
		}

		participant = &Participant{
			id:              participantID,
			peer:            peer,
			logger:          logger,
			remoteSessionID: inviteEvent.SenderSessionID,
			streamMetadata:  inviteEvent.SDPStreamMetadata,
			publishedTracks: make(map[string]PublishedTrack),
		}

		c.participants[participantID] = participant
		sdpAnswer = answer
	}

	// Send the answer back to the remote peer.
	recipient := participant.asMatrixRecipient()
	streamMetadata := c.getAvailableStreamsFor(participantID)
	participant.logger.WithField("sdpAnswer", sdpAnswer.SDP).Debug("Sending SDP answer")
	c.signaling.SendSDPAnswer(recipient, streamMetadata, sdpAnswer.SDP)
	return nil
}

// Process new ICE candidates received from Matrix signaling (from the remote peer) and forward them to
// our internal peer connection.
func (c *Conference) onCandidates(participantID ParticipantID, ev *event.CallCandidatesEventContent) {
	if participant := c.getParticipant(participantID, nil); participant != nil {
		participant.logger.Info("Received remote ICE candidates")

		// Convert the candidates to the WebRTC format.
		candidates := make([]webrtc.ICECandidateInit, len(ev.Candidates))
		for i, candidate := range ev.Candidates {
			SDPMLineIndex := uint16(candidate.SDPMLineIndex)
			candidates[i] = webrtc.ICECandidateInit{
				Candidate:        candidate.Candidate,
				SDPMid:           &candidate.SDPMID,
				SDPMLineIndex:    &SDPMLineIndex,
				UsernameFragment: new(string),
			}
		}

		participant.peer.ProcessNewRemoteCandidates(candidates)
	}
}

// Process an acknowledgement from the remote peer that the SDP answer has been received
// and that the call can now proceed.
func (c *Conference) onSelectAnswer(participantID ParticipantID, ev *event.CallSelectAnswerEventContent) {
	if participant := c.getParticipant(participantID, nil); participant != nil {
		participant.logger.Info("Received remote answer selection")

		if ev.SelectedPartyID != string(c.signaling.DeviceID()) {
			c.logger.WithFields(logrus.Fields{
				"device_id": ev.SelectedPartyID,
				"user_id":   participantID,
			}).Errorf("Call was answered on a different device, kicking this peer")
			c.removeParticipant(participantID)
		}
	}
}

// Process a message from the remote peer telling that it wants to hang up the call.
func (c *Conference) onHangup(participantID ParticipantID, ev *event.CallHangupEventContent) {
	if participant := c.participants[participantID]; participant != nil {
		participant.logger.Info("Received remote hangup")
		c.removeParticipant(participantID)
	}
}
