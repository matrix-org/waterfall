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
	"fmt"
	"log"
	"reflect"
	"strings"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
)

const localSessionID = "sfu"

func initMatrix(config *config) error {
	client, err := mautrix.NewClient(config.HomeserverURL, config.UserID, config.AccessToken)
	if err != nil {
		log.Fatal("Failed to create client", err)
	}

	whoami, err := client.Whoami()
	if err != nil {
		log.Fatal("Failed to identify SFU user", err)
	}
	if config.UserID != whoami.UserID {
		log.Fatalf("Access token is for the wrong user: %s", config.UserID)
	}
	log.Printf("Identified SFU as device %s", whoami.DeviceID)
	client.DeviceID = whoami.DeviceID

	focus := new(focus)
	focus.Init(fmt.Sprintf("%s (%s)", config.UserID, client.DeviceID))

	syncer := client.Syncer.(*mautrix.DefaultSyncer)
	syncer.ParseEventContent = true

	// add to-device flavours of the call events to mautrix for MSC3401
	var (
		CallInvite       = event.Type{"m.call.invite", event.ToDeviceEventType}
		CallCandidates   = event.Type{"m.call.candidates", event.ToDeviceEventType}
		CallAnswer       = event.Type{"m.call.answer", event.ToDeviceEventType}
		CallReject       = event.Type{"m.call.reject", event.ToDeviceEventType}
		CallSelectAnswer = event.Type{"m.call.select_answer", event.ToDeviceEventType}
		CallNegotiate    = event.Type{"m.call.negotiate", event.ToDeviceEventType}
		CallHangup       = event.Type{"m.call.hangup", event.ToDeviceEventType}
	)
	event.TypeMap[CallInvite] = reflect.TypeOf(event.CallInviteEventContent{})
	event.TypeMap[CallCandidates] = reflect.TypeOf(event.CallCandidatesEventContent{})
	event.TypeMap[CallAnswer] = reflect.TypeOf(event.CallAnswerEventContent{})
	event.TypeMap[CallReject] = reflect.TypeOf(event.CallRejectEventContent{})
	event.TypeMap[CallSelectAnswer] = reflect.TypeOf(event.CallSelectAnswerEventContent{})
	event.TypeMap[CallNegotiate] = reflect.TypeOf(event.CallNegotiateEventContent{})
	event.TypeMap[CallHangup] = reflect.TypeOf(event.CallHangupEventContent{})

	// TODO: E2EE

	getExistingCall := func(confID string, callID string) (*call, error) {
		var conf *conf
		var call *call

		if conf, err = focus.getConf(confID, false); err != nil || conf == nil {
			log.Printf("failed to get conf %s: %s", confID, err)
			return nil, err
		}
		if call, err = conf.getCall(callID, false); err != nil || call == nil {
			log.Printf("failed to get call %s: %s", callID, err)
			return nil, err
		}
		return call, nil
	}

	syncer.OnSync(func(resp *mautrix.RespSync, since string) bool {
		for _, evt := range resp.ToDevice.Events {
			evt.Type.Class = event.ToDeviceEventType
			err := evt.Content.ParseRaw(evt.Type)
			if err != nil {
				log.Printf("failed to parse to-device event of type %s: %v", evt.Type.Type, err)
				continue
			}

			var conf *conf
			var call *call

			if strings.HasPrefix(evt.Type.Type, "m.call.") || strings.HasPrefix(evt.Type.Type, "org.matrix.call.") {
				log.Printf("%s | received to-device event %s", evt.Sender.String(), evt.Type.Type)
			} else {
				log.Printf("received non-call to-device event %s", evt.Type.Type)
				continue
			}

			if evt.Content.Raw["dest_session_id"] != localSessionID {
				log.Printf("%s | SessionID %s does not match our SessionID %s - ignoring", evt.Content.Raw["dest_session_id"], localSessionID, err)
				continue
			}

			switch evt.Type.Type {
			case CallInvite.Type:
				invite := evt.Content.AsCallInvite()
				if conf, err = focus.getConf(invite.ConfID, true); err != nil || conf == nil {
					log.Printf("%s | failed to create conf %s: %+v", evt.Sender.String(), invite.ConfID, err)
					return true
				}
				if err := conf.removeOldCallsByDeviceAndSessionIds(invite.DeviceID, invite.SenderSessionID); err != nil {
					log.Printf("%s | error removing old calls - ignoring call: %+v", evt.Sender.String(), err)
					return true
				}
				if call, err = conf.getCall(invite.CallID, true); err != nil || call == nil {
					log.Printf("%s | failed to create call: %+v", evt.Sender.String(), err)
					return true
				}
				call.userID = evt.Sender
				call.deviceID = invite.DeviceID
				// XXX: What if an SFU gets restarted?
				call.localSessionID = localSessionID
				call.remoteSessionID = invite.SenderSessionID
				call.client = client
				call.onInvite(invite)
			case CallCandidates.Type:
				candidates := evt.Content.AsCallCandidates()
				if call, err = getExistingCall((*candidates).ConfID, (*candidates).CallID); err != nil || call == nil {
					return true
				}
				call.onCandidates(candidates)
			case CallSelectAnswer.Type:
				selectAnswer := evt.Content.AsCallSelectAnswer()
				if call, err = getExistingCall(selectAnswer.ConfID, selectAnswer.CallID); err != nil || call == nil {
					return true
				}
				call.onSelectAnswer(selectAnswer)
			case CallHangup.Type:
				hangup := evt.Content.AsCallHangup()
				if call, err = getExistingCall(hangup.ConfID, hangup.CallID); err != nil || call == nil {
					return true
				}
				call.onHangup(hangup)

			// Events we don't care about
			case CallNegotiate.Type:
				log.Printf("%s | ignoring event %s as should be handled over DC", evt.Sender.String(), evt.Type.Type)
			case CallReject.Type:
			case CallAnswer.Type:
				log.Printf("%s | ignoring event %s as we are always the ones answering", evt.Sender.String(), evt.Type.Type)
			default:
				log.Printf("%s | ignoring unrecognised to-device event of type %s", evt.Sender.String(), evt.Type.Type)
			}
		}

		return true
	})

	if err = client.Sync(); err != nil {
		log.Panic("Sync failed", err)
	}

	return nil
}
