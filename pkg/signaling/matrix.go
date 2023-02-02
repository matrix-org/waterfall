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

package signaling

import (
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

const LocalSessionID = "sfu"

// Interface that abstracts sending Send-to-device messages for the conference.
type MatrixSignaler interface {
	SendMessage(MatrixMessage)
	DeviceID() id.DeviceID
}

// Defines the data that identifies a receiver of Matrix's to-device message.
type MatrixRecipient struct {
	UserID          id.UserID
	DeviceID        id.DeviceID
	RemoteSessionID id.SessionID
	CallID          string
}

type MatrixMessage struct {
	Recipient MatrixRecipient
	Message   interface{}
}

type SdpAnswer struct {
	StreamMetadata event.CallSDPStreamMetadata
	SDP            string
}

type IceCandidates struct {
	Candidates []event.CallCandidate
}

type CandidatesGatheringFinished struct{}

type Hangup struct {
	Reason event.CallHangupReason
}

// Matrix client scoped for a particular conference.
type MatrixForConference struct {
	client       *mautrix.Client
	conferenceID string
}

// Create a new Matrix client that abstracts outgoing Matrix messages from a given conference.
func (m *MatrixClient) CreateForConference(conferenceID string) *MatrixForConference {
	return &MatrixForConference{
		client:       m.client,
		conferenceID: conferenceID,
	}
}

func (m *MatrixForConference) SendMessage(message MatrixMessage) {
	switch msg := message.Message.(type) {
	case SdpAnswer:
		m.sendSdpAnswer(message.Recipient, msg.StreamMetadata, msg.SDP)
	case IceCandidates:
		m.sendICECandidates(message.Recipient, msg.Candidates)
	case CandidatesGatheringFinished:
		m.sendCandidatesGatheringFinished(message.Recipient)
	case Hangup:
		m.sendHangup(message.Recipient, msg.Reason)
	default:
		logrus.Error("Unknown signaling message type")
		return
	}
}

func (m *MatrixForConference) DeviceID() id.DeviceID {
	return m.client.DeviceID
}

func (m *MatrixForConference) sendSdpAnswer(
	recipient MatrixRecipient,
	streamMetadata event.CallSDPStreamMetadata,
	sdp string,
) {
	eventContent := &event.Content{
		Parsed: event.CallAnswerEventContent{
			BaseCallEventContent: m.createBaseEventContent(recipient.CallID, recipient.RemoteSessionID),
			Answer: event.CallData{
				Type: "answer",
				SDP:  sdp,
			},
			SDPStreamMetadata: streamMetadata,
		},
	}

	m.sendToDevice(recipient, event.CallAnswer, eventContent)
}

func (m *MatrixForConference) sendICECandidates(recipient MatrixRecipient, candidates []event.CallCandidate) {
	eventContent := &event.Content{
		Parsed: event.CallCandidatesEventContent{
			BaseCallEventContent: m.createBaseEventContent(recipient.CallID, recipient.RemoteSessionID),
			Candidates:           candidates,
		},
	}

	m.sendToDevice(recipient, event.CallCandidates, eventContent)
}

func (m *MatrixForConference) sendCandidatesGatheringFinished(recipient MatrixRecipient) {
	eventContent := &event.Content{
		Parsed: event.CallCandidatesEventContent{
			BaseCallEventContent: m.createBaseEventContent(recipient.CallID, recipient.RemoteSessionID),
			Candidates:           []event.CallCandidate{{Candidate: ""}},
		},
	}

	m.sendToDevice(recipient, event.CallCandidates, eventContent)
}

func (m *MatrixForConference) sendHangup(recipient MatrixRecipient, reason event.CallHangupReason) {
	eventContent := &event.Content{
		Parsed: event.CallHangupEventContent{
			BaseCallEventContent: m.createBaseEventContent(recipient.CallID, recipient.RemoteSessionID),
			Reason:               reason,
		},
	}

	m.sendToDevice(recipient, event.CallHangup, eventContent)
}

func (m *MatrixForConference) createBaseEventContent(
	callID string,
	destSessionID id.SessionID,
) event.BaseCallEventContent {
	return event.BaseCallEventContent{
		CallID:          callID,
		ConfID:          m.conferenceID,
		DeviceID:        m.client.DeviceID,
		SenderSessionID: LocalSessionID,
		DestSessionID:   destSessionID,
		PartyID:         string(m.client.DeviceID),
		Version:         event.CallVersion("1"),
	}
}

// Sends a to-device event to the given user.
func (m *MatrixForConference) sendToDevice(user MatrixRecipient, eventType event.Type, eventContent *event.Content) {
	logger := logrus.WithFields(logrus.Fields{
		"user_id":   user.UserID,
		"device_id": user.DeviceID,
	})

	sendRequest := &mautrix.ReqSendToDevice{
		Messages: map[id.UserID]map[id.DeviceID]*event.Content{
			user.UserID: {
				user.DeviceID: eventContent,
			},
		},
	}

	if _, err := m.client.SendToDevice(eventType, sendRequest); err != nil {
		logger.Errorf("failed to send to-device event: %v", err)
	}
}
