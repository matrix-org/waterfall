package conference

import (
	"time"

	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/matrix-org/waterfall/pkg/conference/participant"
	"github.com/matrix-org/waterfall/pkg/peer"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix/event"
)

type MessageContent interface{}

type MatrixMessage struct {
	Sender   participant.ID
	Content  MessageContent
	RawEvent *event.Event
}

// New participant tries to join the conference.
func (c *Conference) onNewParticipant(id participant.ID, inviteEvent *event.CallInviteEventContent) error {
	logger := c.logger.WithFields(logrus.Fields{
		"user_id":   id.UserID,
		"device_id": id.DeviceID,
	})

	logger.Info("Incoming participant")

	// As per MSC3401, when the `session_id` field changes from an incoming `m.call.member` event,
	// any existing calls from this device in this call should be terminated.
	if participant := c.tracker.GetParticipant(id); participant != nil {
		if participant.RemoteSessionID == inviteEvent.SenderSessionID {
			c.logger.Errorf("Found existing participant with equal DeviceID and SessionID")
		} else {
			c.removeParticipant(id)
		}
	}

	p := c.tracker.GetParticipant(id)
	var sdpAnswer *webrtc.SessionDescription

	// If participant exists still exists, then it means that the client does not behave properly.
	// In this case we treat this new invitation as a new SDP offer. Otherwise, we create a new one.
	if p != nil {
		answer, err := p.Peer.ProcessSDPOffer(inviteEvent.Offer.SDP)
		if err != nil {
			logger.WithError(err).Errorf("Failed to process SDP offer")
			return err
		}
		sdpAnswer = answer
	} else {
		messageSink := common.NewMessageSink(id, c.peerMessages)

		peerConnection, answer, err := peer.NewPeer(inviteEvent.Offer.SDP, messageSink, logger)
		if err != nil {
			logger.WithError(err).Errorf("Failed to process SDP offer")
			return err
		}

		heartbeat := common.Heartbeat{
			Interval: time.Duration(c.config.HeartbeatConfig.Interval) * time.Second,
			Timeout:  time.Duration(c.config.HeartbeatConfig.Timeout) * time.Second,
			SendPing: func() bool {
				return p.SendDataChannelMessage(event.Event{
					Type:    event.FocusCallPing,
					Content: event.Content{},
				}) == nil
			},
			OnTimeout: func() {
				messageSink.Send(peer.LeftTheCall{event.CallHangupKeepAliveTimeout})
			},
		}

		p = &participant.Participant{
			ID:              id,
			Peer:            peerConnection,
			Logger:          logger,
			RemoteSessionID: inviteEvent.SenderSessionID,
			HeartbeatPong:   heartbeat.Start(),
		}

		c.tracker.AddParticipant(p)
		sdpAnswer = answer
	}

	// Update streams metadata.
	c.updateMetadata(inviteEvent.SDPStreamMetadata)

	// Send the answer back to the remote peer.
	p.Logger.WithField("sdpAnswer", sdpAnswer.SDP).Debug("Sending SDP answer")
	recipient := p.AsMatrixRecipient()
	c.signaling.SendSDPAnswer(recipient, c.getAvailableStreamsFor(id), sdpAnswer.SDP)
	return nil
}

// Process new ICE candidates received from Matrix signaling (from the remote peer) and forward them to
// our internal peer connection.
func (c *Conference) onCandidates(id participant.ID, ev *event.CallCandidatesEventContent) {
	if participant := c.getParticipant(id, nil); participant != nil {
		participant.Logger.Debug("Received remote ICE candidates")

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

		participant.Peer.ProcessNewRemoteCandidates(candidates)
	}
}

// Process an acknowledgement from the remote peer that the SDP answer has been received
// and that the call can now proceed.
func (c *Conference) onSelectAnswer(id participant.ID, ev *event.CallSelectAnswerEventContent) {
	if participant := c.getParticipant(id, nil); participant != nil {
		participant.Logger.Info("Received remote answer selection")

		if ev.SelectedPartyID != string(c.signaling.DeviceID()) {
			c.logger.WithFields(logrus.Fields{
				"device_id": ev.SelectedPartyID,
				"user_id":   id,
			}).Errorf("Call was answered on a different device, kicking this peer")
			c.removeParticipant(id)
		}
	}
}

// Process a message from the remote peer telling that it wants to hang up the call.
func (c *Conference) onHangup(id participant.ID, ev *event.CallHangupEventContent) {
	if participant := c.getParticipant(id, nil); participant != nil {
		participant.Logger.Info("Received remote hangup")
		c.removeParticipant(id)
	}
}