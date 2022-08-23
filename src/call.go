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
	"sync"
	"time"

	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type Call struct {
	mutex                  sync.RWMutex
	CallID                 string
	UserID                 id.UserID
	DeviceID               id.DeviceID
	LocalSessionID         id.SessionID
	RemoteSessionID        id.SessionID
	Client                 *mautrix.Client
	PeerConnection         *webrtc.PeerConnection
	Conf                   *Conference
	Publishers             []*Publisher
	dataChannel            *webrtc.DataChannel
	lastKeepAliveTimestamp time.Time
	sentEndOfCandidates    bool
	logger                 *logrus.Entry
	subscribers            []*Subscriber
}

func (c *Call) onDCSelect(start []event.SFUTrackDescription) {
	if len(start) == 0 {
		return
	}

	for _, trackDesc := range start {
		trackLogger := c.logger.WithFields(logrus.Fields{
			"track_id":  trackDesc.TrackID,
			"stream_id": trackDesc.StreamID,
		})

		trackLogger.Info("selecting track")

		for _, publisher := range c.Conf.GetPublishers() {
			if !publisher.Matches(trackDesc) {
				continue
			}

			streamID := ""

			// If there already exists a subscriber subscribing to a publisher
			// with the same streamID as the publisher we're going to subscribe
			// to, use its streamID to correctly group tracks
			for _, subscriber := range c.subscribers {
				if subscriber.GetPublisher() != nil && subscriber.GetPublisher().track.StreamID() == publisher.track.StreamID() {
					streamID = subscriber.track.StreamID()
				}
			}

			for _, subscriber := range c.subscribers {
				if !subscriber.CanSubscribe(publisher, streamID) {
					continue
				}

				subscriber.Subscribe(publisher)

				c.SendDataChannelMessage(event.SFUMessage{Op: event.SFUOperationMetadata})

				break
			}
		}
	}
}

func (c *Call) onDCPublish(start []event.SFUTrackDescription) {
	c.logger.Info("received DC publish")

	for _, description := range start {
		for _, transceiver := range c.PeerConnection.GetTransceivers() {
			track := transceiver.Receiver().Track()
			if track.ID() == description.TrackID && track.StreamID() == description.StreamID {
				// TODO: Start receiver somehow
				c.trackHandler(track)
				break
			}
		}
	}
}

func (c *Call) onDCUnpublish(stop []event.SFUTrackDescription, sdp string) {
	for _, trackDesc := range stop {
		trackLogger := c.logger.WithFields(logrus.Fields{
			"track_id":  trackDesc.TrackID,
			"stream_id": trackDesc.StreamID,
		})

		// TODO: This should actually do something
		trackLogger.Info("unpublishing track")
	}

	err := c.PeerConnection.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  sdp,
	})
	if err != nil {
		c.logger.WithField("sdp", sdp).WithError(err).Error("failed to set remote description - ignoring")
		return
	}

	offer, err := c.PeerConnection.CreateAnswer(nil)
	if err != nil {
		c.logger.WithError(err).Error("failed to create answer - ignoring")
		return
	}

	err = c.PeerConnection.SetLocalDescription(offer)
	if err != nil {
		c.logger.WithField("sdp", offer.SDP).WithError(err).Error("failed to set local description - ignoring")
		return
	}

	c.SendDataChannelMessage(event.SFUMessage{
		Op:  event.SFUOperationAnswer,
		SDP: offer.SDP,
	})
}

func (c *Call) onDCAnswer(sdp string) {
	c.logger.Info("received DC answer")

	err := c.PeerConnection.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  sdp,
	})
	if err != nil {
		c.logger.WithField("sdp", sdp).WithError(err).Error("failed to set remote description - ignoring")
		return
	}
}

func (c *Call) onDCAlive() {
	c.lastKeepAliveTimestamp = time.Now()
}

func (c *Call) onDCMetadata() {
	c.logger.Info("received DC metadata")

	c.Conf.SendUpdatedMetadataFromCall(c.CallID)
}

