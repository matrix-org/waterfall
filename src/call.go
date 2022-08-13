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
	"strings"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/pion/rtcp"
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
	DataChannel            *webrtc.DataChannel
	LastKeepAliveTimestamp time.Time
}

func (c *Call) DataChannelHandler(d *webrtc.DataChannel) {
	c.DataChannel = d
	peerConnection := c.PeerConnection

	d.OnOpen(func() {
		c.SendDataChannelMessage(dataChannelMessage{Op: "metadata"})
	})

	d.OnError(func(err error) {
		log.Fatalf("%s | DC error: %s", c.CallID, err)
	})

	d.OnMessage(func(m webrtc.DataChannelMessage) {
		if !m.IsString {
			log.Fatal("Inbound message is not string")
		}

		msg := &dataChannelMessage{}
		if err := json.Unmarshal(m.Data, msg); err != nil {
			log.Fatalf("%s | failed to unmarshal: %s", c.CallID, err)
		}

		// TODO: hook cascade back up.
		// As we're not an AS, we'd rely on the client
		// to send us a "connect" op to tell us how to
		// connect to another focus in order to select
		// its streams.

		if msg.Metadata != nil {
			c.Conf.UpdateSDPStreamMetadata(c.DeviceID, msg.Metadata)
		}

		switch msg.Op {
		case "select":
			if len(msg.Start) == 0 {
				return
			}

			for _, trackDesc := range msg.Start {
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
					log.Printf("%s | adding %s StreamID %s TrackID %s", c.UserID, track.Kind(), track.StreamID(), track.ID())
					if _, err := c.PeerConnection.AddTrack(track); err != nil {
						panic(err)
					}
				}
			}

		case "publish":
			log.Printf("%s | received DC publish", c.UserID)

			peerConnection.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeOffer,
				SDP:  msg.SDP,
			})

			offer, err := c.PeerConnection.CreateAnswer(nil)
			if err != nil {
				panic(err)
			}
			err = c.PeerConnection.SetLocalDescription(offer)
			if err != nil {
				panic(err)
			}

			c.SendDataChannelMessage(dataChannelMessage{
				Op:  "answer",
				SDP: offer.SDP,
			})

		case "unpublish":
			for _, trackDesc := range msg.Stop {
				log.Printf("%s | unpublishing StreamID %s TrackID %s", c.UserID, trackDesc.StreamID, trackDesc.TrackID)
				if removedTracksCount := c.Conf.RemoveTracksFromPeerConnectionsByInfo(LocalTrackInfo{
					StreamID: trackDesc.StreamID,
					TrackID:  trackDesc.TrackID,
				}); removedTracksCount == 0 {
					log.Printf("%s | no tracks to remove for: %+v", c.UserID, msg.Stop)
				}

			}

			peerConnection.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeOffer,
				SDP:  msg.SDP,
			})

			offer, err := c.PeerConnection.CreateAnswer(nil)
			if err != nil {
				panic(err)
			}
			err = c.PeerConnection.SetLocalDescription(offer)
			if err != nil {
				panic(err)
			}

			c.SendDataChannelMessage(dataChannelMessage{
				Op:  "answer",
				SDP: offer.SDP,
			})

		case "answer":
			log.Printf("%s | received DC answer", c.UserID)

			peerConnection.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeAnswer,
				SDP:  msg.SDP,
			})

		case "alive":
			c.LastKeepAliveTimestamp = time.Now()

		case "metadata":
			log.Printf("%s | received DC metadata", c.UserID)

			c.Conf.SendUpdatedMetadataFromCall(c.CallID)

		default:
			log.Fatalf("Unknown operation %s", msg.Op)
			// TODO: hook up msg.Stop to unsubscribe from tracks
		}
	})
}

func (c *Call) NegotiationNeededHandler() {
	offer, err := c.PeerConnection.CreateOffer(nil)
	if err != nil {
		panic(err)
	}
	err = c.PeerConnection.SetLocalDescription(offer)
	if err != nil {
		panic(err)
	}

	c.SendDataChannelMessage(dataChannelMessage{
		Op:  "offer",
		SDP: offer.SDP,
	})
}

func (c *Call) IceCandidateHandler(candidate *webrtc.ICECandidate) {
	if candidate == nil {
		return
	}

	ice := candidate.ToJSON()

	// TODO: batch these up a bit
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
	c.SendToDevice(event.CallCandidates, candidateEvtContent)
}

