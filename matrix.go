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

	syncer.OnEventType(CallInvite, func(_ mautrix.EventSource, event *event.Event) {
		log.Printf("event %+v", event)
		invite := event.Content.AsCallInvite()
		conf, _ := focus.getConf(invite.ConfID, true)
		call, _ := conf.getCall(invite.CallID, true)
		call.userID = event.Sender
		call.deviceID = invite.DeviceID
		// TODO: check session IDs
		call.onInvite(invite)
	})

	syncer.OnEventType(CallCandidates, func(_ mautrix.EventSource, event *event.Event) {
		log.Printf("event %+v", event)
		candidates := event.Content.AsCallCandidates()
		var conf *conf
		var call *call
		var err error
		if conf, err = focus.getConf(candidates.ConfID, false); err != nil {
			log.Printf("Got candidates for unknown conf %s", candidates.ConfID)
			return
		}
		if call, err = conf.getCall(candidates.CallID, false); err != nil {
			log.Printf("Got candidates for unknown call %s in conf %s", candidates.CallID, candidates.ConfID)
			return
		}
		call.onCandidates(candidates)
	})

	syncer.OnEventType(CallAnswer, func(_ mautrix.EventSource, event *event.Event) {
		log.Printf("event %+v", event)
		// until we have cascading hooked up, we should never be receiving answer events
		log.Print("Ignoring unexpected answer event")
	})

	syncer.OnEventType(CallReject, func(_ mautrix.EventSource, event *event.Event) {
		log.Printf("event %+v", event)
		// until we have cascading hooked up, we should never be receiving reject events
		log.Print("Ignoring unexpected reject event")
	})

	syncer.OnEventType(CallSelectAnswer, func(_ mautrix.EventSource, event *event.Event) {
		log.Printf("event %+v", event)
		// until we have cascading hooked up, we should never be receiving answer events
		log.Print("Ignoring unexpected select answer event")
	})

	syncer.OnEventType(CallNegotiate, func(_ mautrix.EventSource, event *event.Event) {
		log.Printf("event %+v", event)
		// TODO: process SDP renegotiation
	})

	syncer.OnEventType(CallHangup, func(_ mautrix.EventSource, event *event.Event) {
		log.Printf("event %+v", event)
		// TODO: process hangups
	})

	syncer.OnEvent(func(source mautrix.EventSource, evt *event.Event) {
		log.Printf("onEvent %+v %+v", source, evt);
	})

	syncer.OnSync(func(resp *mautrix.RespSync, since string) bool {
		log.Printf("synced %+v %+v", resp, since);
		return true
	})

	err = client.Sync()
	if err != nil {
		log.Panic("Sync failed", err)
	}

	return nil
}
