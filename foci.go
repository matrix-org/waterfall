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

type setStreamDetails func(newCallID, newDeviceID, newPurpose string)

type foci struct {
	name            string
	streamDetailsMu sync.RWMutex
	streamDetails   []streamDetail
}

func (f *foci) localStreamLookup(msg dataChannelMessage) (audioTrack, videoTrack webrtc.TrackLocal) {
	f.streamDetailsMu.Lock()
	defer f.streamDetailsMu.Unlock()

	for _, s := range f.streamDetails {
		if s.callID == msg.CallID && s.deviceID == msg.DeviceID && s.purpose == msg.Purpose {
			if s.track.Kind() == webrtc.RTPCodecTypeAudio {
				audioTrack = s.track
			} else {
				videoTrack = s.track
			}
		}
	}
	return
}

func (f *foci) dataChannelHandler(peerConnection *webrtc.PeerConnection, d *webrtc.DataChannel, setPublishDetails setStreamDetails) {
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

			setPublishDetails(msg.CallID, msg.DeviceID, msg.Purpose)

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

			if msg.FOCI == f.name {
				audioTrack, videoTrack = f.localStreamLookup(*msg)
			} else {
				audioTrack, videoTrack = remoteStreamLookup(*msg)
			}

			if audioTrack == nil && videoTrack == nil {
				sendError("No Such Stream")
				return
			}

			if err := peerConnection.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeOffer,
				SDP:  msg.SDP,
			}); err != nil {
				panic(err)
			}

			if audioTrack != nil {
				if _, err := peerConnection.AddTrack(audioTrack); err != nil {
					panic(err)
				}
			}

			if videoTrack != nil {
				if _, err := peerConnection.AddTrack(videoTrack); err != nil {
					panic(err)
				}
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
}

func (f *foci) handleCreateSession(w http.ResponseWriter, r *http.Request) error {
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
	setPublishDetails := func(newCallID, newDeviceID, newPurpose string) {
		publishDetailsMu.Lock()
		defer publishDetailsMu.Unlock()

		callID = newCallID
		deviceID = newDeviceID
		purpose = newPurpose
	}

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
		f.streamDetailsMu.Lock()
		trackLocal, err := webrtc.NewTrackLocalStaticRTP(trackRemote.Codec().RTPCodecCapability, id, fmt.Sprintf("%s-%s-%s", callID, deviceID, purpose))
		if err != nil {
			panic(err)
		}

		f.streamDetails = append(f.streamDetails, streamDetail{
			callID:   callID,
			deviceID: deviceID,
			purpose:  purpose,
			track:    trackLocal,
		})
		f.streamDetailsMu.Unlock()
		publishDetailsMu.Unlock()

		copyRemoteToLocal(trackRemote, trackLocal)
	})

	peerConnection.OnDataChannel(func(d *webrtc.DataChannel) {
		f.dataChannelHandler(peerConnection, d, setPublishDetails)
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

func copyRemoteToLocal(trackRemote *webrtc.TrackRemote, trackLocal *webrtc.TrackLocalStaticRTP) {
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

}
