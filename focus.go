package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
)

// stolen from matrix-js-sdk
// TODO: actually use callState (will be needed for renegotiation)
const (
	Fledgling      = "fledgling"
	InviteSent     = "invite_sent"
	WaitLocalMedia = "wait_local_media"
	CreateOffer    = "create_offer"
	CreateAnswer   = "create_answer"
	Connecting     = "connecting"
	Connected      = "connected"
	Ringing        = "ringing"
	Ended          = "ended"
)

type callState string

type call struct {
	callID          string
	userID          id.UserID
	deviceID        id.DeviceID
	localSessionID  string
	remoteSessionID string
	client          *mautrix.Client
	peerConnection  *webrtc.PeerConnection
	callState       callState
	conf            *conf
	// we track the call's tracks via the conf object.
}

type calls struct {
	callsMu sync.RWMutex
	calls   map[string]*call
}

type conf struct {
	confID   string
	calls    calls
	tracksMu sync.RWMutex
	tracks   map[string][]*webrtc.TrackLocalStaticRTP // by streamId.
}

type confs struct {
	confsMu sync.RWMutex
	confs   map[string]*conf
}

type focus struct {
	name  string
	confs confs
}

func (f *focus) Init(name string) {
	f.name = name
	f.confs.confs = make(map[string]*conf)
}

func (f *focus) getConf(confID string, create bool) (*conf, error) {
	f.confs.confsMu.Lock()
	defer f.confs.confsMu.Unlock()
	co := f.confs.confs[confID]
	if co == nil {
		if create {
			co = &conf{
				confID: confID,
			}
			f.confs.confs[confID] = co
			co.calls.calls = make(map[string]*call)
			co.tracks = make(map[string][]*webrtc.TrackLocalStaticRTP)
		} else {
			return nil, errors.New("no such conf")
		}
	}
	return co, nil
}

func (c *conf) getCall(callID string, create bool) (*call, error) {
	c.calls.callsMu.Lock()
	defer c.calls.callsMu.Unlock()
	ca := c.calls.calls[callID]
	if ca == nil {
		if create {
			ca = &call{
				callID:    callID,
				conf:      c,
				callState: WaitLocalMedia,
			}
			c.calls.calls[callID] = ca
		} else {
			return nil, errors.New("no such call")
		}
	}
	return ca, nil
}

func (c *conf) getLocalTrackByStreamId(streamID string) (tracks []webrtc.TrackLocal, err error) {
	c.tracksMu.Lock()
	defer c.tracksMu.Unlock()

	foundTracks := c.tracks[streamID]
	if foundTracks == nil {
		log.Printf("Found no streams for %s", streamID)
		return nil, errors.New("no such streams")
	} else {
		tracksToReturn := []webrtc.TrackLocal{}
		for _, track := range foundTracks {
			tracksToReturn = append(tracksToReturn, track)
		}
		return tracksToReturn, nil
	}
}

func (c *call) dataChannelHandler(d *webrtc.DataChannel) {
	peerConnection := c.peerConnection

	sendError := func(errMsg string) {
		log.Printf("%s | sending DC error %s", c.callID, errMsg)
		marshaled, err := json.Marshal(&dataChannelMessage{
			Op:      "error",
			Message: errMsg,
		})
		if err != nil {
			panic(err)
		}

		if err = d.SendText(string(marshaled)); err != nil {
			panic(err)
		}
	}

	d.OnOpen(func() {
		log.Printf("DC opened on call %s", c.callID)
	})

	d.OnClose(func() {
		log.Printf("DC closed on call %s", c.callID)
	})

	d.OnError(func(err error) {
		log.Fatalf("DC error on call %s: %s", c.callID, err)
	})

	d.OnMessage(func(m webrtc.DataChannelMessage) {
		if !m.IsString {
			log.Fatal("Inbound message is not string")
		}

		msg := &dataChannelMessage{}
		if err := json.Unmarshal(m.Data, msg); err != nil {
			log.Fatal(err)
		}

		log.Printf("%s | Received DC %s confId=%s start=%+v", c.callID, msg.Op, msg.ConfID, msg.Start)

		// TODO: hook cascade back up.
		// As we're not an AS, we'd rely on the client
		// to send us a "connect" op to tell us how to
		// connect to another focus in order to select
		// its streams.

		switch msg.Op {
		case "select":
			var tracks []webrtc.TrackLocal
			for _, trackDesc := range msg.Start {
				foundTracks, err := c.conf.getLocalTrackByStreamId(trackDesc.StreamID)
				if err != nil {
					sendError("No Such Stream")
					return
				} else {
					tracks = append(tracks, foundTracks...)
				}
			}

			for _, track := range tracks {
				log.Printf("%s | adding %s track with StreamID %s", c.callID, track.Kind(), track.StreamID())
				if _, err := peerConnection.AddTrack(track); err != nil {
					panic(err)
				}
			}

			offer, err := peerConnection.CreateOffer(nil)
			if err != nil {
				panic(err)
			}
			err = peerConnection.SetLocalDescription(offer)
			if err != nil {
				panic(err)
			}

			response := dataChannelMessage{
				Op:  "offer",
				ID:  msg.ID,
				SDP: offer.SDP,
			}
			marshaled, err := json.Marshal(response)
			if err != nil {
				panic(err)
			}
			err = d.SendText(string(marshaled))
			if err != nil {
				panic(err)
			}

			log.Printf("%s | Sent DC %s", c.callID, response.Op)

		case "answer":
			peerConnection.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeAnswer,
				SDP:  msg.SDP,
			})

		default:
			log.Fatalf("Unknown operation %s", msg.Op)
			// TODO: hook up msg.Stop to unsubscribe from tracks
		}
	})
}

