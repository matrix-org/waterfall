package conference

import (
	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/matrix-org/waterfall/pkg/peer"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type MessageContent interface{}

type IncomingMatrixMessage struct {
	UserID  id.UserID
	Content MessageContent
}

// New participant tries to join the conference.
func (c *Conference) onNewParticipant(participantID ParticipantID, inviteEvent *event.CallInviteEventContent) error {
	logger := c.logger.WithFields(logrus.Fields{
		"user_id":   participantID.UserID,
		"device_id": participantID.DeviceID,
	})

	// As per MSC3401, when the `session_id` field changes from an incoming `m.call.member` event,
	// any existing calls from this device in this call should be terminated.
	participant := c.getParticipant(participantID, nil)
	if participant != nil {
		if participant.remoteSessionID == inviteEvent.SenderSessionID {
			c.logger.Errorf("Found existing participant with equal DeviceID and SessionID")
		} else {
			c.removeParticipant(participantID)
			participant = nil
		}
	}

	// If participant exists still exists, then it means that the client does not behave properly.
	// In this case we treat this new invitation as a new SDP offer. Otherwise, we create a new one.
	sdpAnswer, err := func() (*webrtc.SessionDescription, error) {
		if participant == nil {
			messageSink := common.NewMessageSink(participantID, c.peerMessages)

			peer, answer, err := peer.NewPeer(inviteEvent.Offer.SDP, messageSink, logger)
			if err != nil {
				return nil, err
			}

			participant = &Participant{
				id:              participantID,
				peer:            peer,
				logger:          logger,
				remoteSessionID: inviteEvent.SenderSessionID,
				streamMetadata:  inviteEvent.SDPStreamMetadata,
				publishedTracks: make(map[event.SFUTrackDescription]*webrtc.TrackLocalStaticRTP),
			}

			c.participants[participantID] = participant
			return answer, nil
		} else {
			answer, err := participant.peer.ProcessSDPOffer(inviteEvent.Offer.SDP)
			if err != nil {
				return nil, err
			}
			return answer, nil
		}
	}()
	if err != nil {
		logger.WithError(err).Errorf("Failed to process SDP offer")
		return err
	}

	// Send the answer back to the remote peer.
	recipient := participant.asMatrixRecipient()
	streamMetadata := c.getAvailableStreamsFor(participantID)
	c.signaling.SendSDPAnswer(recipient, streamMetadata, sdpAnswer.SDP)
	return nil
}

// Process new ICE candidates received from Matrix signaling (from the remote peer) and forward them to
// our internal peer connection.
func (c *Conference) onCandidates(participantID ParticipantID, ev *event.CallCandidatesEventContent) {
	if participant := c.getParticipant(participantID, nil); participant != nil {
		// Convert the candidates to the WebRTC format.
		candidates := make([]webrtc.ICECandidateInit, len(ev.Candidates))
		for i, candidate := range ev.Candidates {
			SDPMLineIndex := uint16(candidate.SDPMLineIndex)
			candidates[i] = webrtc.ICECandidateInit{
				Candidate:     candidate.Candidate,
				SDPMid:        &candidate.SDPMID,
				SDPMLineIndex: &SDPMLineIndex,
			}
		}

		participant.peer.ProcessNewRemoteCandidates(candidates)
	}
}

// Process an acknowledgement from the remote peer that the SDP answer has been received
// and that the call can now proceed.
func (c *Conference) onSelectAnswer(participantID ParticipantID, ev *event.CallSelectAnswerEventContent) {
	if participant := c.getParticipant(participantID, nil); participant != nil {
		if ev.SelectedPartyID != participantID.DeviceID.String() {
			c.logger.WithFields(logrus.Fields{
				"device_id": ev.SelectedPartyID,
				"user_id":   participantID,
			}).Errorf("Call was answered on a different device, kicking this peer")
			participant.peer.Terminate()
		}
	}
}

// Process a message from the remote peer telling that it wants to hang up the call.
func (c *Conference) onHangup(participantID ParticipantID, ev *event.CallHangupEventContent) {
	c.removeParticipant(participantID)
}
