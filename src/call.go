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
	"sync"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
)

type subscribedTracks struct {
	mutex  sync.RWMutex
	tracks []localTrackInfo
}

type call struct {
	callID           string
	userID           id.UserID
	deviceID         id.DeviceID
	localSessionID   id.SessionID
	remoteSessionID  id.SessionID
	client           *mautrix.Client
	peerConnection   *webrtc.PeerConnection
	conf             *conf
	dataChannel      *webrtc.DataChannel
	subscribedTracks subscribedTracks
}

func (c *call) dataChannelHandler(d *webrtc.DataChannel) {
	c.dataChannel = d
	peerConnection := c.peerConnection

	d.OnOpen(func() {
		log.Printf("%s | DC opened", c.userID)
	})

	d.OnClose(func() {
		log.Printf("%s | DC closed", c.userID)
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

		log.Printf("%s | received DC: %s", c.userID, msg.Op)

		// TODO: hook cascade back up.
		// As we're not an AS, we'd rely on the client
		// to send us a "connect" op to tell us how to
		// connect to another focus in order to select
		// its streams.

		switch msg.Op {
		case "select":
			log.Printf("%s | selected: %+v", c.userID, msg.Start)

			c.subscribedTracks.mutex.Lock()
			for _, trackDesc := range msg.Start {
				c.subscribedTracks.tracks = append(c.subscribedTracks.tracks, localTrackInfo{
					streamID: trackDesc.StreamID,
					trackID:  trackDesc.TrackID,
				})
			}
			c.subscribedTracks.mutex.Unlock()

			go c.addSubscribedTracksToPeerConnection()

		case "publish":
			peerConnection.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeOffer,
				SDP:  msg.SDP,
			})

			offer, err := c.peerConnection.CreateAnswer(nil)
			if err != nil {
				panic(err)
			}
			err = c.peerConnection.SetLocalDescription(offer)
			if err != nil {
				panic(err)
			}

			c.sendDataChannelMessage(dataChannelMessage{
				Op:  "answer",
				SDP: offer.SDP,
			})

		case "unpublish":
			log.Printf("%s | unpublished: %+v", c.userID, msg.Stop)

			for _, trackDesc := range msg.Stop {
				if removedTracksCount := c.conf.removeTracksFromPeerConnectionsByInfo(localTrackInfo{
					streamID: trackDesc.StreamID,
					trackID:  trackDesc.TrackID,
				}); removedTracksCount == 0 {
					log.Printf("%s | no tracks to remove for: %+v", c.userID, msg.Stop)
				}

			}

			peerConnection.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeOffer,
				SDP:  msg.SDP,
			})

			offer, err := c.peerConnection.CreateAnswer(nil)
			if err != nil {
				panic(err)
			}
			err = c.peerConnection.SetLocalDescription(offer)
			if err != nil {
				panic(err)
			}

			c.sendDataChannelMessage(dataChannelMessage{
				Op:  "answer",
				SDP: offer.SDP,
			})

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

func (c *call) negotiationNeededHandler() {
	log.Printf("%s | negotiation needed", c.userID)

	offer, err := c.peerConnection.CreateOffer(nil)
	if err != nil {
		panic(err)
	}
	err = c.peerConnection.SetLocalDescription(offer)
	if err != nil {
		panic(err)
	}

	c.sendDataChannelMessage(dataChannelMessage{
		Op:  "offer",
		SDP: offer.SDP,
	})
}

