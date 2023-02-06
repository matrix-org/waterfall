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
	"github.com/matrix-org/waterfall/pkg/channel"
	"github.com/matrix-org/waterfall/pkg/conference/participant"
	"github.com/matrix-org/waterfall/pkg/peer"
	"github.com/matrix-org/waterfall/pkg/signaling"
	"github.com/matrix-org/waterfall/pkg/webrtc_ext"
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// Starts a new conference or fails and returns an error.
// The conference ends when the last participant leaves.
func StartConference(
	confID string,
	config Config,
	peerConnectionFactory *webrtc_ext.PeerConnectionFactory,
	signaling signaling.MatrixSignaler,
	matrixEvents <-chan MatrixMessage,
	userID id.UserID,
	inviteEvent *event.CallInviteEventContent,
) (<-chan struct{}, error) {
	done := make(chan struct{})
	conference := &Conference{
		id:                confID,
		config:            config,
		connectionFactory: peerConnectionFactory,
		logger:            logrus.WithFields(logrus.Fields{"conf_id": confID}),
		matrixWorker:      newMatrixWorker(signaling),
		tracker:           *participant.NewParticipantTracker(),
		streamsMetadata:   make(event.CallSDPStreamMetadata),
		peerMessages:      make(chan channel.Message[participant.ID, peer.MessageContent], 100),
		matrixEvents:      matrixEvents,
		conferenceDone:    done,
	}

	participantID := participant.ID{UserID: userID, DeviceID: inviteEvent.DeviceID, CallID: inviteEvent.CallID}
	if err := conference.onNewParticipant(participantID, inviteEvent); err != nil {
		return nil, nil
	}

	// Start conference "main loop".
	go conference.processMessages()

	return done, nil
}
