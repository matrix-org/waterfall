/*
Copyright 2022 The Matrix.org Foundation C.I.C.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"encoding/json"
	"log"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/pion/webrtc/v3"
)

type Call struct {
	CallID                 string
	UserID                 id.UserID
	DeviceID               id.DeviceID
	LocalSessionID         id.SessionID
	RemoteSessionID        id.SessionID
	Client                 *mautrix.Client
	PeerConnection         *webrtc.PeerConnection
	Conf                   *Conference
	dataChannel            *webrtc.DataChannel
	lastKeepAliveTimestamp time.Time
	sentEndOfCandidates    bool
}

func (c *Call) onDCSelect(start []event.SFUTrackDescription) {
	if len(start) == 0 {
		return
	}

	for _, trackDesc := range start {
		log.Printf("%s | selecting StreamID %s TrackID %s", c.UserID, trackDesc.StreamID, trackDesc.TrackID)
		foundTracks := c.Conf.GetLocalTrackByInfo(LocalTrackInfo{
			StreamID: trackDesc.StreamID,
			TrackID:  trackDesc.TrackID,
		})
		if len(foundTracks) == 0 {
			log.Printf("%s | no track found StreamID %s TrackID %s", c.UserID, trackDesc.StreamID, trackDesc.TrackID)
			continue
		}
		for _, track := range foundTracks {
			if _, err := c.PeerConnection.AddTrack(track); err == nil {
				log.Printf("%s | added %s StreamID %s TrackID %s", c.UserID, track.Kind(), track.StreamID(), track.ID())
			} else {
				log.Printf("%s | failed to add %s StreamID %s TrackID %s", c.UserID, track.Kind(), track.StreamID(), track.ID())
			}
		}
	}
}

func (c *Call) onDCPublish(sdp string) {
	log.Printf("%s | received DC publish", c.UserID)

	err := c.PeerConnection.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  sdp,
	})
	if err != nil {
		log.Printf("%s | failed to set remote description %+v - ignoring: %s", c.UserID, sdp, err)
		return
	}

	offer, err := c.PeerConnection.CreateAnswer(nil)
	if err != nil {
		log.Printf("%s | failed to create answer - ignoring: %s", c.UserID, err)
		return
	}
	err = c.PeerConnection.SetLocalDescription(offer)
	if err != nil {
		log.Printf("%s | failed to set local description %+v - ignoring: %s", c.UserID, offer.SDP, err)
		return
	}

	c.SendDataChannelMessage(event.SFUMessage{
		Op:  event.SFUOperationAnswer,
		SDP: offer.SDP,
	})
}

func (c *Call) onDCUnpublish(stop []event.SFUTrackDescription, sdp string) {
	for _, trackDesc := range stop {
		log.Printf("%s | unpublishing StreamID %s TrackID %s", c.UserID, trackDesc.StreamID, trackDesc.TrackID)
		if removedTracksCount := c.Conf.RemoveTracksFromPeerConnectionsByInfo(LocalTrackInfo{
			StreamID: trackDesc.StreamID,
			TrackID:  trackDesc.TrackID,
		}); removedTracksCount == 0 {
			log.Printf("%s | no tracks to remove for: %+v", c.UserID, stop)
		}

	}

	err := c.PeerConnection.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  sdp,
	})
	if err != nil {
		log.Printf("%s | failed to set remote description %+v - ignoring: %s", c.UserID, sdp, err)
		return
	}

	offer, err := c.PeerConnection.CreateAnswer(nil)
	if err != nil {
		log.Printf("%s | failed to create answer - ignoring: %s", c.UserID, err)
		return
	}
	err = c.PeerConnection.SetLocalDescription(offer)
	if err != nil {
		log.Printf("%s | failed to set local description %+v - ignoring: %s", c.UserID, offer.SDP, err)
		return
	}

	c.SendDataChannelMessage(event.SFUMessage{
		Op:  event.SFUOperationAnswer,
		SDP: offer.SDP,
	})
}

func (c *Call) onDCAnswer(sdp string) {
	log.Printf("%s | received DC answer", c.UserID)

	err := c.PeerConnection.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  sdp,
	})
	if err != nil {
		log.Printf("%s | failed to set remote description %+v - ignoring: %s", c.UserID, sdp, err)
		return
	}
}

func (c *Call) onDCAlive() {
	c.lastKeepAliveTimestamp = time.Now()

}

func (c *Call) onDCMetadata(metadata event.CallSDPStreamMetadata) {
	log.Printf("%s | received DC metadata", c.UserID)

	c.Conf.SendUpdatedMetadataFromCall(c.CallID)
}

func (c *Call) dataChannelHandler(d *webrtc.DataChannel) {
	c.dataChannel = d

	d.OnOpen(func() {
		c.SendDataChannelMessage(event.SFUMessage{Op: event.SFUOperationMetadata})
	})

	d.OnError(func(err error) {
		log.Fatalf("%s | DC error: %s", c.CallID, err)
	})

	d.OnMessage(func(m webrtc.DataChannelMessage) {
		if !m.IsString {
			log.Printf("%s | inbound message is not string - ignoring: %+v", c.UserID, m)
			return
		}

		msg := &event.SFUMessage{}
		if err := json.Unmarshal(m.Data, msg); err != nil {
			log.Printf("%s | failed to unmarshal %+v - ignoring: %s", c.CallID, msg, err)
			return
		}

		if msg.Metadata != nil {
			c.Conf.UpdateSDPStreamMetadata(c.DeviceID, msg.Metadata)
		}

		switch msg.Op {
		case event.SFUOperationSelect:
			c.onDCSelect(msg.Start)
		case event.SFUOperationPublish:
			c.onDCPublish(msg.SDP)
		case event.SFUOperationUnpublish:
			c.onDCUnpublish(msg.Stop, msg.SDP)
		case event.SFUOperationAnswer:
			c.onDCAnswer(msg.SDP)
		case event.SFUOperationAlive:
			c.onDCAlive()
		case event.SFUOperationMetadata:
			c.onDCMetadata(msg.Metadata)

		default:
			log.Printf("Unknown operation - ignoring: %s", msg.Op)
			// TODO: hook up msg.Stop to unsubscribe from tracks
			// TODO: hook cascade back up.
			// As we're not an AS, we'd rely on the client
			// to send us a "connect" op to tell us how to
			// connect to another focus in order to select
			// its streams.
		}
	})
}

func (c *Call) negotiationNeededHandler() {
	offer, err := c.PeerConnection.CreateOffer(nil)
	if err != nil {
		log.Printf("%s | failed to create offer - ignoring: %s", c.UserID, err)
		return
	}
	err = c.PeerConnection.SetLocalDescription(offer)
	if err != nil {
		log.Printf("%s | failed to set local description %+v - ignoring: %s", c.UserID, offer.SDP, err)
		return
	}

	c.SendDataChannelMessage(event.SFUMessage{
		Op:  event.SFUOperationOffer,
		SDP: offer.SDP,
	})
}

func (c *Call) iceCandidateHandler(candidate *webrtc.ICECandidate) {
	if candidate == nil {
		return
	}

	jsonCandidate := candidate.ToJSON()

	candidateEvtContent := &event.Content{
		Parsed: event.CallCandidatesEventContent{
			BaseCallEventContent: event.BaseCallEventContent{
				CallID:          c.CallID,
				ConfID:          c.Conf.ConfID,
				DeviceID:        c.Client.DeviceID,
				SenderSessionID: c.LocalSessionID,
				DestSessionID:   c.RemoteSessionID,
				PartyID:         string(c.Client.DeviceID),
				Version:         event.CallVersion("1"),
			},
			Candidates: []event.CallCandidate{{
				Candidate:     jsonCandidate.Candidate,
				SDPMLineIndex: int(*jsonCandidate.SDPMLineIndex),
				SDPMID:        *jsonCandidate.SDPMid,
			}},
		},
	}
	c.sendToDevice(event.CallCandidates, candidateEvtContent)
}

func (c *Call) trackHandler(trackRemote *webrtc.TrackRemote, rec *webrtc.RTPReceiver) {
	go WriteRTCP(trackRemote, c.PeerConnection)

	trackLocal, err := webrtc.NewTrackLocalStaticRTP(trackRemote.Codec().RTPCodecCapability, trackRemote.ID(), trackRemote.StreamID())
	if err != nil {
		log.Printf("%s | failed to create new track local static RTP %+v - ignoring: %s", c.UserID, trackRemote.Codec().RTPCodecCapability, err)
		return
	}

	c.Conf.Tracks.Mutex.Lock()
	c.Conf.Tracks.Tracks = append(c.Conf.Tracks.Tracks, LocalTrackWithInfo{
		Track: trackLocal,
		Info: LocalTrackInfo{
			TrackID:  trackLocal.ID(),
			StreamID: trackLocal.StreamID(),
			Call:     c,
		},
	})
	c.Conf.Tracks.Mutex.Unlock()

	log.Printf("%s | published %s StreamID %s TrackID %s", c.UserID, trackLocal.Kind(), trackLocal.StreamID(), trackLocal.ID())

	go c.Conf.SendUpdatedMetadataFromCall(c.CallID)
	go CopyRemoteToLocal(trackRemote, trackLocal)
}

func (c *Call) iceConnectionStateHandler(state webrtc.ICEConnectionState) {
	if state == webrtc.ICEConnectionStateCompleted || state == webrtc.ICEConnectionStateConnected {
		c.lastKeepAliveTimestamp = time.Now()
		go c.CheckKeepAliveTimestamp()

		if !c.sentEndOfCandidates {
			candidateEvtContent := &event.Content{
				Parsed: event.CallCandidatesEventContent{
					BaseCallEventContent: event.BaseCallEventContent{
						CallID:          c.CallID,
						ConfID:          c.Conf.ConfID,
						DeviceID:        c.Client.DeviceID,
						SenderSessionID: c.LocalSessionID,
						DestSessionID:   c.RemoteSessionID,
						PartyID:         string(c.Client.DeviceID),
						Version:         event.CallVersion("1"),
					},
					Candidates: []event.CallCandidate{{Candidate: ""}},
				},
			}
			c.sendToDevice(event.CallCandidates, candidateEvtContent)
			c.sentEndOfCandidates = true
		}
	}
}

func (c *Call) OnInvite(content *event.CallInviteEventContent) {
	c.Conf.UpdateSDPStreamMetadata(c.DeviceID, content.SDPStreamMetadata)
	offer := content.Offer

	peerConnection, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		log.Panicf("%s | failed to create new peer connection: %s", c.UserID, err)
	}
	c.PeerConnection = peerConnection

	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		c.trackHandler(track, receiver)
	})
	peerConnection.OnDataChannel(func(d *webrtc.DataChannel) {
		c.dataChannelHandler(d)
	})
	peerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		c.iceCandidateHandler(candidate)
	})
	peerConnection.OnNegotiationNeeded(func() {
		c.negotiationNeededHandler()
	})
	peerConnection.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		c.iceConnectionStateHandler(state)
	})

	err = peerConnection.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offer.SDP,
	})
	if err != nil {
		log.Printf("%s | failed to set remote description %+v - ignoring: %s", c.UserID, offer.SDP, err)
		return
	}

	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		log.Printf("%s | failed to create answer - ignoring: %s", c.UserID, err)
		return
	}

	// TODO: trickle ICE for fast conn setup, rather than block here
	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)
	if err = peerConnection.SetLocalDescription(answer); err != nil {
		log.Printf("%s | failed to set local description %+v - ignoring: %s", c.UserID, offer.SDP, err)
		return
	}
	<-gatherComplete

	answerEvtContent := &event.Content{
		Parsed: event.CallAnswerEventContent{
			BaseCallEventContent: event.BaseCallEventContent{
				CallID:          c.CallID,
				ConfID:          c.Conf.ConfID,
				DeviceID:        c.Client.DeviceID,
				SenderSessionID: c.LocalSessionID,
				DestSessionID:   c.RemoteSessionID,
				PartyID:         string(c.Client.DeviceID),
				Version:         event.CallVersion("1"),
			},
			Answer: event.CallData{
				Type: "answer",
				SDP:  peerConnection.LocalDescription().SDP,
			},
			SDPStreamMetadata: c.Conf.GetRemoteMetadataForDevice(c.DeviceID),
		},
	}
	c.sendToDevice(event.CallAnswer, answerEvtContent)
}

func (c *Call) OnSelectAnswer(content *event.CallSelectAnswerEventContent) {
	selectedPartyID := content.SelectedPartyID
	if selectedPartyID != string(c.Client.DeviceID) {
		c.Terminate()
		log.Printf("%s | call was answered on a different device: %s", c.UserID, selectedPartyID)
	}
}

func (c *Call) OnHangup(content *event.CallHangupEventContent) {
	c.Terminate()
}

func (c *Call) OnCandidates(content *event.CallCandidatesEventContent) {
	for _, candidate := range content.Candidates {
		sdpMLineIndex := uint16(candidate.SDPMLineIndex)
		ice := webrtc.ICECandidateInit{
			Candidate:        candidate.Candidate,
			SDPMid:           &candidate.SDPMID,
			SDPMLineIndex:    &sdpMLineIndex,
			UsernameFragment: new(string),
		}
		if err := c.PeerConnection.AddICECandidate(ice); err != nil {
			log.Printf("%s | failed to add ICE candidate %+v: %s", c.UserID, content, err)
		}
	}
}

func (c *Call) Terminate() {
	log.Printf("%s | terminating call", c.UserID)

	if err := c.PeerConnection.Close(); err != nil {
		log.Printf("%s | error closing peer connection: %s", c.UserID, err)
	}

	c.Conf.Calls.CallsMu.Lock()
	delete(c.Conf.Calls.Calls, c.CallID)
	c.Conf.Calls.CallsMu.Unlock()

	info := LocalTrackInfo{Call: c}
	c.Conf.RemoveTracksFromPeerConnectionsByInfo(info)
	c.Conf.RemoveTracksFromConfByInfo(info)
	c.Conf.RemoveMetadataByDeviceID(c.DeviceID)
	c.Conf.SendUpdatedMetadataFromCall(c.CallID)
}

func (c *Call) Hangup(reason event.CallHangupReason) {
	hangupEvtContent := &event.Content{
		Parsed: event.CallHangupEventContent{
			BaseCallEventContent: event.BaseCallEventContent{
				CallID:          c.CallID,
				ConfID:          c.Conf.ConfID,
				DeviceID:        c.Client.DeviceID,
				SenderSessionID: c.LocalSessionID,
				DestSessionID:   c.RemoteSessionID,
				PartyID:         string(c.Client.DeviceID),
				Version:         event.CallVersion("1"),
			},
			Reason: reason,
		},
	}
	c.sendToDevice(event.CallHangup, hangupEvtContent)
	c.Terminate()
}

func (c *Call) sendToDevice(callType event.Type, content *event.Content) {
	if callType.Type != event.CallCandidates.Type {
		log.Printf("%s | sending to device %s", c.UserID, callType.Type)
	}
	toDevice := &mautrix.ReqSendToDevice{
		Messages: map[id.UserID]map[id.DeviceID]*event.Content{
			c.UserID: {
				c.DeviceID: content,
			},
		},
	}

	// TODO: E2EE
	// TODO: to-device reliability
	c.Client.SendToDevice(callType, toDevice)
}

func (c *Call) SendDataChannelMessage(msg event.SFUMessage) {
	if c.dataChannel == nil {
		return
	}

	msg.Metadata = c.Conf.GetRemoteMetadataForDevice(c.DeviceID)
	if msg.Op == "metadata" && len(msg.Metadata) == 0 {
		return
	}

	marshaled, err := json.Marshal(msg)
	if err != nil {
		log.Printf("%s | failed to marshal %+v - ignoring: %s", c.UserID, msg, err)
		return
	}

	err = c.dataChannel.SendText(string(marshaled))
	if err != nil {
		log.Printf("%s | failed to send %s over DC: %s", c.UserID, msg.Op, err)
	}

	log.Printf("%s | sent DC %s", c.UserID, msg.Op)
}

func (c *Call) CheckKeepAliveTimestamp() {
	timeout := time.Second * time.Duration(config.Timeout)
	for range time.Tick(timeout) {
		if c.lastKeepAliveTimestamp.Add(timeout).Before(time.Now()) {
			if c.PeerConnection.ConnectionState() != webrtc.PeerConnectionStateClosed {
				log.Printf("%s | did not get keep-alive message in the last %s:", c.UserID, timeout)
				c.Hangup(event.CallHangupKeepAliveTimeout)
			}
			break
		}
	}
}
