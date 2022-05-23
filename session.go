package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/pion/webrtc/v3"
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

	peerConnection.OnTrack(func(*webrtc.TrackRemote, *webrtc.RTPReceiver) {
		fmt.Println("OnTrack Fired")
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

				if err = peerConnection.SetLocalDescription(answer); err != nil {
					panic(err)
				}

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
