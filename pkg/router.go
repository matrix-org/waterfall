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
	"github.com/matrix-org/waterfall/pkg/conference"
	conf "github.com/matrix-org/waterfall/pkg/conference"
	"github.com/matrix-org/waterfall/pkg/signaling"
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix/event"
)

// The top-level state of the Router.
type Router struct {
	// Matrix matrix.
	matrix *signaling.MatrixClient
	// Sinks of all conferences (all calls that are currently forwarded by this SFU).
	conferenceSinks map[string]chan<- conf.MatrixMessage
	// Configuration for the calls.
	config conf.Config
}

// Creates a new instance of the SFU with the given configuration.
func newRouter(matrix *signaling.MatrixClient, config conf.Config) *Router {
	return &Router{
		matrix:          matrix,
		conferenceSinks: make(map[string]chan<- conference.MatrixMessage),
		config:          config,
	}
}

// Handles incoming To-Device events that the SFU receives from clients.
func (r *Router) handleMatrixEvent(evt *event.Event) {
	// Check if `conf_id` is present in the message (right messages do have it).
	rawConferenceID, ok := evt.Content.Raw["conf_id"]
	if !ok {
		return
	}

	// Try to parse the conference ID without parsing the whole event.
	conferenceID, ok := rawConferenceID.(string)
	if !ok {
		return
	}

	logger := logrus.WithFields(logrus.Fields{
		"type":    evt.Type.Type,
		"user_id": evt.Sender.String(),
		"conf_id": conferenceID,
	})

	conference := r.conferenceSinks[conferenceID]

	// Only ToDeviceCallInvite events are allowed to create a new conference, others
	// are expected to operate on an existing conference that is running on the SFU.
	if conference == nil && evt.Type.Type != event.ToDeviceCallInvite.Type {
		logger.Warnf("ignoring %s for an unknown conference %s, ignoring", &event.ToDeviceCallInvite.Type)
		return
	}

	switch evt.Type.Type {
	// Someone tries to participate in a call (join a call).
	case event.ToDeviceCallInvite.Type:
		// If there is an invitation sent and the conference does not exist, create one.
		if conference == nil {
			logger.Infof("creating new conference %s", conferenceID)
			conferenceSink, err := conf.StartConference(
				conferenceID,
				r.config,
				r.matrix.CreateForConference(conferenceID),
				evt.Sender, evt.Content.AsCallInvite(),
			)
			if err != nil {
				logger.WithError(err).Errorf("failed to start conference %s", conferenceID)
				return
			}

			r.conferenceSinks[conferenceID] = conferenceSink
			return
		}

		conference <- conf.MatrixMessage{UserID: evt.Sender, Content: evt.Content.AsCallInvite()}
	case event.ToDeviceCallCandidates.Type:
		// Someone tries to send ICE candidates to the existing call.
		conference <- conf.MatrixMessage{UserID: evt.Sender, Content: evt.Content.AsCallCandidates()}
	case event.ToDeviceCallSelectAnswer.Type:
		// Someone informs us about them accepting our (SFU's) SDP answer for an existing call.
		conference <- conf.MatrixMessage{UserID: evt.Sender, Content: evt.Content.AsCallSelectAnswer()}
	case event.ToDeviceCallHangup.Type:
		// Someone tries to inform us about leaving an existing call.
		conference <- conf.MatrixMessage{UserID: evt.Sender, Content: evt.Content.AsCallHangup()}
	default:
		logger.Warnf("ignoring event that we must not receive: %s", evt.Type.Type)
	}
}
