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

package routing

import (
	conf "github.com/matrix-org/waterfall/pkg/conference"
	"github.com/matrix-org/waterfall/pkg/conference/participant"
	"github.com/matrix-org/waterfall/pkg/signaling"
	"github.com/matrix-org/waterfall/pkg/webrtc_ext"
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// The top-level state of the Router.
type Router struct {
	// Matrix matrix.
	matrix *signaling.MatrixClient
	// Sinks of all conferences (all calls that are currently forwarded by this SFU).
	conferenceSinks map[string]*conferenceStage
	// Configuration for the calls.
	config conf.Config
	// Channel for reading incoming Matrix SDK To-Device events and distributing them to the conferences.
	matrixEvents <-chan *event.Event
	// Channel for handling conference ended events.
	// Peer connection factory that can be used to create pre-configured peer connections.
	connectionFactory *webrtc_ext.PeerConnectionFactory
}

// Creates a new instance of the SFU with the given configuration.
func StartRouter(
	matrix *signaling.MatrixClient,
	connectionFactory *webrtc_ext.PeerConnectionFactory,
	matrixEvents <-chan *event.Event,
	config conf.Config,
) {
	router := &Router{
		matrix:            matrix,
		conferenceSinks:   make(map[string]*conferenceStage),
		config:            config,
		matrixEvents:      matrixEvents,
		connectionFactory: connectionFactory,
	}

	// Start the main loop of the Router.
	go func() {
		for msg := range router.matrixEvents {
			// To-Device message received from the remote peer.
			router.handleMatrixEvent(msg)
		}
	}()
}

// Handles incoming To-Device events that the SFU receives from clients.
func (r *Router) handleMatrixEvent(evt *event.Event) {
	var (
		conferenceID string
		callID       string
		deviceID     string
		userID       = evt.Sender
	)

	// Check if `conf_id` is present in the message (right messages do have it).
	rawConferenceID, okConferenceId := evt.Content.Raw["conf_id"]
	rawCallID, okCallId := evt.Content.Raw["call_id"]
	rawDeviceID, okDeviceID := evt.Content.Raw["device_id"]

	if okConferenceId && okCallId && okDeviceID {
		// Extract the conference ID from the message.
		conferenceID, okConferenceId = rawConferenceID.(string)
		callID, okCallId = rawCallID.(string)
		deviceID, okDeviceID = rawDeviceID.(string)

		if !okConferenceId || !okCallId || !okDeviceID {
			logrus.Warn("Ignoring invalid message without IDs")
			return
		}
	}

	logger := logrus.WithFields(logrus.Fields{
		"type":      evt.Type.Type,
		"user_id":   userID,
		"conf_id":   conferenceID,
		"device_id": deviceID,
	})

	conference := r.conferenceSinks[conferenceID]

	// Only ToDeviceCallInvite events are allowed to create a new conference, others
	// are expected to operate on an existing conference that is running on the SFU.
	if conference == nil && evt.Type.Type == event.ToDeviceCallInvite.Type {
		logger.Infof("creating new conference %s", conferenceID)

		matrixEvents := make(chan conf.MatrixMessage)

		conferenceDone, err := conf.StartConference(
			conferenceID,
			r.config,
			r.connectionFactory,
			r.matrix.CreateForConference(conferenceID),
			matrixEvents,
			userID,
			evt.Content.AsCallInvite(),
		)
		if err != nil {
			logger.WithError(err).Errorf("failed to start conference %s", conferenceID)
			return
		}

		r.conferenceSinks[conferenceID] = &conferenceStage{matrixEvents, conferenceDone}
		return
	}

	// All other events are expected to be handled by the existing conference.
	if conference == nil {
		logger.Warnf("ignoring %s since the conference is unknown", evt.Type.Type)
		return
	}

	// Sender of the To-Device message.
	sender := participant.ID{userID, id.DeviceID(deviceID), callID}

	var content conf.MessageContent
	switch evt.Type.Type {
	// Someone tries to participate in a call (join a call).
	case event.ToDeviceCallInvite.Type:
		// If there is an invitation sent and the conference does not exist, create one.
		content = evt.Content.AsCallInvite()
	case event.ToDeviceCallCandidates.Type:
		// Someone tries to send ICE candidates to the existing call.
		content = evt.Content.AsCallCandidates()
	case event.ToDeviceCallSelectAnswer.Type:
		// Someone informs us about them accepting our (SFU's) SDP answer for an existing call.
		content = evt.Content.AsCallSelectAnswer()
	case event.ToDeviceCallHangup.Type:
		// Someone tries to inform us about leaving an existing call.
		content = evt.Content.AsCallHangup()
	default:
		logger.Warnf("ignoring event that we must not receive: %s", evt.Type.Type)
		return
	}

	// Send the message to the conference.
	select {
	case <-conference.done:
		// Conference has just gotten closed, let's remove it from the list of conferences.
		delete(r.conferenceSinks, conferenceID)
		close(conference.sink)

		// Since we were not able to send the message, let's re-process it now.
		r.handleMatrixEvent(evt)
	case conference.sink <- conf.MatrixMessage{Content: content, Sender: sender}:
		// Ok,sent!
		return
	}
}

type conferenceStage struct {
	sink chan<- conf.MatrixMessage
	done <-chan struct{}
}
