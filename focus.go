package main

import (
	"encoding/json"
	"errors"
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
	dataChannel     *webrtc.DataChannel
	// we track the call's tracks via the conf object.
}

type localTrackInfo struct {
	streamID string
	trackID  string
	call     *call
}

type localTrackWithInfo struct {
	track *webrtc.TrackLocalStaticRTP
	info  localTrackInfo
}

type calls struct {
	callsMu sync.RWMutex
	calls   map[string]*call // By callID
}

type conf struct {
	confID   string
	calls    calls
	tracksMu sync.RWMutex
	tracks   []localTrackWithInfo
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
			co.tracks = []localTrackWithInfo{}
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

func (c *conf) getLocalTrackIndicesByInfo(selectInfo localTrackInfo) (tracks []int, err error) {
	foundIndices := []int{}
	for index, track := range c.tracks {
		info := track.info
		if selectInfo.call != nil && selectInfo.call != info.call {
			continue
		}
		if selectInfo.streamID != "" && selectInfo.streamID != info.streamID {
			continue
		}
		if selectInfo.trackID != "" && selectInfo.trackID != info.trackID {
			continue
		}
		foundIndices = append(foundIndices, index)
	}

	if len(foundIndices) == 0 {
		log.Printf("Found no tracks for %+v", selectInfo)
		return nil, errors.New("no such tracks")
	} else {
		return foundIndices, nil
	}
}

func (c *conf) getLocalTrackByInfo(selectInfo localTrackInfo) (tracks []webrtc.TrackLocal, err error) {
	c.tracksMu.Lock()
	defer c.tracksMu.Unlock()

	indices, err := c.getLocalTrackIndicesByInfo(selectInfo)
	if err != nil {
		return nil, err
	}

	foundTracks := []webrtc.TrackLocal{}
	for _, index := range indices {
		foundTracks = append(foundTracks, c.tracks[index].track)
	}

	if len(foundTracks) == 0 {
		log.Printf("No tracks")
		return nil, errors.New("no such tracks")
	} else {
		return foundTracks, nil
	}
}

func (c *conf) removeTracksFromPeerConnectionsByInfo(removeInfo localTrackInfo) error {
	c.tracksMu.Lock()
	defer c.tracksMu.Unlock()

	indices, err := c.getLocalTrackIndicesByInfo(removeInfo)
	if err != nil {
		return err
	}

	// FIXME: the big O of this must be awful...
	for _, index := range indices {
		info := c.tracks[index].info

		for _, call := range c.calls.calls {
			for _, sender := range call.peerConnection.GetSenders() {
				if info.trackID == sender.Track().ID() {
					log.Printf("%s | removing %s track with StreamID %s", call.callID, sender.Track().Kind(), info.streamID)
					if err := sender.Stop(); err != nil {
						log.Printf("%s | failed to stop sender: %s", call.callID, err)
					}
					if err := call.peerConnection.RemoveTrack(sender); err != nil {
						log.Printf("%s | failed to remove track: %s", call.callID, err)
						return err
					}
				}
			}
		}
	}

	return nil
}

func (c *call) dataChannelHandler(d *webrtc.DataChannel) {
	c.dataChannel = d
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
		log.Printf("%s | DC opened", c.callID)
	})

	d.OnClose(func() {
		log.Printf("%s | DC closed", c.callID)
	})

	d.OnError(func(err error) {
		log.Fatalf("%s | DC error: %s", c.callID, err)
	})

	d.OnMessage(func(m webrtc.DataChannelMessage) {
		if !m.IsString {
			log.Fatal("Inbound message is not string")
		}

		msg := &dataChannelMessage{}
		if err := json.Unmarshal(m.Data, msg); err != nil {
			log.Fatalf("%s | failed to unmarshal: %s", c.callID, err)
		}

		log.Printf("%s | received DC %s confId=%s start=%+v", c.callID, msg.Op, msg.ConfID, msg.Start)

		// TODO: hook cascade back up.
		// As we're not an AS, we'd rely on the client
		// to send us a "connect" op to tell us how to
		// connect to another focus in order to select
		// its streams.

		switch msg.Op {
		case "select":
			var tracks []webrtc.TrackLocal
			for _, trackDesc := range msg.Start {
				foundTracks, err := c.conf.getLocalTrackByInfo(localTrackInfo{streamID: trackDesc.StreamID})
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

func (c *call) negotiationNeeded() {
	log.Printf("%s | negotiation needed", c.callID)

	offer, err := c.peerConnection.CreateOffer(nil)
	if err != nil {
		panic(err)
	}
	err = c.peerConnection.SetLocalDescription(offer)
	if err != nil {
		panic(err)
	}

	response := dataChannelMessage{
		Op:  "offer",
		SDP: offer.SDP,
	}
	marshaled, err := json.Marshal(response)
	if err != nil {
		panic(err)
	}
	err = c.dataChannel.SendText(string(marshaled))
	if err != nil {
		log.Printf("%s | failed to send over DC: %s", c.callID, err)
	}

	log.Printf("%s | sent DC %s", c.callID, response.Op)
}

func (c *call) iceCandidateHandler(candidate *webrtc.ICECandidate) {
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
					if err := peerConnection.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(trackRemote.SSRC())}}); err != nil {
						log.Printf("%s | failed to write RTCP on trackID %s: %s", c.callID, trackRemote.ID(), err)
						break
					}
				}
			}()
		}

		c.conf.tracksMu.Lock()
		trackLocal, err := webrtc.NewTrackLocalStaticRTP(trackRemote.Codec().RTPCodecCapability, trackRemote.ID(), trackRemote.StreamID())
		if err != nil {
			panic(err)
		}

		c.conf.tracks = append(c.conf.tracks, localTrackWithInfo{
			track: trackLocal,
			info: localTrackInfo{
				trackID:  trackLocal.ID(),
				streamID: trackLocal.StreamID(),
				call:     c,
			},
		})

		log.Printf("%s | published track with streamID %s and kind %s", c.callID, trackLocal.StreamID(), trackLocal.Kind())
		c.conf.tracksMu.Unlock()

		copyRemoteToLocal(trackRemote, trackLocal)
	})

	peerConnection.OnDataChannel(func(d *webrtc.DataChannel) {
		c.dataChannelHandler(d)
	})
	peerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		c.iceCandidateHandler(candidate)
	})
	peerConnection.OnNegotiationNeeded(func() {
		c.negotiationNeeded()
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

	return err
}

func (c *call) onSelectAnswer(content *event.CallSelectAnswerEventContent) {
	selectedPartyId := content.SelectedPartyID
	if selectedPartyId != string(c.client.DeviceID) {
		c.terminate()
		log.Printf("%s | Call was answered on a different device: %s", content.CallID, selectedPartyId)
	}
}

func (c *call) onHangup(content *event.CallHangupEventContent) {
	c.terminate()
}

func (c *call) terminate() error {
	log.Printf("%s | Terminating call", c.callID)

	if err := c.peerConnection.Close(); err != nil {
		log.Printf("%s | error closing peer connection: %s", c.callID, err)
	}

	c.conf.calls.callsMu.Lock()
	delete(c.conf.calls.calls, c.callID)
	c.conf.calls.callsMu.Unlock()

	if err := c.conf.removeTracksFromPeerConnectionsByInfo(localTrackInfo{call: c}); err != nil {
		return err
	}

	// TODO: Remove the tracks from conf.tracks

	return nil
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
		if err != nil || buff == nil {
			log.Printf("ending read on track with StreamID %s: %s", trackRemote.StreamID(), err)
			break
		}

		if _, err = trackLocal.Write(buff[:i]); err != nil {
			log.Printf("ending write on track with StreamID %s: %s", trackLocal.StreamID(), err)
			break
		}
	}

}
