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

package main

import (
	conf "github.com/matrix-org/waterfall/pkg/conference"
	"github.com/matrix-org/waterfall/pkg/signaling"
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix/event"
)

// The top-level state of the Router.
type Router struct {
	// Matrix matrix.
	matrix *signaling.MatrixClient
	// All calls currently forwarded by this SFU.
	conferences map[string]*conf.Conference
	// Configuration for the calls.
	config conf.Config
}

// Creates a new instance of the SFU with the given configuration.
func newRouter(matrix *signaling.MatrixClient, config conf.Config) *Router {
	return &Router{
		matrix:      matrix,
		conferences: make(map[string]*conf.Conference),
		config:      config,
	}
}

// Handles incoming To-Device events that the SFU receives from clients.
//
//nolint:funlen
func (r *Router) handleMatrixEvent(evt *event.Event) {
	// TODO: Don't create logger again and again, it might be a bit expensive.
	logger := logrus.WithFields(logrus.Fields{
		"type":    evt.Type.Type,
		"user_id": evt.Sender.String(),
		"conf_id": evt.Content.Raw["conf_id"],
	})

	switch evt.Type.Type {
	// Someone tries to participate in a call (join a call).
	case event.ToDeviceCallInvite.Type:
		invite := evt.Content.AsCallInvite()
		if invite == nil {
			logger.Error("failed to parse invite")
			return
		}

		// If there is an invitation sent and the conference does not exist, create one.
		if conference := r.conferences[invite.ConfID]; conference == nil {
			logger.Infof("creating new conference %s", invite.ConfID)
			r.conferences[invite.ConfID] = conf.NewConference(
				invite.ConfID,
				r.config,
				r.matrix.CreateForConference(invite.ConfID),
			)
		}

		peerID := conf.ParticipantID{
			UserID:   evt.Sender,
			DeviceID: invite.DeviceID,
		}

		// Inform conference about incoming participant.
		r.conferences[invite.ConfID].OnNewParticipant(peerID, invite)

	// Someone tries to send ICE candidates to the existing call.
	case event.ToDeviceCallCandidates.Type:
		candidates := evt.Content.AsCallCandidates()
		if candidates == nil {
			logger.Error("failed to parse candidates")
			return
		}

		conference := r.conferences[candidates.ConfID]
		if conference == nil {
			logger.Errorf("received candidates for unknown conference %s", candidates.ConfID)
			return
		}

		peerID := conf.ParticipantID{
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

		conference := r.conferences[selectAnswer.ConfID]
		if conference == nil {
			logger.Errorf("received select_answer for unknown conference %s", selectAnswer.ConfID)
			return
		}

		peerID := conf.ParticipantID{
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

		conference := r.conferences[hangup.ConfID]
		if conference == nil {
			logger.Errorf("received hangup for unknown conference %s", hangup.ConfID)
			return
		}

		peerID := conf.ParticipantID{
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
