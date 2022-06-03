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
	userID         string
	deviceID       string
	client         *Client
	peerConnection *peerConnection
	callState      callState
	conf           *conf
	// we track the call's tracks via the conf object.
}

type calls struct {
	callsMu sync.RWMutex
	calls   map[string]*call
}

type conf struct {
	confID         string
	calls          calls
	trackDetailsMu sync.RWMutex
	// FIXME: we should really index tracks by {streamID, trackID}
	trackDetails map[string]*trackDetail // by trackID.
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
	conf := f.confs.confs[confID]
	if conf == nil {
		if create {
			conf := &conf{
				confID: confID,
			}
			f.confs.confs[confID] = conf
		} else {
			return _, error("No such conf")
		}
	}
	return &conf
}

func (c *conf) getCall(callID string, create bool) (*call, error) {
	c.calls.callsMu.Lock()
	defer c.calls.callsMu.Unlock()
	call := c.calls.calls[callID]
	if call == nil {
		if create {
			call := &call{
				callID:    callID,
				conf:      c,
				callState: WaitLocalMedia,
			}
			c.calls.calls[callID] = call
		} else {
			return _, error("No such call")
		}
	}
	return &conf
}

func (c *conf) localTrackLookup(trackID string) (track webrtc.TrackLocal) {
	c.trackDetailsMu.Lock()
	defer c.trackDetailsMu.Unlock()
	return trackDetails[trackID]
}

func (c *conf) dataChannelHandler(peerConnection *webrtc.PeerConnection, d *webrtc.DataChannel) {
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

		switch msg.Op {
		case "select":
			// XXX: do we actually need to call setRemoteDescription
			// at this point, given it shouldn't have changed since the
			// caller connected?

			for _, trackDesc := range msg.Start {
				// TODO: we should really be indexed by streamID too here
				track := c.localTrackLookup(trackDesc.TrackID)

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

			// XXX: do we actually have to send the answer back to th caller?
			// if so, do we have a problem that the updated SDP is sent over
			// slow to-device messages rather than directly over DC, as per
			// Sean's original POC?  In terms of speed of selection...

		default:
			log.Fatalf("Unknown msg Event type %s", msg.Event)
		}
	})
}

func (c *call) onInvite(content *CallInviteEventContent) error {
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
		trackLocal, err := webrtc.NewTrackLocalStaticRTP(trackRemote.Codec().RTPCodecCapability, id, fmt.Sprintf("%s-%s-%s", c.callID, c.deviceID, trackRemote.id))
		if err != nil {
			panic(err)
		}

		c.conf.trackDetails[trackRemote.id] = trackDetail{
			call:  c,
			track: trackLocal,
		}
		c.conf.trackDetailsMu.Unlock()

		copyRemoteToLocal(trackRemote, trackLocal)
	})

	peerConnection.OnDataChannel(func(d *webrtc.DataChannel) {
		log.Print("onDataChannel", d)
		//f.dataChannelHandler(peerConnection, d, setPublishDetails)
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

	// TODO: send any subsequent candidates we discover to the peer

	answerSdp := peerConnection.LocalDescription().SDP

	// TODO: sessions
	answerEvtContent := &event.Content{
		Parsed: event.CallAnswerEventContent{
			CallID:  c.callID,
			ConfID:  c.conf.confID,
			PartyID: c.client.DeviceID,
			Version: 1,
			Answer:  answerSdp,
		},
	}

	toDeviceAnswer := &mautrix.ReqSendToDevice{
		Messages: map[c.UserID]map[c.DeviceID]*event.Content{
			toUser: {
				toDevice: answerEvtContent,
			},
		},
	}

	// TODO: E2EE
	// TODO: to-device reliability
	c.client.SendToDevice(event.CallAnswer, toDeviceAnswer)

	return err
}

func (c *call) onCandidates(content *CallCandidatesEventContent) error {
	// TODO: tell our peerConnection about the new candidates we just discovered
	log.Print("ignoring candidates as not yet implemented", content)
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
