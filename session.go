package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
)

type streamDetail struct {
	callID, deviceID, purpose string
	track                     *webrtc.TrackLocalStaticRTP
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
		callID, deviceID, purpose string
	)

	peerConnection.OnTrack(func(trackRemote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		id := "audio"
		if strings.Contains(trackRemote.Codec().MimeType, "video") {
			id = "video"

			// Send a PLI on an interval so that the publisher is pushing a keyframe every rtcpPLIInterval
			go func() {
				ticker := time.NewTicker(time.Millisecond * 200)
				for range ticker.C {
					if errSend := peerConnection.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(trackRemote.SSRC())}}); errSend != nil {
						fmt.Println(errSend)
					}
				}
			}()

		}

		publishDetailsMu.Lock()
		streamDetailsMu.Lock()
		trackLocal, err := webrtc.NewTrackLocalStaticRTP(trackRemote.Codec().RTPCodecCapability, id, fmt.Sprintf("%s-%s-%s", callID, deviceID, purpose))
		if err != nil {
			panic(err)
		}

		streamDetails = append(streamDetails, streamDetail{
			callID:   callID,
			deviceID: deviceID,
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

		sendError := func(errMsg string) {
			marshaled, err := json.Marshal(&dataChannelMessage{
				Event:   "error",
				Message: errMsg,
			})
			if err != nil {
				panic(err)
			}

			if err = d.SendText(string(marshaled)); err != nil {
				panic(err)
			}
		}

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
				callID = msg.CallID
				deviceID = msg.DeviceID
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
				var audioTrack, videoTrack webrtc.TrackLocal
				for _, s := range streamDetails {
					if s.callID == msg.CallID && s.deviceID == msg.DeviceID && s.purpose == msg.Purpose {
						if s.track.Kind() == webrtc.RTPCodecTypeAudio {
							audioTrack = s.track
						} else {
							videoTrack = s.track
						}
					}
				}

				if audioTrack == nil || videoTrack == nil {
					sendError("No Such Stream")
					return
				}

				if err := peerConnection.SetRemoteDescription(webrtc.SessionDescription{
					Type: webrtc.SDPTypeOffer,
					SDP:  msg.SDP,
				}); err != nil {
					panic(err)
				}

				if _, err = peerConnection.AddTrack(audioTrack); err != nil {
					panic(err)
				}

				if _, err = peerConnection.AddTrack(videoTrack); err != nil {
					panic(err)
				}

				answer, err := peerConnection.CreateAnswer(nil)
				if err != nil {
					panic(err)
				}

				if err := peerConnection.SetLocalDescription(answer); err != nil {
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
