package main

import (
	"fmt"
	"log"
	"reflect"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
)

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

	focus := &focus{
		name: fmt.Sprintf("%s (%s)", config.UserID, client.DeviceID),
	}

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

	syncer.OnSync(func(resp *mautrix.RespSync, since string) bool {
		log.Printf("synced %+v %+v", resp, since)

		for _, evt := range resp.ToDevice.Events {
			evt.Type.Class = event.ToDeviceEventType
			err := evt.Content.ParseRaw(evt.Type)
			if err != nil {
				log.Printf("Failed to parse to-device event of type %s: %v", evt.Type.Type, err)
				continue
			}
			log.Printf("event %+v", evt)
			switch evt.Type.Type {
			case CallInvite.Type:
				invite := evt.Content.AsCallInvite()
				conf, _ := focus.getConf(invite.ConfID, true)
				call, _ := conf.getCall(invite.CallID, true)
				call.userID = evt.Sender
				call.deviceID = invite.DeviceID
				// TODO: check session IDs
				call.onInvite(invite)
			case CallCandidates.Type:
				candidates := evt.Content.AsCallCandidates()
				var conf *conf
				var call *call
				var err error
				if conf, err = focus.getConf(candidates.ConfID, false); err != nil {
					log.Printf("Got candidates for unknown conf %s", candidates.ConfID)
					return true
				}
				if call, err = conf.getCall(candidates.CallID, false); err != nil {
					log.Printf("Got candidates for unknown call %s in conf %s", candidates.CallID, candidates.ConfID)
					return true
				}
				call.onCandidates(candidates)
			case CallAnswer.Type:
				log.Printf("Ignoring unimplemented event of type %s", evt.Type.Type)
			case CallReject.Type:
				log.Printf("Ignoring unimplemented event of type %s", evt.Type.Type)
			case CallSelectAnswer.Type:
				log.Printf("Ignoring unimplemented event of type %s", evt.Type.Type)
			case CallNegotiate.Type:
				log.Printf("Ignoring unimplemented event of type %s", evt.Type.Type)
			case CallHangup.Type:
				log.Printf("Ignoring unimplemented event of type %s", evt.Type.Type)
			default:
				log.Printf("Ignoring unrecognised to-device event of type %s", evt.Type.Type)
			}
		}

		return true
	})

	err = client.Sync()
	if err != nil {
		log.Panic("Sync failed", err)
	}

	return nil
}
