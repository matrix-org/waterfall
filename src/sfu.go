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
	"errors"

	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
)

var ErrNoSuchConference = errors.New("no such conference")

// The top-level state of the SFU.
// Note that in Matrix MSCs, the term "focus" is used to refer to the SFU. But since "focus" is a very
// generic name and only makes sense in a certain context, we use the term "SFU" instead to avoid confusion
// given that this particular part is just the SFU logic (and not the "focus" selection algorithm etc).
type SFU struct {
	// Matrix client.
	client *mautrix.Client
	// All calls currently forwarded by this SFU.
	conferences map[string]*Conference
	// Structured logging for the SFU.
	logger *logrus.Entry
	// Configuration for the calls.
	config *CallConfig
}

// Creates a new instance of the SFU with the given configuration.
func NewSFU(client *mautrix.Client, config *CallConfig) *SFU {
	return &SFU{
		client:      client,
		conferences: make(map[string]*Conference),
		logger:      logrus.WithField("module", "sfu"),
		config:      config,
	}
}

// Returns a conference by its `id`, or creates a new one if it doesn't exist yet.
func (f *SFU) GetOrCreateConference(confID string, create bool) (*Conference, error) {
	if conference := f.conferences[confID]; conference != nil {
		return conference, nil
	}

	if create {
		f.logger.Printf("creating new conference %s", confID)
		conference := NewConference(confID, f.config)
		f.conferences[confID] = conference
		return conference, nil
	}

	return nil, ErrNoSuchConference
}

func (f *SFU) GetCall(confID string, callID string) (*Call, error) {
	var (
		conf *Conference
		call *Call
		err  error
	)

	if conf, err = f.GetOrCreateConference(confID, false); err != nil || conf == nil {
		f.logger.Printf("failed to get conf %s: %s", confID, err)
		return nil, err
	}

	if call, err = conf.GetCall(callID, false); err != nil || call == nil {
		f.logger.Printf("failed to get call %s: %s", callID, err)
		return nil, err
	}

	return call, nil
}

// Handles To-Device events that the SFU receives from clients.
func (f *SFU) onMatrixEvent(_ mautrix.EventSource, evt *event.Event) {
	// We only care about to-device events.
	if evt.Type.Class != event.ToDeviceEventType {
		f.logger.Warn("ignoring a not to-device event")
		return
	}

	evtLogger := f.logger.WithFields(logrus.Fields{
		"type":    evt.Type.Type,
		"user_id": evt.Sender.String(),
		"conf_id": evt.Content.Raw["conf_id"],
	})

	if evt.Content.Raw["dest_session_id"] != LocalSessionID {
		evtLogger.WithField("dest_session_id", LocalSessionID).Warn("SessionID does not match our SessionID - ignoring")
		return
	}

	var (
		conference *Conference
		call       *Call
		err        error
	)

	switch evt.Type.Type {
	case event.ToDeviceCallInvite.Type:
		invite := evt.Content.AsCallInvite()
		if invite == nil {
			evtLogger.Error("failed to parse invite")
			return
		}

		if conference, err = f.GetOrCreateConference(invite.ConfID, true); err != nil {
			evtLogger.WithError(err).WithFields(logrus.Fields{
				"conf_id": invite.ConfID,
			}).Error("failed to create conf")

			return
		}

		if err := conference.RemoveOldCallsByDeviceAndSessionIDs(invite.DeviceID, invite.SenderSessionID); err != nil {
			evtLogger.WithError(err).Error("error removing old calls - ignoring call")
			return
		}

		if call, err = conference.GetCall(invite.CallID, true); err != nil || call == nil {
			evtLogger.WithError(err).Error("failed to create call")
			return
		}

		call.InitWithInvite(evt, f.client)
		call.OnInvite(invite)
	case event.ToDeviceCallCandidates.Type:
		candidates := evt.Content.AsCallCandidates()
		if call, err = f.GetCall(candidates.ConfID, candidates.CallID); err != nil {
			return
		}

		call.OnCandidates(candidates)
	case event.ToDeviceCallSelectAnswer.Type:
		selectAnswer := evt.Content.AsCallSelectAnswer()
		if call, err = f.GetCall(selectAnswer.ConfID, selectAnswer.CallID); err != nil {
			return
		}

		call.OnSelectAnswer(selectAnswer)
	case event.ToDeviceCallHangup.Type:
		hangup := evt.Content.AsCallHangup()
		if call, err = f.GetCall(hangup.ConfID, hangup.CallID); err != nil {
			return
		}

		call.OnHangup()
	// Events that we **should not** receive!
	case event.ToDeviceCallNegotiate.Type:
		evtLogger.Warn("ignoring event as it should be handled over DC")
	case event.ToDeviceCallReject.Type:
	case event.ToDeviceCallAnswer.Type:
		evtLogger.Warn("ignoring event as we are always the ones answering")
	default:
		evtLogger.Warnf("ignoring unexpected event: %s", evt.Type.Type)
	}
}
