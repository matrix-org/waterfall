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
	"errors"

	"github.com/matrix-org/waterfall/src/conference"
	"github.com/matrix-org/waterfall/src/peer"
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

var ErrNoSuchConference = errors.New("no such conference")

// The top-level state of the SignalingServer.
// Note that in Matrix MSCs, the term "focus" is used to refer to the SignalingServer. But since "focus" is a very
// generic name and only makes sense in a certain context, we use the term "SignalingServer" instead to avoid confusion
// given that this particular part is just the SignalingServer logic (and not the "focus" selection algorithm etc).
type SignalingServer struct {
	// Matrix client.
	client *mautrix.Client
	// All calls currently forwarded by this SFU.
	conferences map[string]*conference.Conference
	// Configuration for the calls.
	config *conference.CallConfig
}

// Creates a new instance of the SFU with the given configuration.
func NewSignalingServer(client *mautrix.Client, config *conference.CallConfig) *SignalingServer {
	return &SignalingServer{
		client:      client,
		conferences: make(map[string]*conference.Conference),
		config:      config,
	}
}

// Handles To-Device events that the SFU receives from clients.
//
//nolint:funlen
func (f *SignalingServer) onMatrixEvent(_ mautrix.EventSource, evt *event.Event) {
	// We only care about to-device events.
	if evt.Type.Class != event.ToDeviceEventType {
		logrus.Warn("ignoring a not to-device event")
		return
	}

	// TODO: Don't create logger again and again, it might be a bit expensive.
	logger := logrus.WithFields(logrus.Fields{
		"type":    evt.Type.Type,
		"user_id": evt.Sender.String(),
		"conf_id": evt.Content.Raw["conf_id"],
	})

	if evt.Content.Raw["dest_session_id"] != LocalSessionID {
		logger.WithField("dest_session_id", LocalSessionID).Warn("SessionID does not match our SessionID - ignoring")
		return
	}

	switch evt.Type.Type {
	// Someone tries to participate in a call (join a call).
	case event.ToDeviceCallInvite.Type:
		invite := evt.Content.AsCallInvite()
		if invite == nil {
			logger.Error("failed to parse invite")
			return
		}

		// If there is an invitation sent and the conf does not exist, create one.
		if conf := f.conferences[invite.ConfID]; conf == nil {
			logger.Infof("creating new conference %s", invite.ConfID)
			f.conferences[invite.ConfID] = conference.NewConference(invite.ConfID, f.config)
		}

		peerID := peer.ID{
			UserID:   evt.Sender,
			DeviceID: invite.DeviceID,
		}

		// Inform conference about incoming participant.
		f.conferences[invite.ConfID].OnNewParticipant(peerID, invite)

	// Someone tries to send ICE candidates to the existing call.
	case event.ToDeviceCallCandidates.Type:
		candidates := evt.Content.AsCallCandidates()
		if candidates == nil {
			logger.Error("failed to parse candidates")
			return
		}

		conference := f.conferences[candidates.ConfID]
		if conference == nil {
			logger.Errorf("received candidates for unknown conference %s", candidates.ConfID)
			return
		}

		peerID := peer.ID{
			UserID:   evt.Sender,
			DeviceID: candidates.DeviceID,
		}

		conference.OnCandidates(peerID, candidates)

	// Someone informs us about them accepting our (SFU's) SDP answer for an existing call.
	case event.ToDeviceCallSelectAnswer.Type:
		selectAnswer := evt.Content.AsCallSelectAnswer()
		if selectAnswer == nil {
			logger.Error("failed to parse select_answer")
			return
		}

		conference := f.conferences[selectAnswer.ConfID]
		if conference == nil {
			logger.Errorf("received select_answer for unknown conference %s", selectAnswer.ConfID)
			return
		}

		peerID := peer.ID{
			UserID:   evt.Sender,
			DeviceID: selectAnswer.DeviceID,
		}

		conference.OnSelectAnswer(peerID, selectAnswer)

	// Someone tries to inform us about leaving an existing call.
	case event.ToDeviceCallHangup.Type:
		hangup := evt.Content.AsCallHangup()
		if hangup == nil {
			logger.Error("failed to parse hangup")
			return
		}

		conference := f.conferences[hangup.ConfID]
		if conference == nil {
			logger.Errorf("received hangup for unknown conference %s", hangup.ConfID)
			return
		}

		peerID := peer.ID{
			UserID:   evt.Sender,
			DeviceID: hangup.DeviceID,
		}

		conference.OnHangup(peerID, hangup)

	// Events that we **should not** receive!
	case event.ToDeviceCallNegotiate.Type:
		logger.Warn("ignoring negotiate event that must be handled over the data channel")
	case event.ToDeviceCallReject.Type:
		logger.Warn("ignoring reject event that must be handled over the data channel")
	case event.ToDeviceCallAnswer.Type:
		logger.Warn("ignoring event as we are always the ones sending the SDP answer at the moment")
	default:
		logger.Warnf("ignoring unexpected event: %s", evt.Type.Type)
	}
}

func (f *SignalingServer) createSDPAnswerEvent(
	conferenceID string,
	destSessionID id.SessionID,
	peerID peer.ID,
	sdp string,
	streamMetadata event.CallSDPStreamMetadata,
) *event.Content {
	return &event.Content{
		Parsed: event.CallAnswerEventContent{
			BaseCallEventContent: createBaseEventContent(conferenceID, f.client.DeviceID, peerID.DeviceID, destSessionID),
			Answer: event.CallData{
				Type: "answer",
				SDP:  sdp,
			},
			SDPStreamMetadata: streamMetadata,
		},
	}
}

func createBaseEventContent(
	conferenceID string,
	sfuDeviceID id.DeviceID,
	destDeviceID id.DeviceID,
	destSessionID id.SessionID,
) event.BaseCallEventContent {
	return event.BaseCallEventContent{
		CallID:          conferenceID,
		ConfID:          conferenceID,
		DeviceID:        sfuDeviceID,
		SenderSessionID: LocalSessionID,
		DestSessionID:   destSessionID,
		PartyID:         string(destDeviceID),
		Version:         event.CallVersion("1"),
	}
}

// Sends a to-device event to the given user.
func (f *SignalingServer) sendToDevice(participantID peer.ID, ev *event.Event) {
	// TODO: Don't create logger again and again, it might be a bit expensive.
	logger := logrus.WithFields(logrus.Fields{
		"user_id":   participantID.UserID,
		"device_id": participantID.DeviceID,
	})

	sendRequest := &mautrix.ReqSendToDevice{
		Messages: map[id.UserID]map[id.DeviceID]*event.Content{
			participantID.UserID: {
				participantID.DeviceID: &ev.Content,
			},
		},
	}

	if _, err := f.client.SendToDevice(ev.Type, sendRequest); err != nil {
		logger.Errorf("failed to send to-device event: %w", err)
	}
}