func (c *Call) dataChannelHandler(channel *webrtc.DataChannel) {
	c.dataChannel = channel

	channel.OnOpen(func() {
		c.SendDataChannelMessage(event.SFUMessage{Op: event.SFUOperationPublish})
	})

	channel.OnError(func(err error) {
		logrus.Fatalf("%s | DC error: %s", c.CallID, err)
	})

	channel.OnMessage(func(marshaledMsg webrtc.DataChannelMessage) {
		if !marshaledMsg.IsString {
			c.logger.WithField("msg", marshaledMsg).Warn("inbound message is not string - ignoring")
			return
		}

		msg := &event.SFUMessage{}
		if err := json.Unmarshal(marshaledMsg.Data, msg); err != nil {
			c.logger.WithField("msg", marshaledMsg).WithError(err).Error("failed to unmarshal - ignoring")
			return
		}

		if msg.Metadata != nil {
			c.Conf.UpdateSDPStreamMetadata(c.DeviceID, msg.Metadata)
		}

		switch msg.Op {
		case event.SFUOperationSelect:
			c.onDCSelect(msg.Start)
		case event.SFUOperationPublish:
			c.onDCPublish(msg.Start)
		case event.SFUOperationUnpublish:
			c.onDCUnpublish(msg.Stop, msg.SDP)
		case event.SFUOperationAnswer:
			c.onDCAnswer(msg.SDP)
		case event.SFUOperationAlive:
			c.onDCAlive()
		case event.SFUOperationMetadata:
			c.onDCMetadata()

		default:
			c.logger.WithField("op", msg.Op).Warn("Unknown operation - ignoring")
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
		c.logger.WithError(err).Error("failed to create offer - ignoring")
		return
	}

	err = c.PeerConnection.SetLocalDescription(offer)
	if err != nil {
		c.logger.WithField("sdp", offer.SDP).WithError(err).Error("failed to set local description - ignoring")
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

func (c *Call) trackHandler(trackRemote *webrtc.TrackRemote) {
	trackLogger := c.logger.WithFields(logrus.Fields{
		"track_id":   trackRemote.ID(),
		"track_kind": trackRemote.Kind(),
		"stream_id":  trackRemote.StreamID(),
	})

	go WriteRTCP(trackRemote, c.PeerConnection, trackLogger)

	publisher := NewPublisher(trackRemote, c)

	c.mutex.Lock()
	c.Publishers = append(c.Publishers, publisher)
	c.mutex.Unlock()

	trackLogger.Info("published track")

	go c.Conf.SendUpdatedMetadataFromCall(c.CallID)
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

	//peerConnection, err := c.Conf.api.NewPeerConnection(webrtc.Configuration{})
	peerConnection, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		logrus.WithError(err).Error("failed to create new peer connection")
	}

	c.PeerConnection = peerConnection

	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		c.trackHandler(track)
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
		c.logger.WithField("sdp", offer.SDP).WithError(err).Error("failed to set remote description - ignoring")
		return
	}

	lastAudioTrackStreamID := ""
	lastVideoTrackStreamID := ""

	for _, transceiver := range c.PeerConnection.GetTransceivers() {
		trackID, err := GenerateID()
		if err != nil {
			c.logger.WithError(err).Error("failed to generate trackID")
		}

		// FIXME: Can this be done any better?
		streamID := ""
		if lastAudioTrackStreamID == lastVideoTrackStreamID {
			streamID, err = GenerateID()
			if err != nil {
				c.logger.WithError(err).Error("failed to generate streamID")
			}

			if transceiver.Kind() == webrtc.RTPCodecTypeAudio {
				lastAudioTrackStreamID = streamID
				lastVideoTrackStreamID = ""
			} else {
				lastVideoTrackStreamID = streamID
				lastAudioTrackStreamID = ""
			}
		} else if transceiver.Kind() == webrtc.RTPCodecTypeAudio && lastAudioTrackStreamID == "" {
			streamID = lastVideoTrackStreamID
			lastAudioTrackStreamID = streamID
		} else if transceiver.Kind() == webrtc.RTPCodecTypeVideo && lastVideoTrackStreamID == "" {
			streamID = lastAudioTrackStreamID
			lastVideoTrackStreamID = streamID
		}

		trackLocal, err := webrtc.NewTrackLocalStaticRTP(
			// FIXME: We naively expect all clients to support this codec for sending
			transceiver.Receiver().GetParameters().Codecs[0].RTPCodecCapability,
			trackID,
			streamID,
		)
		if err != nil {
			panic(err)
		}

		if _, err := peerConnection.AddTrack(trackLocal); err != nil {
			c.logger.WithError(err).Error("Failed to add track during pool setup")
		}

		subscriber := NewSubscriber(trackLocal, c)

		c.mutex.Lock()
		c.subscribers = append(c.subscribers, subscriber)
		c.mutex.Unlock()
	}

	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		c.logger.WithError(err).Error("failed to create answer - ignoring")
		return
	}

	// TODO: trickle ICE for fast conn setup, rather than block here
	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)

	if err = peerConnection.SetLocalDescription(answer); err != nil {
		c.logger.WithField("sdp", offer.SDP).WithError(err).Error("failed to set local description - ignoring")
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
		c.logger.WithField("selected_party_id", selectedPartyID).Warn("call was answered on a different device")
		c.Terminate()
	}
}

