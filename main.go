package main

import (
	"flag"
	"fmt"
    "github.com/rs/zerolog/log"
	yaml "gopkg.in/yaml.v3"
	"io/ioutil"
	"reflect"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func loadConfig(configFilePath string) (*config, error) {
	log.Info().Msgf("loaded %s", configFilePath)
	file, err := ioutil.ReadFile(configFilePath)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to read config")
	}
	var config config
	if err := yaml.Unmarshal(file, &config); err != nil {
		return nil, fmt.Errorf("Failed to unmarshal YAML: %s", err)
	}
	return &config, nil
}

func main() {
	configFilePath := flag.String("config", "config.yaml", "Configuration file path")
	flag.Parse()

	var config *config
	var err error
	if config, err = loadConfig(*configFilePath); err != nil {
		log.Fatal().Err(err).Msg("Failed to load config file")
	}

	client, err := mautrix.NewClient(config.HomeserverURL, config.UserID, config.AccessToken)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create client")
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

	syncer.OnEventType(CallInvite, func(_ mautrix.EventSource, event *event.Event) {
		log.Info().Interface("event", event)
	})
	syncer.OnEventType(CallCandidates, func(_ mautrix.EventSource, event *event.Event) {
		log.Info().Interface("event", event)
	})
	syncer.OnEventType(CallAnswer, func(_ mautrix.EventSource, event *event.Event) {
		log.Info().Interface("event", event)
	})
	syncer.OnEventType(CallReject, func(_ mautrix.EventSource, event *event.Event) {
		log.Info().Interface("event", event)
	})
	syncer.OnEventType(CallSelectAnswer, func(_ mautrix.EventSource, event *event.Event) {
		log.Info().Interface("event", event)
	})
	syncer.OnEventType(CallNegotiate, func(_ mautrix.EventSource, event *event.Event) {
		log.Info().Interface("event", event)
	})
	syncer.OnEventType(CallHangup, func(_ mautrix.EventSource, event *event.Event) {
		log.Info().Interface("event", event)
	})

	// TODO: actually hook up events and hook up sessions in the SFU
	// if err := handleCreateSession(w, r); err != nil {
	// 	log.Fatal(err)
	// }

	err = client.Sync()
	if err != nil {
		log.Panic().Err(err).Msg("Sync failed")
	}
}

type config struct {
	UserID        id.UserID
	HomeserverURL string
	AccessToken   string
	DeviceID      id.DeviceID
}

type dataChannelMessage struct {
	Event    string `json:"event"`
	ID       string `json:"id"`
	CallID   string `json:"call_id"`
	DeviceID string `json:"device_id"`
	Purpose  string `json:"purpose"`
	SDP      string `json:"sdp"`
}
