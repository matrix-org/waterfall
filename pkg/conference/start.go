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
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// Starts a new conference or fails and returns an error.
func StartConference(
	confID string,
	config Config,
	signaling signaling.MatrixSignaling,
	conferenceEndNotifier ConferenceEndNotifier,
	UserID id.UserID,
	inviteEvent *event.CallInviteEventContent,
) (*common.Sender[MatrixMessage], error) {
	sender, receiver := common.NewChannel[MatrixMessage]()

	conference := &Conference{
		id:             confID,
		config:         config,
		signaling:      signaling,
		matrixMessages: receiver,
		endNotifier:    conferenceEndNotifier,
		participants:   make(map[ParticipantID]*Participant),
		peerMessages:   make(chan common.Message[ParticipantID, peer.MessageContent], 128),
		logger:         logrus.WithFields(logrus.Fields{"conf_id": confID}),
	}

	participantID := ParticipantID{UserID: UserID, DeviceID: inviteEvent.DeviceID, CallID: inviteEvent.CallID}
	if err := conference.onNewParticipant(participantID, inviteEvent); err != nil {
		return nil, err
	}

	// Start conference "main loop".
	go conference.processMessages()

	return &sender, nil
}

type ConferenceEndNotifier interface {
	// Called when the conference ends.
	Notify(unread []MatrixMessage)
}
