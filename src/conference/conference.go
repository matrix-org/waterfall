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
	"github.com/matrix-org/waterfall/src/peer"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// Configuration for the group conferences (calls).
type CallConfig struct {
	// Keep-alive timeout for WebRTC connections. If no keep-alive has been received
	// from the client for this duration, the connection is considered dead.
	KeepAliveTimeout int
}

type Participant struct {
	Peer *peer.Peer
	Data *ParticipantData
}

type ParticipantData struct {
	RemoteSessionID id.SessionID
	StreamMetadata  event.CallSDPStreamMetadata
}

type Conference struct {
	conferenceID        string
	config              *CallConfig
	participants        map[peer.ID]*Participant
	participantsChannel peer.MessageChannel
	logger              *logrus.Entry
}

func NewConference(confID string, config *CallConfig) *Conference {
	conference := new(Conference)
	conference.config = config
	conference.conferenceID = confID
	conference.participants = make(map[peer.ID]*Participant)
	conference.participantsChannel = make(peer.MessageChannel)
	conference.logger = logrus.WithFields(logrus.Fields{
		"conf_id": confID,
	})
	return conference
}

// New participant tries to join the conference.
func (c *Conference) OnNewParticipant(participantID peer.ID, inviteEvent *event.CallInviteEventContent) {
	// As per MSC3401, when the `session_id` field changes from an incoming `m.call.member` event,
	// any existing calls from this device in this call should be terminated.
	// TODO: Implement this.
	/*
		for _, participant := range c.participants {
			if participant.data.DeviceID == inviteEvent.DeviceID {
				if participant.data.RemoteSessionID == inviteEvent.SenderSessionID {
					c.logger.WithFields(logrus.Fields{
						"device_id":  inviteEvent.DeviceID,
						"session_id": inviteEvent.SenderSessionID,
					}).Errorf("Found existing participant with equal DeviceID and SessionID")
					return
				} else {
					participant.Terminate()
					delete(c.participants, participant.data.UserID)
				}
			}
		}
	*/

	peer, _, err := peer.NewPeer(participantID, c.conferenceID, inviteEvent.Offer.SDP, c.participantsChannel)
	if err != nil {
		c.logger.WithError(err).Errorf("Failed to create new peer")
		return
	}

	participantData := &ParticipantData{
		RemoteSessionID: inviteEvent.SenderSessionID,
		StreamMetadata:  inviteEvent.SDPStreamMetadata,
	}

	c.participants[participantID] = &Participant{Peer: peer, Data: participantData}

	// TODO: Send the SDP answer back to the participant's device.
}

func (c *Conference) OnCandidates(peerID peer.ID, candidatesEvent *event.CallCandidatesEventContent) {
	if participant := c.getParticipant(peerID); participant != nil {
		// Convert the candidates to the WebRTC format.
		candidates := make([]webrtc.ICECandidateInit, len(candidatesEvent.Candidates))
		for i, candidate := range candidatesEvent.Candidates {
			SDPMLineIndex := uint16(candidate.SDPMLineIndex)
			candidates[i] = webrtc.ICECandidateInit{
				Candidate:     candidate.Candidate,
				SDPMid:        &candidate.SDPMID,
				SDPMLineIndex: &SDPMLineIndex,
			}
		}

		participant.Peer.AddICECandidates(candidates)
	}
}

func (c *Conference) OnSelectAnswer(peerID peer.ID, selectAnswerEvent *event.CallSelectAnswerEventContent) {
	if participant := c.getParticipant(peerID); participant != nil {
		if selectAnswerEvent.SelectedPartyID != peerID.DeviceID.String() {
			c.logger.WithFields(logrus.Fields{
				"device_id": selectAnswerEvent.SelectedPartyID,
			}).Errorf("Call was answered on a different device, kicking this peer")
			participant.Peer.Terminate()
		}
	}
}

func (c *Conference) OnHangup(peerID peer.ID, hangupEvent *event.CallHangupEventContent) {
	if participant := c.getParticipant(peerID); participant != nil {
		participant.Peer.Terminate()
	}
}

func (c *Conference) getParticipant(peerID peer.ID) *Participant {
	participant, ok := c.participants[peerID]
	if !ok {
		c.logger.WithFields(logrus.Fields{
			"user_id":   peerID.UserID,
			"device_id": peerID.DeviceID,
		}).Errorf("Failed to find participant")
		return nil
	}

	return participant
}
