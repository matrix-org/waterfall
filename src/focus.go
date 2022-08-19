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
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
)

var ErrNoSuchConference = errors.New("no such conf")

type Confs struct {
	confsMu sync.RWMutex
	confs   map[string]*Conference
}

type Focus struct {
	name   string
	client *mautrix.Client
	confs  Confs
	logger *logrus.Entry
}

func NewFocus(name string, client *mautrix.Client) *Focus {
	focus := new(Focus)

	focus.name = name
	focus.client = client
	focus.confs.confs = make(map[string]*Conference)
	focus.logger = logrus.WithFields(logrus.Fields{
		"name": name,
	})

	return focus
}

func (f *Focus) GetConf(confID string, create bool) (*Conference, error) {
	f.confs.confsMu.Lock()
	defer f.confs.confsMu.Unlock()
	conference := f.confs.confs[confID]

	if conference == nil {
		if create {
			conference = &Conference{
				ConfID: confID,
			}
			f.confs.confs[confID] = conference
			conference.Calls.Calls = make(map[string]*Call)
			conference.Tracks.Tracks = []LocalTrackWithInfo{}
			conference.Metadata.Metadata = make(event.CallSDPStreamMetadata)
			conference.logger = logrus.WithFields(logrus.Fields{
				"conf_id": confID,
			})
		} else {
			return nil, ErrNoSuchConference
		}
	}

	return conference, nil
}

func (f *Focus) getExistingCall(confID string, callID string) (*Call, error) {
	var conf *Conference
	var call *Call
	var err error

	if conf, err = f.GetConf(confID, false); err != nil || conf == nil {
		f.logger.Printf("failed to get conf %s: %s", confID, err)
		return nil, err
	}

	if call, err = conf.GetCall(callID, false); err != nil || call == nil {
		f.logger.Printf("failed to get call %s: %s", callID, err)
		return nil, err
	}

	return call, nil
}

func (f *Focus) onEvent(_ mautrix.EventSource, evt *event.Event) {
	// We only care about to-device events
	if evt.Type.Class != event.ToDeviceEventType {
		return
	}

	evtLogger := f.logger.WithFields(logrus.Fields{
		"type":    evt.Type.Type,
		"user_id": evt.Sender.String(),
		"conf_id": evt.Content.Raw["conf_id"],
	})

	if !strings.HasPrefix(evt.Type.Type, "m.call.") && !strings.HasPrefix(evt.Type.Type, "org.matrix.call.") {
		evtLogger.Warn("received non-call to-device event")
		return
	} else if evt.Type.Type != event.ToDeviceCallCandidates.Type && evt.Type.Type != event.ToDeviceCallSelectAnswer.Type {
		evtLogger.Info("received to-device event")
	}

	if evt.Content.Raw["dest_session_id"] != localSessionID {
		evtLogger.WithField("dest_session_id", localSessionID).Warn("SessionID does not match our SessionID - ignoring")
		return
	}

	var conf *Conference
	var call *Call
	var err error

	switch evt.Type.Type {
	case event.ToDeviceCallInvite.Type:
		invite := evt.Content.AsCallInvite()
		if conf, err = f.GetConf(invite.ConfID, true); err != nil {
			evtLogger.WithError(err).WithFields(logrus.Fields{
				"conf_id": invite.ConfID,
			}).Error("failed to create conf")

			return
		}

		if err := conf.RemoveOldCallsByDeviceAndSessionIDs(invite.DeviceID, invite.SenderSessionID); err != nil {
			evtLogger.WithError(err).Error("error removing old calls - ignoring call")
			return
		}

		if call, err = conf.GetCall(invite.CallID, true); err != nil || call == nil {
			evtLogger.WithError(err).Error("failed to create call")
			return
		}

		call.UserID = evt.Sender
		call.DeviceID = invite.DeviceID
		// XXX: What if an SFU gets restarted?
		call.LocalSessionID = localSessionID
		call.RemoteSessionID = invite.SenderSessionID
		call.Client = f.client
		call.logger = logrus.WithFields(logrus.Fields{
			"user_id": evt.Sender,
			"conf_id": invite.ConfID,
		})
		call.OnInvite(invite)
	case event.ToDeviceCallCandidates.Type:
		candidates := evt.Content.AsCallCandidates()
		if call, err = f.getExistingCall(candidates.ConfID, candidates.CallID); err != nil {
			return
		}

		call.OnCandidates(candidates)
	case event.ToDeviceCallSelectAnswer.Type:
		selectAnswer := evt.Content.AsCallSelectAnswer()
		if call, err = f.getExistingCall(selectAnswer.ConfID, selectAnswer.CallID); err != nil {
			return
		}

		call.OnSelectAnswer(selectAnswer)
	case event.ToDeviceCallHangup.Type:
		hangup := evt.Content.AsCallHangup()
		if call, err = f.getExistingCall(hangup.ConfID, hangup.CallID); err != nil {
			return
		}

		call.OnHangup()
	// Events we don't care about
	case event.ToDeviceCallNegotiate.Type:
		evtLogger.Warn("ignoring event as it should be handled over DC")
	case event.ToDeviceCallReject.Type:
	case event.ToDeviceCallAnswer.Type:
		evtLogger.Warn("ignoring event as we are always the ones answering")
	default:
		evtLogger.Warn("ignoring unrecognised event")
	}
}
