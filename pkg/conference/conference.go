/*
Copyright 2022 The Matrix.org Foundation C.I.C.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package conference

import (
	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/matrix-org/waterfall/pkg/peer"
	"github.com/matrix-org/waterfall/pkg/signaling"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
	"golang.org/x/exp/maps"
	"maunium.net/go/mautrix/event"
)

// A single conference. Call and conference mean the same in context of Matrix.
type Conference struct {
	id           string
	config       Config
	signaling    signaling.MatrixSignaling
	participants map[ParticipantID]*Participant
	peerMessages chan common.Message[ParticipantID, peer.MessageContent]
	logger       *logrus.Entry
}

func NewConference(confID string, config Config, signaling signaling.MatrixSignaling) *Conference {
	conference := &Conference{
		id:           confID,
		config:       config,
		signaling:    signaling,
		participants: make(map[ParticipantID]*Participant),
		peerMessages: make(chan common.Message[ParticipantID, peer.MessageContent]),
		logger:       logrus.WithFields(logrus.Fields{"conf_id": confID}),
	}

	// Start conference "main loop".
	go conference.processMessages()
	return conference
}

// New participant tries to join the conference.
func (c *Conference) OnNewParticipant(participantID ParticipantID, inviteEvent *event.CallInviteEventContent) {
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
		return
	}

	// Send the answer back to the remote peer.
	recipient := participant.asMatrixRecipient()
	streamMetadata := c.getAvailableStreamsFor(participantID)
	c.signaling.SendSDPAnswer(recipient, streamMetadata, sdpAnswer.SDP)
}

// Process new ICE candidates received from Matrix signaling (from the remote peer) and forward them to
// our internal peer connection.
func (c *Conference) OnCandidates(participantID ParticipantID, ev *event.CallCandidatesEventContent) {
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
func (c *Conference) OnSelectAnswer(participantID ParticipantID, ev *event.CallSelectAnswerEventContent) {
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
func (c *Conference) OnHangup(participantID ParticipantID, ev *event.CallHangupEventContent) {
	c.removeParticipant(participantID)
}

func (c *Conference) getParticipant(participantID ParticipantID, optionalErrorMessage error) *Participant {
	participant, ok := c.participants[participantID]
	if !ok {
		logEntry := c.logger.WithFields(logrus.Fields{
			"user_id":   participantID.UserID,
			"device_id": participantID.DeviceID,
		})

		if optionalErrorMessage != nil {
			logEntry.WithError(optionalErrorMessage)
		} else {
			logEntry.Error("Participant not found")
		}

		return nil
	}

	return participant
}

// Helper to terminate and remove a participant from the conference.
func (c *Conference) removeParticipant(participantID ParticipantID) {
	participant := c.getParticipant(participantID, nil)
	if participant == nil {
		return
	}

	// Terminate the participant and remove it from the list.
	participant.peer.Terminate()
	delete(c.participants, participantID)

	// Inform the other participants about updated metadata (since the participant left
	// the corresponding streams of the participant are no longer available, so we're informing
	// others about it).
	c.resendMetadataToAllExcept(participantID)

	// Remove the participant's tracks from all participants who might have subscribed to them.
	obsoleteTracks := maps.Values(participant.publishedTracks)
	for _, otherParticipant := range c.participants {
		otherParticipant.peer.UnsubscribeFrom(obsoleteTracks)
	}
}

// Helper to get the list of available streams for a given participant, i.e. the list of streams
// that a given participant **can subscribe to**.
func (c *Conference) getAvailableStreamsFor(forParticipant ParticipantID) event.CallSDPStreamMetadata {
	streamsMetadata := make(event.CallSDPStreamMetadata)
	for id, participant := range c.participants {
		if forParticipant != id {
			for streamID, metadata := range participant.streamMetadata {
				streamsMetadata[streamID] = metadata
			}
		}
	}

	return streamsMetadata
}

// Helper that returns the list of streams inside this conference that match the given stream IDs.
func (c *Conference) getTracks(identifiers []event.SFUTrackDescription) []*webrtc.TrackLocalStaticRTP {
	tracks := make([]*webrtc.TrackLocalStaticRTP, len(identifiers))
	for _, participant := range c.participants {
		// Check if this participant has any of the tracks that we're looking for.
		for _, identifier := range identifiers {
			if track, ok := participant.publishedTracks[identifier]; ok {
				tracks = append(tracks, track)
			}
		}
	}
	return tracks
}

// Helper that sends current metadata about all available tracks to all participants except a given one.
func (c *Conference) resendMetadataToAllExcept(exceptMe ParticipantID) {
	for participantID, participant := range c.participants {
		if participantID != exceptMe {
			participant.sendDataChannelMessage(event.SFUMessage{
				Op:       event.SFUOperationMetadata,
				Metadata: c.getAvailableStreamsFor(participantID),
			})
		}
	}
}
