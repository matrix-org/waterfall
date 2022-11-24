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
	"maunium.net/go/mautrix/event"
)

type Conference struct {
	id           string
	config       Config
	signaling    signaling.MatrixSignaling
	participants map[ParticipantID]*Participant
	peerEvents   chan common.Message[ParticipantID, peer.MessageContent]
	logger       *logrus.Entry
}

func NewConference(confID string, config Config, signaling signaling.MatrixSignaling) *Conference {
	conference := &Conference{
		id:           confID,
		config:       config,
		signaling:    signaling,
		participants: make(map[ParticipantID]*Participant),
		peerEvents:   make(chan common.Message[ParticipantID, peer.MessageContent]),
		logger:       logrus.WithFields(logrus.Fields{"conf_id": confID}),
	}

	// Start conference "main loop".
	go conference.processMessages()
	return conference
}

// New participant tries to join the conference.
func (c *Conference) OnNewParticipant(participantID ParticipantID, inviteEvent *event.CallInviteEventContent) {
	// As per MSC3401, when the `session_id` field changes from an incoming `m.call.member` event,
	// any existing calls from this device in this call should be terminated.
	// TODO: Implement this.
	for id, participant := range c.participants {
		if id.DeviceID == inviteEvent.DeviceID {
			if participant.remoteSessionID == inviteEvent.SenderSessionID {
				c.logger.WithFields(logrus.Fields{
					"device_id":  inviteEvent.DeviceID,
					"session_id": inviteEvent.SenderSessionID,
				}).Errorf("Found existing participant with equal DeviceID and SessionID")
				return
			} else {
				participant.peer.Terminate()
			}
		}
	}

	var (
		participantlogger = logrus.WithFields(logrus.Fields{
			"user_id":   participantID.UserID,
			"device_id": participantID.DeviceID,
			"conf_id":   c.id,
		})
		messageSink = common.NewMessageSink(participantID, c.peerEvents)
	)

	peer, sdpOffer, err := peer.NewPeer(inviteEvent.Offer.SDP, messageSink, participantlogger)
	if err != nil {
		c.logger.WithError(err).Errorf("Failed to create new peer")
		return
	}

	participant := &Participant{
		id:              participantID,
		peer:            peer,
		remoteSessionID: inviteEvent.SenderSessionID,
		streamMetadata:  inviteEvent.SDPStreamMetadata,
		publishedTracks: make(map[event.SFUTrackDescription]*webrtc.TrackLocalStaticRTP),
	}

	c.participants[participantID] = participant

	recipient := participant.asMatrixRecipient()
	streamMetadata := c.getStreamsMetadata(participantID)
	c.signaling.SendSDPAnswer(recipient, streamMetadata, sdpOffer.SDP)
}

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

		participant.peer.AddICECandidates(candidates)
	}
}

func (c *Conference) OnSelectAnswer(participantID ParticipantID, ev *event.CallSelectAnswerEventContent) {
	if participant := c.getParticipant(participantID, nil); participant != nil {
		if ev.SelectedPartyID != participantID.DeviceID.String() {
			c.logger.WithFields(logrus.Fields{
				"device_id": ev.SelectedPartyID,
			}).Errorf("Call was answered on a different device, kicking this peer")
			participant.peer.Terminate()
		}
	}
}

func (c *Conference) OnHangup(participantID ParticipantID, ev *event.CallHangupEventContent) {
	if participant := c.getParticipant(participantID, nil); participant != nil {
		participant.peer.Terminate()
	}
}
