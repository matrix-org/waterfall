package main

import (
	"log"
	"reflect"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func initMatrix(config config) error {
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
		name: fmt.printf("%s (%s)", config.UserID, config.DeviceID),
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
		log.Print("event", event)
		invite := event.Content.AsCallInvite()
		conf := focus.getConf(invite.ConfID, true)
		call := conf.getCall(invite.CallID, true)
		call.userID = event.Sender
		call.deviceID = invite.DeviceID
		// TODO: check session IDs
		call.onInvite(event)
	})

	syncer.OnEventType(CallCandidates, func(_ mautrix.EventSource, event *event.Event) {
		log.Print("event", event)
		if conf := focus.getConf(event.ConfID); err != nil {
			log.Printf("Got candidates for unknown conf %s", event.ConfID)
			return
		}
		if call := conf.getCall(event.CallID); err != nil {
			log.Printf("Got candidates for unknown call %s in conf %s", event.CallID, event.ConfID)
			return
		}
		call.onCandidates(event.Content.AsCallCandidates())
	})

	syncer.OnEventType(CallAnswer, func(_ mautrix.EventSource, event *event.Event) {
		log.Print("event", event)
		// until we have cascading hooked up, we should never be receiving answer events
		log.Printf("Ignoring unexpected answer event")
	})

	syncer.OnEventType(CallReject, func(_ mautrix.EventSource, event *event.Event) {
		log.Print("event", event)
		// until we have cascading hooked up, we should never be receiving reject events
		log.Printf("Ignoring unexpected reject event")
	})

	syncer.OnEventType(CallSelectAnswer, func(_ mautrix.EventSource, event *event.Event) {
		log.Print("event", event)
		// until we have cascading hooked up, we should never be receiving answer events
		log.Printf("Ignoring unexpected select answer event")
	})

	syncer.OnEventType(CallNegotiate, func(_ mautrix.EventSource, event *event.Event) {
		log.Print("event", event)
		// TODO: process SDP renegotiation
	})

	syncer.OnEventType(CallHangup, func(_ mautrix.EventSource, event *event.Event) {
		log.Print("event", event)
		// TODO: process hangups
	})

	err = client.Sync()
	if err != nil {
		log.Panic("Sync failed", err)
	}
}