func (c *call) iceCandidateHandler(candidate *webrtc.ICECandidate) {
	if candidate == nil {
		return
	}

	ice := candidate.ToJSON()

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

func (c *call) trackHandler(trackRemote *webrtc.TrackRemote, rec *webrtc.RTPReceiver) {
	// FIXME: This is a potential performance killer
	if strings.Contains(trackRemote.Codec().MimeType, "video") {
		// Send a PLI on an interval so that the publisher is pushing a keyframe every rtcpPLIInterval
		go func() {
			ticker := time.NewTicker(time.Millisecond * 200)
			for range ticker.C {
				if err := c.peerConnection.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(trackRemote.SSRC())}}); err != nil {
					log.Printf("%s | failed to write RTCP on trackID %s: %s", c.userID, trackRemote.ID(), err)
					break
				}
			}
		}()
	}

	trackLocal, err := webrtc.NewTrackLocalStaticRTP(trackRemote.Codec().RTPCodecCapability, trackRemote.ID(), trackRemote.StreamID())
	if err != nil {
		panic(err)
	}

	c.conf.tracks.mutex.Lock()
	c.conf.tracks.tracks = append(c.conf.tracks.tracks, localTrackWithInfo{
		track: trackLocal,
		info: localTrackInfo{
			trackID:  trackLocal.ID(),
			streamID: trackLocal.StreamID(),
			call:     c,
		},
	})
	c.conf.tracks.mutex.Unlock()

	log.Printf("%s | published track with streamID %s trackID %s and kind %s", c.userID, trackLocal.StreamID(), trackLocal.ID(), trackLocal.Kind())

	for _, call := range c.conf.calls.calls {
		if call.callID != c.callID {
			go call.addSubscribedTracksToPeerConnection()
		}
	}

	go copyRemoteToLocal(trackRemote, trackLocal)
}

func (c *call) onInvite(content *event.CallInviteEventContent) error {
	offer := content.Offer

	peerConnection, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return err
	}
	c.peerConnection = peerConnection

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
		log.Printf("%s | Call was answered on a different device: %s", c.userID, selectedPartyId)
	}
}

func (c *call) onHangup(content *event.CallHangupEventContent) {
	c.terminate()
}

func (c *call) onCandidates(content *event.CallCandidatesEventContent) error {
	for _, candidate := range content.Candidates {
		sdpMLineIndex := uint16(candidate.SDPMLineIndex)
		ice := webrtc.ICECandidateInit{
			Candidate:        candidate.Candidate,
			SDPMid:           &candidate.SDPMID,
			SDPMLineIndex:    &sdpMLineIndex,
			UsernameFragment: new(string),
		}
		if err := c.peerConnection.AddICECandidate(ice); err != nil {
			log.Print("Failed to add ICE candidate", content)
			return err
		}
	}
	return nil
}

func (c *call) terminate() error {
	log.Printf("%s | terminating call", c.userID)

	if err := c.peerConnection.Close(); err != nil {
		log.Printf("%s | error closing peer connection: %s", c.userID, err)
	}

	c.conf.calls.callsMu.Lock()
	delete(c.conf.calls.calls, c.callID)
	c.conf.calls.callsMu.Unlock()

	info := localTrackInfo{call: c}
	c.conf.removeTracksFromPeerConnectionsByInfo(info)
	c.conf.removeTracksFromConfByInfo(info)

	return nil
}

func (c *call) sendToDevice(callType event.Type, content *event.Content) error {
	log.Printf("%s | sending to device %s", c.userID, callType.Type)
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

func (c *call) sendDataChannelMessage(msg dataChannelMessage) {
	msg.ConfID = c.conf.confID
	// TODO: Set ID

	marshaled, err := json.Marshal(msg)
	if err != nil {
		panic(err)
	}

	err = c.dataChannel.SendText(string(marshaled))
	if err != nil {
		log.Printf("%s | failed to send %s over DC: %s", c.userID, msg.Op, err)
	}

	log.Printf("%s | sent DC %s", c.userID, msg.Op)
}

func (c *call) addSubscribedTracksToPeerConnection() {
	if len(c.subscribedTracks.tracks) == 0 {
		return
	}

	newSubscribedTracks := []localTrackInfo{}
	tracksToAddToPeerConnection := []webrtc.TrackLocal{}

	c.subscribedTracks.mutex.Lock()
	for _, trackInfo := range c.subscribedTracks.tracks {
		foundTracks := c.conf.getLocalTrackByInfo(trackInfo)
		if len(foundTracks) == 0 {
			log.Printf("%s | no track found for %+v", c.userID, trackInfo)
			newSubscribedTracks = append(newSubscribedTracks, trackInfo)
		} else {
			tracksToAddToPeerConnection = append(tracksToAddToPeerConnection, foundTracks...)
		}
	}
	c.subscribedTracks.tracks = newSubscribedTracks
	c.subscribedTracks.mutex.Unlock()

	for _, track := range tracksToAddToPeerConnection {
		log.Printf("%s | adding %s track with %s", c.userID, track.Kind(), track.ID())
		if _, err := c.peerConnection.AddTrack(track); err != nil {
			panic(err)
		}
	}
}
