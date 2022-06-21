package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v3"
)

type streamDetail struct {
	callID, deviceID, purpose string
	ssrc                      webrtc.SSRC
	track                     *webrtc.TrackLocalStaticRTP
}

type foci struct {
	name            string
	streamDetailsMu sync.RWMutex
	streamDetails   []streamDetail
}

func (f *foci) localStreamLookup(msg dataChannelMessage) (audioTrack, videoTrack webrtc.TrackLocal) {
	f.streamDetailsMu.Lock()
	defer f.streamDetailsMu.Unlock()

	for _, s := range f.streamDetails {
		if s.track != nil && s.callID == msg.CallID && s.deviceID == msg.DeviceID && s.purpose == msg.Purpose {
			if s.track.Kind() == webrtc.RTPCodecTypeAudio {
				audioTrack = s.track
			} else {
				videoTrack = s.track
			}
		}
	}
	return
}

func (f *foci) dataChannelHandler(peerConnection *webrtc.PeerConnection, d *webrtc.DataChannel) {
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

			parsedOffer := &sdp.SessionDescription{}
			if err := parsedOffer.Unmarshal([]byte(msg.SDP)); err != nil {
				log.Fatal(err)
			}

			f.streamDetailsMu.Lock()
			for _, mid := range msg.MIDs {
				for _, mediaSection := range parsedOffer.MediaDescriptions {
					ssrc := int(0)
					foundMid := false
					err := error(nil)

					for _, attribute := range mediaSection.Attributes {
						if attribute.Key == "mid" && attribute.Value == mid {
							foundMid = true
						}

						if attribute.Key == "ssrc" && ssrc == 0 {
							if ssrc, err = strconv.Atoi(strings.Split(attribute.Value, " ")[0]); err != nil {
								log.Fatal(err)
							}
						}
					}

					if ssrc != 0 && foundMid {
						f.streamDetails = append(f.streamDetails, streamDetail{
							callID:   msg.CallID,
							deviceID: msg.DeviceID,
							purpose:  msg.Purpose,
							ssrc:     webrtc.SSRC(ssrc),
						})
					}
				}
			}
			f.streamDetailsMu.Unlock()

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

		var trackLocal *webrtc.TrackLocalStaticRTP
		f.streamDetailsMu.Lock()
		for i := range f.streamDetails {
			if f.streamDetails[i].ssrc == trackRemote.SSRC() {
				f.streamDetails[i].track, err = webrtc.NewTrackLocalStaticRTP(trackRemote.Codec().RTPCodecCapability, id, fmt.Sprintf("%s-%s-%s", f.streamDetails[i].callID, f.streamDetails[i].deviceID, f.streamDetails[i].purpose))
				if err != nil {
					panic(err)
				}
				trackLocal = f.streamDetails[i].track
			}
		}
		f.streamDetailsMu.Unlock()

		if trackLocal != nil {
			copyRemoteToLocal(trackRemote, trackLocal)
		}
	})

	peerConnection.OnDataChannel(func(d *webrtc.DataChannel) {
		f.dataChannelHandler(peerConnection, d)
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