func (c *Call) TrackHandler(trackRemote *webrtc.TrackRemote, rec *webrtc.RTPReceiver) {
	// FIXME: This is a potential performance killer
	if strings.Contains(trackRemote.Codec().MimeType, "video") {
		// Send a PLI on an interval so that the publisher is pushing a keyframe every rtcpPLIInterval
		go func() {
			ticker := time.NewTicker(time.Millisecond * 200)
			for range ticker.C {
				if err := c.PeerConnection.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(trackRemote.SSRC())}}); err != nil {
					log.Printf("%s | failed to write RTCP on trackID %s: %s", c.UserID, trackRemote.ID(), err)
					break
				}
			}
		}()
	}

	trackLocal, err := webrtc.NewTrackLocalStaticRTP(trackRemote.Codec().RTPCodecCapability, trackRemote.ID(), trackRemote.StreamID())
	if err != nil {
		panic(err)
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

func (c *Call) IceConnectionStateHandler(state webrtc.ICEConnectionState) {
	if state == webrtc.ICEConnectionStateCompleted || state == webrtc.ICEConnectionStateConnected {
		c.LastKeepAliveTimestamp = time.Now()
		go c.CheckKeepAliveTimestamp()
	}
}

func (c *Call) OnInvite(content *event.CallInviteEventContent) error {
	c.Conf.UpdateSDPStreamMetadata(c.DeviceID, content.SDPStreamMetadata)
	offer := content.Offer

	peerConnection, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return err
	}
	c.PeerConnection = peerConnection

	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		c.TrackHandler(track, receiver)
	})
	peerConnection.OnDataChannel(func(d *webrtc.DataChannel) {
		c.DataChannelHandler(d)
	})
	peerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		c.IceCandidateHandler(candidate)
	})
	peerConnection.OnNegotiationNeeded(func() {
		c.NegotiationNeededHandler()
	})
	peerConnection.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		c.IceConnectionStateHandler(state)
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
				SDP:  answerSdp,
			},
			SDPStreamMetadata: c.Conf.GetRemoteMetadataForDevice(c.DeviceID),
		},
	}
	c.SendToDevice(event.CallAnswer, answerEvtContent)

	return err
}

func (c *Call) OnSelectAnswer(content *event.CallSelectAnswerEventContent) {
	selectedPartyID := content.SelectedPartyID
	if selectedPartyID != string(c.Client.DeviceID) {
		c.Terminate()
		log.Printf("%s | Call was answered on a different device: %s", c.UserID, selectedPartyID)
	}
}

func (c *Call) OnHangup(content *event.CallHangupEventContent) {
	c.Terminate()
}

func (c *Call) OnCandidates(content *event.CallCandidatesEventContent) error {
	for _, candidate := range content.Candidates {
		sdpMLineIndex := uint16(candidate.SDPMLineIndex)
		ice := webrtc.ICECandidateInit{
			Candidate:        candidate.Candidate,
			SDPMid:           &candidate.SDPMID,
			SDPMLineIndex:    &sdpMLineIndex,
			UsernameFragment: new(string),
		}
		if err := c.PeerConnection.AddICECandidate(ice); err != nil {
			log.Print("Failed to add ICE candidate", content)
			return err
		}
	}
	return nil
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
	c.SendToDevice(event.CallHangup, hangupEvtContent)
	c.Terminate()
}

func (c *Call) SendToDevice(callType event.Type, content *event.Content) {
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

func (c *Call) SendDataChannelMessage(msg dataChannelMessage) {
	if c.DataChannel == nil {
		return
	}

	msg.ConfID = c.Conf.ConfID
	msg.Metadata = c.Conf.GetRemoteMetadataForDevice(c.DeviceID)
	// TODO: Set ID

	if msg.Op == "metadata" && len(msg.Metadata) == 0 {
		return
	}

	marshaled, err := json.Marshal(msg)
	if err != nil {
		panic(err)
	}

	err = c.DataChannel.SendText(string(marshaled))
	if err != nil {
		log.Printf("%s | failed to send %s over DC: %s", c.UserID, msg.Op, err)
	}

	log.Printf("%s | sent DC %s", c.UserID, msg.Op)
}

func (c *Call) CheckKeepAliveTimestamp() {
	timeout := time.Second * time.Duration(configInstance.Timeout)
	for range time.Tick(timeout) {
		if c.LastKeepAliveTimestamp.Add(timeout).Before(time.Now()) {
			log.Printf("%s | did not get keep-alive message in the last %s:", c.UserID, timeout)
			c.Hangup(event.CallHangupKeepAliveTimeout)
			break
		}
	}
}
