package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/pion/webrtc/v3"
)

type streamDetail struct {
	callId, deviceId, purpose string
	track                     webrtc.TrackLocal
}

var (
	streamDetailsMu sync.RWMutex
	streamDetails   []streamDetail
)

func handleCreateSession(w http.ResponseWriter, r *http.Request) error {
	offer, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}

	peerConnection, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return err
	}

	var (
		publishDetailsMu          sync.RWMutex
		callId, deviceId, purpose string
	)

	peerConnection.OnTrack(func(trackRemote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		id := "video"
		if strings.Contains(trackRemote.Codec().MimeType, "audio") {
			id = "audio"
		}

		publishDetailsMu.Lock()
		streamDetailsMu.Lock()
		trackLocal, err := webrtc.NewTrackLocalStaticRTP(trackRemote.Codec().RTPCodecCapability, id, fmt.Sprintf("%s-%s-%s", callId, deviceId, purpose))
		if err != nil {
			panic(err)
		}

		streamDetails = append(streamDetails, streamDetail{
			callId:   callId,
			deviceId: deviceId,
			purpose:  purpose,
			track:    trackLocal,
		})
		streamDetailsMu.Unlock()
		publishDetailsMu.Unlock()

		buff := make([]byte, 1500)
		for {
			i, _, err := trackRemote.Read(buff)
			if err != nil {
				panic(err)
			}

			if _, err = trackLocal.Write(buff[:i]); err != nil {
				panic(err)
			}
		}
	})

	peerConnection.OnDataChannel(func(d *webrtc.DataChannel) {
		d.OnMessage(func(m webrtc.DataChannelMessage) {
			if !m.IsString {
				log.Fatal("Inbound message is not string")
			}

			msg := &dataChannelMessage{}
			if err := json.Unmarshal(m.Data, msg); err != nil {
				log.Fatal(err)
			}

			switch msg.Event {
			case "publish":
				if err := peerConnection.SetRemoteDescription(webrtc.SessionDescription{
					Type: webrtc.SDPTypeOffer,
					SDP:  msg.SDP,
				}); err != nil {
					panic(err)
				}

				answer, err := peerConnection.CreateAnswer(nil)
				if err != nil {
					panic(err)
				}

				if err := peerConnection.SetLocalDescription(answer); err != nil {
					panic(err)
				}

				publishDetailsMu.Lock()
				callId = msg.CallID
				deviceId = msg.DeviceID
				purpose = msg.Purpose
				publishDetailsMu.Unlock()

				msg.SDP = answer.SDP
				marshaled, err := json.Marshal(msg)
				if err != nil {
					panic(err)
				}

				if err = d.SendText(string(marshaled)); err != nil {
					panic(err)
				}
			case "subscribe":
			default:
				log.Fatalf("Unknown msg Event type %s", msg.Event)
			}
		})
	})

	peerConnection.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  string(offer),
	})

	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		return err
	}

	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)
	if err = peerConnection.SetLocalDescription(answer); err != nil {
		return err
	}
	<-gatherComplete

	_, err = fmt.Fprintf(w, peerConnection.LocalDescription().SDP)
	return err
}