func (c *call) onInvite(content *event.CallInviteEventContent) error {
	offer := content.Offer

	peerConnection, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return err
	}
	c.peerConnection = peerConnection

	peerConnection.OnTrack(func(trackRemote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		log.Printf("%s | discovered track with streamID %s and kind %s", c.callID, trackRemote.StreamID(), trackRemote.Kind())
		if strings.Contains(trackRemote.Codec().MimeType, "video") {
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

		c.conf.tracksMu.Lock()
		trackLocal, err := webrtc.NewTrackLocalStaticRTP(trackRemote.Codec().RTPCodecCapability, trackRemote.Kind().String(), trackRemote.StreamID())
		if err != nil {
			panic(err)
		}

		if c.conf.tracks[trackLocal.StreamID()] == nil {
			receivedTracks := []*webrtc.TrackLocalStaticRTP{trackLocal}
			c.conf.tracks[trackLocal.StreamID()] = receivedTracks

		} else {
			receivedTracks := append(c.conf.tracks[trackLocal.StreamID()], trackLocal)
			c.conf.tracks[trackLocal.StreamID()] = receivedTracks

		}

		log.Printf("%s | published track with streamID %s and kind %s", c.callID, trackLocal.StreamID(), trackLocal.Kind())
		c.conf.tracksMu.Unlock()

		copyRemoteToLocal(trackRemote, trackLocal)
	})

	peerConnection.OnDataChannel(func(d *webrtc.DataChannel) {
		c.dataChannelHandler(d)
	})

	peerConnection.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offer.SDP,
	})

	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		return err
	}

	// TODO: trickle ICE for fast conn setup, rather than block here
	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)
	if err = peerConnection.SetLocalDescription(answer); err != nil {
		return err
	}
	<-gatherComplete

	answerSdp := peerConnection.LocalDescription().SDP

	answerEvtContent := &event.Content{
		Parsed: event.CallAnswerEventContent{
			BaseCallEventContent: event.BaseCallEventContent{
				CallID:          c.callID,
				ConfID:          c.conf.confID,
				DeviceID:        c.client.DeviceID,
				SenderSessionID: c.localSessionID,
				DestSessionID:   c.remoteSessionID,
				PartyID:         string(c.client.DeviceID),
				Version:         event.CallVersion("1"),
			},
			Answer: event.CallData{
				Type: "answer",
				SDP:  answerSdp,
			},
		},
	}
	c.sendToDevice(event.CallAnswer, answerEvtContent)

	peerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}

		ice := candidate.ToJSON()

		log.Printf("%s | discovered local candidate %s", c.callID, ice.Candidate)

		// TODO: batch these up a bit
		candidateEvtContent := &event.Content{
			Parsed: event.CallCandidatesEventContent{
				BaseCallEventContent: event.BaseCallEventContent{
					CallID:          c.callID,
					ConfID:          c.conf.confID,
					DeviceID:        c.client.DeviceID,
					SenderSessionID: c.localSessionID,
					DestSessionID:   c.remoteSessionID,
					PartyID:         string(c.client.DeviceID),
					Version:         event.CallVersion("1"),
				},
				Candidates: []event.CallCandidate{
					{
						Candidate:     ice.Candidate,
						SDPMLineIndex: int(*ice.SDPMLineIndex),
						SDPMID:        *ice.SDPMid,
						// XXX: what about ice.UsernameFragment?
					},
				},
			},
		}
		c.sendToDevice(event.CallCandidates, candidateEvtContent)
	})
	return err
}

func (c *call) sendToDevice(callType event.Type, content *event.Content) error {
	log.Printf("%s | sending to device %s", c.callID, callType.Type)
	toDevice := &mautrix.ReqSendToDevice{
		Messages: map[id.UserID]map[id.DeviceID]*event.Content{
			c.userID: {
				c.deviceID: content,
			},
		},
	}

	// TODO: E2EE
	// TODO: to-device reliability
	c.client.SendToDevice(callType, toDevice)

	return nil
}

func (c *call) onCandidates(content *event.CallCandidatesEventContent) error {
	for _, candidate := range content.Candidates {
		sdpMLineIndex := uint16(candidate.SDPMLineIndex)
		ice := webrtc.ICECandidateInit{
			Candidate:     candidate.Candidate,
			SDPMLineIndex: &sdpMLineIndex,
			SDPMid:        &candidate.SDPMID,
		}
		if err := c.peerConnection.AddICECandidate(ice); err != nil {
			log.Print("Failed to add ICE candidate", content)
			return err
		}
	}
	return nil
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