func (c *Call) OnHangup() {
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
			c.logger.WithField("content", content).WithError(err).Error("failed to add ICE candidate")
		}
	}
}

func (c *Call) Terminate() {
	c.logger.Info("terminating call")

	if err := c.PeerConnection.Close(); err != nil {
		c.logger.WithError(err).Error("error closing peer connection")
	}

	c.Conf.Calls.CallsMu.Lock()
	delete(c.Conf.Calls.Calls, c.CallID)
	c.Conf.Calls.CallsMu.Unlock()

	for _, publisher := range c.Publishers {
		for _, subscriber := range publisher.GetSubscribers() {
			subscriber.Unsubscribe()
		}
	}

	for _, subscriber := range c.subscribers {
		subscriber.Unsubscribe()
	}

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
	evtLogger := c.logger.WithFields(logrus.Fields{
		"type": callType.Type,
	})

	if callType.Type != event.CallCandidates.Type {
		evtLogger.Info("sending to device")
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
	if _, err := c.Client.SendToDevice(callType, toDevice); err != nil {
		evtLogger.WithField("content", content).WithError(err).Error("error sending to-device")
	}
}

func (c *Call) SendDataChannelMessage(msg event.SFUMessage) {
	if c.dataChannel == nil {
		return
	}

	if msg.Metadata == nil {
		if msg.Op == event.SFUOperationPublish {
			msg.Metadata = c.Conf.GetRemoteMetadataForDevice(c.DeviceID)
		} else {
			msg.Metadata = c.GetMetadata()
		}

		if msg.Op == event.SFUOperationPublish && len(msg.Metadata) == 0 {
			return
		}
	}

	evtLogger := c.logger.WithFields(logrus.Fields{
		"op": msg.Op,
	})

	marshaled, err := json.Marshal(msg)
	if err != nil {
		evtLogger.WithField("msg", msg).WithError(err).Error("failed to marshal - ignoring")
		return
	}

	err = c.dataChannel.SendText(string(marshaled))
	if err != nil {
		evtLogger.WithField("msg", msg).WithError(err).Error("failed to send message over DC")
	}

	evtLogger.Info("sent message over DC")
}

func (c *Call) CheckKeepAliveTimestamp() {
	timeout := time.Second * time.Duration(config.Timeout)
	for range time.Tick(timeout) {
		if c.lastKeepAliveTimestamp.Add(timeout).Before(time.Now()) {
			if c.PeerConnection.ConnectionState() != webrtc.PeerConnectionStateClosed {
				c.logger.WithField("timeout", timeout).Warn("did not get keep-alive message")
				c.Hangup(event.CallHangupKeepAliveTimeout)
			}

			break
		}
	}
}

func (c *Call) GetMetadata() event.CallSDPStreamMetadata {
	metadata := make(event.CallSDPStreamMetadata)

	for _, subscriber := range c.subscribers {
		if subscriber.publisher == nil {
			continue
		}

		if c.DeviceID == subscriber.publisher.call.DeviceID {
			continue
		}

		streamID := subscriber.track.StreamID()
		trackID := subscriber.track.ID()

		info, exists := metadata[streamID]
		if exists {
			info.Tracks[trackID] = event.CallSDPStreamMetadataTrack{}
			metadata[streamID] = info
		} else {
			metadata[streamID] = event.CallSDPStreamMetadataObject{
				UserID:   subscriber.publisher.call.UserID,
				DeviceID: subscriber.publisher.call.DeviceID,
				Purpose:  c.Conf.Metadata.Metadata[streamID].Purpose,
				Tracks: event.CallSDPStreamMetadataTracks{
					trackID: {},
				},
			}
		}
	}

	return metadata
}
