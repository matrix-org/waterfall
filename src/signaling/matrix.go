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
	"github.com/matrix-org/waterfall/src/peer"
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

const LocalSessionID = "sfu"

type MatrixClient struct {
	client *mautrix.Client
}

func NewMatrixClient(config Config) *MatrixClient {
	client, err := mautrix.NewClient(config.HomeserverURL, config.UserID, config.AccessToken)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create client")
	}

	whoami, err := client.Whoami()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to identify SFU user")
	}

	if config.UserID != whoami.UserID {
		logrus.WithField("user_id", config.UserID).Fatal("Access token is for the wrong user")
	}

	logrus.WithField("device_id", whoami.DeviceID).Info("Identified SFU as DeviceID")
	client.DeviceID = whoami.DeviceID

	return &MatrixClient{
		client: client,
	}
}

// Starts the Matrix client and connects to the homeserver,
// Returns only when the sync with Matrix fails.
func (m *MatrixClient) RunSync(callback func(*event.Event)) {
	syncer, ok := m.client.Syncer.(*mautrix.DefaultSyncer)
	if !ok {
		logrus.Panic("Syncer is not DefaultSyncer")
	}

	syncer.ParseEventContent = true
	syncer.OnEvent(func(_ mautrix.EventSource, evt *event.Event) {
		// We only care about to-device events.
		if evt.Type.Class != event.ToDeviceEventType {
			logrus.Warn("ignoring a not to-device event")
			return
		}

		// We drop the messages if they are not meant for us.
		if evt.Content.Raw["dest_session_id"] != LocalSessionID {
			logrus.Warn("SessionID does not match our SessionID - ignoring")
			return
		}

		callback(evt)
	})

	// TODO: We may want to reconnect if `Sync()` fails instead of ending the SFU
	//       as ending here will essentially drop all conferences which may not necessarily
	// 	     be what we want for the existing running conferences.
	if err := m.client.Sync(); err != nil {
		logrus.WithError(err).Panic("Sync failed")
	}
}

func (m *MatrixClient) CreateForConference(conferenceID string) *MatrixForConference {
	return &MatrixForConference{
		client:       m.client,
		conferenceID: conferenceID,
	}
}

type MatrixRecipient struct {
	ID              peer.ID
	RemoteSessionID id.SessionID
}

type MatrixSignaling interface {
	SendSDPAnswer(recipient MatrixRecipient, streamMetadata event.CallSDPStreamMetadata, sdp string)
	SendICECandidates(recipient MatrixRecipient, candidates []event.CallCandidate)
	SendCandidatesGatheringFinished(recipient MatrixRecipient)
	SendHangup(recipient MatrixRecipient, reason event.CallHangupReason)
}

type MatrixForConference struct {
	client       *mautrix.Client
	conferenceID string
}

func (m *MatrixForConference) SendSDPAnswer(
	recipient MatrixRecipient,
	streamMetadata event.CallSDPStreamMetadata,
	sdp string,
) {
	eventContent := &event.Content{
		Parsed: event.CallAnswerEventContent{
			BaseCallEventContent: m.createBaseEventContent(recipient.ID.DeviceID, recipient.RemoteSessionID),
			Answer: event.CallData{
				Type: "answer",
				SDP:  sdp,
			},
			SDPStreamMetadata: streamMetadata,
		},
	}

	m.sendToDevice(recipient.ID, event.CallAnswer, eventContent)
}

func (m *MatrixForConference) SendICECandidates(recipient MatrixRecipient, candidates []event.CallCandidate) {
	eventContent := &event.Content{
		Parsed: event.CallCandidatesEventContent{
			BaseCallEventContent: m.createBaseEventContent(recipient.ID.DeviceID, recipient.RemoteSessionID),
			Candidates:           candidates,
		},
	}

	m.sendToDevice(recipient.ID, event.CallCandidates, eventContent)
}

func (m *MatrixForConference) SendCandidatesGatheringFinished(recipient MatrixRecipient) {
	eventContent := &event.Content{
		Parsed: event.CallCandidatesEventContent{
			BaseCallEventContent: m.createBaseEventContent(recipient.ID.DeviceID, recipient.RemoteSessionID),
			Candidates:           []event.CallCandidate{{Candidate: ""}},
		},
	}

	m.sendToDevice(recipient.ID, event.CallCandidates, eventContent)
}

func (m *MatrixForConference) SendHangup(recipient MatrixRecipient, reason event.CallHangupReason) {
	eventContent := &event.Content{
		Parsed: event.CallHangupEventContent{
			BaseCallEventContent: m.createBaseEventContent(recipient.ID.DeviceID, recipient.RemoteSessionID),
			Reason:               reason,
		},
	}

	m.sendToDevice(recipient.ID, event.CallHangup, eventContent)
}

func (m *MatrixForConference) createBaseEventContent(
	destDeviceID id.DeviceID,
	destSessionID id.SessionID,
) event.BaseCallEventContent {
	return event.BaseCallEventContent{
		CallID:          m.conferenceID,
		ConfID:          m.conferenceID,
		DeviceID:        m.client.DeviceID,
		SenderSessionID: LocalSessionID,
		DestSessionID:   destSessionID,
		PartyID:         string(destDeviceID),
		Version:         event.CallVersion("1"),
	}
}

// Sends a to-device event to the given user.
func (m *MatrixForConference) sendToDevice(participantID peer.ID, eventType event.Type, eventContent *event.Content) {
	// TODO: Don't create logger again and again, it might be a bit expensive.
	logger := logrus.WithFields(logrus.Fields{
		"user_id":   participantID.UserID,
		"device_id": participantID.DeviceID,
	})

	sendRequest := &mautrix.ReqSendToDevice{
		Messages: map[id.UserID]map[id.DeviceID]*event.Content{
			participantID.UserID: {
				participantID.DeviceID: eventContent,
			},
		},
	}

	if _, err := m.client.SendToDevice(eventType, sendRequest); err != nil {
		logger.Errorf("failed to send to-device event: %w", err)
	}
}
