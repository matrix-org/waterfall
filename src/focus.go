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
	"log"
	"strings"
	"sync"

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
}

func NewFocus(name string, client *mautrix.Client) *Focus {
	focus := new(Focus)

	focus.name = name
	focus.client = client
	focus.confs.confs = make(map[string]*Conference)

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
		log.Printf("failed to get conf %s: %s", confID, err)
		return nil, err
	}

	if call, err = conf.GetCall(callID, false); err != nil || call == nil {
		log.Printf("failed to get call %s: %s", callID, err)
		return nil, err
	}

	return call, nil
}

func (f *Focus) onEvent(_ mautrix.EventSource, evt *event.Event) {
	// We only care about to-device events
	if evt.Type.Class != event.ToDeviceEventType {
		return
	}

	if !strings.HasPrefix(evt.Type.Type, "m.call.") && !strings.HasPrefix(evt.Type.Type, "org.matrix.call.") {
		log.Printf("received non-call to-device event %s", evt.Type.Type)
		return
	} else if evt.Type.Type != event.ToDeviceCallCandidates.Type && evt.Type.Type != event.ToDeviceCallSelectAnswer.Type {
		log.Printf("%s | received to-device event %s", evt.Sender.String(), evt.Type.Type)
	}

	if evt.Content.Raw["dest_session_id"] != localSessionID {
		log.Printf(
			"%s | SessionID %s does not match our SessionID - ignoring",
			evt.Content.Raw["dest_session_id"],
			localSessionID,
		)

		return
	}

	var conf *Conference
	var call *Call
	var err error

	switch evt.Type.Type {
	case event.ToDeviceCallInvite.Type:
		invite := evt.Content.AsCallInvite()
		if conf, err = f.GetConf(invite.ConfID, true); err != nil {
			log.Printf("%s | failed to create conf %s: %+v", evt.Sender.String(), invite.ConfID, err)
			return
		}

		if err := conf.RemoveOldCallsByDeviceAndSessionIDs(invite.DeviceID, invite.SenderSessionID); err != nil {
			log.Printf("%s | error removing old calls - ignoring call: %+v", evt.Sender.String(), err)
			return
		}

		if call, err = conf.GetCall(invite.CallID, true); err != nil || call == nil {
			log.Printf("%s | failed to create call: %+v", evt.Sender.String(), err)
			return
		}

		call.UserID = evt.Sender
		call.DeviceID = invite.DeviceID
		// XXX: What if an SFU gets restarted?
		call.LocalSessionID = localSessionID
		call.RemoteSessionID = invite.SenderSessionID
		call.Client = f.client
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
		log.Printf("%s | ignoring event %s as should be handled over DC", evt.Sender.String(), evt.Type.Type)
	case event.ToDeviceCallReject.Type:
	case event.ToDeviceCallAnswer.Type:
		log.Printf("%s | ignoring event %s as we are always the ones answering", evt.Sender.String(), evt.Type.Type)
	default:
		log.Printf("%s | ignoring unrecognised to-device event of type %s", evt.Sender.String(), evt.Type.Type)
	}
}
