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
	trackID  string
	streamID string
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
	callID         string
	userID         id.UserID
	deviceID       id.DeviceID
	client         *mautrix.Client
	peerConnection *webrtc.PeerConnection
	callState      callState
	conf           *conf
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

func (f *focus) getConf(confID string, create bool) (*conf, error) {
	f.confs.confsMu.Lock()
	defer f.confs.confsMu.Unlock()
	co := f.confs.confs[confID]
	if co == nil {
		if create {
			co := conf{
				confID: confID,
			}
			f.confs.confs[confID] = &co
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
			ca := call{
				callID:    callID,
				conf:      c,
				callState: WaitLocalMedia,
			}
			c.calls.calls[callID] = &ca
		} else {
			return nil, errors.New("No such call")
		}
	}
	return ca, nil
}

func (c *conf) localTrackLookup(streamID, trackID string) (track webrtc.TrackLocal) {
	c.trackDetailsMu.Lock()
	defer c.trackDetailsMu.Unlock()
	return c.trackDetails[trackKey{
		streamID: streamID,
		trackID:  trackID,
	}].track
}

func (c *call) dataChannelHandler(peerConnection *webrtc.PeerConnection, d *webrtc.DataChannel) {
	sendError := func(errMsg string) {
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

		switch msg.Op {
		case "select":
			if err := peerConnection.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeOffer,
				SDP:  msg.SDP,
			}); err != nil {
				panic(err)
			}

			for _, trackDesc := range msg.Start {
				track := c.conf.localTrackLookup(trackDesc.streamID, trackDesc.trackID)

				// TODO: hook cascade back up.
				// As we're not an AS, we'd rely on the client
				// to send us a "connect" op to tell us how to
				// connect to another focus in order to select
				// its streams.

				if track == nil {
					sendError("No Such Track")
					return
				}

				if track != nil {
					if _, err := peerConnection.AddTrack(track); err != nil {
						panic(err)
					}
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
			streamID: trackRemote.StreamID(),
			trackID:  trackRemote.ID(),
		}] = &trackDetail{
			call:  c,
			track: trackLocal,
		}
		c.conf.trackDetailsMu.Unlock()

		copyRemoteToLocal(trackRemote, trackLocal)
	})

	peerConnection.OnDataChannel(func(d *webrtc.DataChannel) {
		c.dataChannelHandler(peerConnection, d)
	})

	peerConnection.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offer.SDP,
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

	peerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		ice := candidate.ToJSON()

		// TODO: batch these up a bit
		candidateEvtContent := &event.Content{
			Parsed: event.CallCandidatesEventContent{
				BaseCallEventContent: event.BaseCallEventContent{
					CallID:  c.callID,
					ConfID:  c.conf.confID,
					PartyID: string(c.client.DeviceID),
					Version: event.CallVersion("1"),
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

		toDevice := &mautrix.ReqSendToDevice{
			Messages: map[id.UserID]map[id.DeviceID]*event.Content{
				c.userID: {
					c.deviceID: candidateEvtContent,
				},
			},
		}

		// TODO: E2EE
		// TODO: to-device reliability
		c.client.SendToDevice(event.CallCandidates, toDevice)
	})

	// TODO: send any subsequent candidates we discover to the peer

	answerSdp := peerConnection.LocalDescription().SDP

	// TODO: sessions
	answerEvtContent := &event.Content{
		Parsed: event.CallAnswerEventContent{
			BaseCallEventContent: event.BaseCallEventContent{
				CallID:  c.callID,
				ConfID:  c.conf.confID,
				PartyID: string(c.client.DeviceID),
				Version: event.CallVersion("1"),
			},
			Answer: event.CallData{
				Type: "answer",
				SDP:  answerSdp,
			},
		},
	}

	toDevice := &mautrix.ReqSendToDevice{
		Messages: map[id.UserID]map[id.DeviceID]*event.Content{
			c.userID: {
				c.deviceID: answerEvtContent,
			},
		},
	}

	// TODO: E2EE
	// TODO: to-device reliability
	c.client.SendToDevice(event.CallAnswer, toDevice)

	return err
}

func (c *call) onCandidates(content *event.CallCandidatesEventContent) error {
	// TODO: tell our peerConnection about the new candidates we just discovered
	log.Print("ignoring candidates as not yet implemented", content)
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
