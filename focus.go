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

type trackDetail struct {
	call     *call
	track    *webrtc.TrackLocalStaticRTP
}

type setTrackDetails func(call *call, track *webrtc.TrackLocal)

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

// FIXME: for uniqueness, should we index tracks by {callID, streamID, trackID}?
type trackKey struct {
	streamID string
	trackID  string
}

type conf struct {
	confID         string
	calls          calls
	trackDetailsMu sync.RWMutex
	trackDetails   map[trackKey]*trackDetail // by trackID.
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
			co.trackDetails = make(map[trackKey]*trackDetail)
		} else {
			return nil, errors.New("No such conf")
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
			return nil, errors.New("No such call")
		}
	}
	return ca, nil
}

func (c *conf) localTrackLookup(streamID, trackID string) (track webrtc.TrackLocal, err error) {
	log.Printf("localTrackLookup called for %s %s", streamID, trackID)
	c.trackDetailsMu.Lock()
	defer c.trackDetailsMu.Unlock()

	trackDetail := c.trackDetails[trackKey{
		streamID: streamID,
		trackID:  trackID,
	}]

	log.Printf("localTrackLookup returning with trackDetail %+v", trackDetail)

	if trackDetail == nil {
		return nil, errors.New("No such track")
	} else {
		return trackDetail.track, nil
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
				track, err := c.conf.localTrackLookup(trackDesc.StreamID, trackDesc.TrackID)
				if err != nil {
					sendError("No Such Track")
					return
				} else {
					tracks = append(tracks, track)
				}
			}

			if err := peerConnection.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeOffer,
				SDP:  msg.SDP,
			}); err != nil {
				panic(err)
			}

			for _, track := range tracks {
				log.Printf("%s | adding track %s", c.callID, track.ID())
				if _, err := peerConnection.AddTrack(track); err != nil {
					panic(err)
				}
			}

			// TODO: hook up msg.Stop to unsubscribe from tracks

			answer, err := peerConnection.CreateAnswer(nil)
			if err != nil {
				panic(err)
			}

			if err := peerConnection.SetLocalDescription(answer); err != nil {
				panic(err)
			}

			response := dataChannelMessage{
				Op:  "answer",
				ID:  msg.ID,
				SDP: answer.SDP,
			}
			marshaled, err := json.Marshal(response)
			if err != nil {
				panic(err)
			}

			log.Printf("%s | Sending DC %s", c.callID, response.Op)

			if err = d.SendText(string(marshaled)); err != nil {
				panic(err)
			}
		default:
			log.Fatalf("Unknown operation %s", msg.Op)
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
		log.Printf("%s | discovered track on PC with id %s, streamID %s and codec %+v", c.callID, trackRemote.ID(), trackRemote.StreamID(), trackRemote.Codec())
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

		c.conf.trackDetailsMu.Lock()
		trackLocal, err := webrtc.NewTrackLocalStaticRTP(trackRemote.Codec().RTPCodecCapability, id, fmt.Sprintf("%s-%s-%s", c.callID, c.deviceID, trackRemote.ID()))
		if err != nil {
			panic(err)
		}

		c.conf.trackDetails[trackKey{
			streamID: "unknown",
			trackID:  trackRemote.ID(),
		}] = &trackDetail{
			call:  c,
			track: trackLocal,
		}
		log.Printf("%s | published %s %s", c.callID, "unknown", trackRemote.ID())
		c.conf.trackDetailsMu.Unlock()

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
		if (candidate == nil) {
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
					event.CallCandidate{
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
			log.Printf("Failed to add ICE candidate", content)
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
