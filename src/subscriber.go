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
	"sync"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
)

type Subscriber struct {
	Track     *webrtc.TrackLocalStaticRTP
	Publisher *Publisher

	mutex  sync.RWMutex
	logger *logrus.Entry
	call   *Call
	sender *webrtc.RTPSender

	// The spatial layer from which we would like to read
	maxSpatialLayer SpatialLayer
	// The spatial layer from which are actually reading
	CurrentSpatialLayer SpatialLayer

	// For RTP packet header munging (see WriteRTP())
	snOffset uint16
	tsOffset uint32
	lastSSRC uint32
	lastSN   uint16
	lastTS   uint32
}

func NewSubscriber(call *Call) *Subscriber {
	subscriber := new(Subscriber)

	subscriber.call = call
	subscriber.logger = call.logger

	call.mutex.Lock()
	call.Subscribers = append(call.Subscribers, subscriber)
	call.mutex.Unlock()

	return subscriber
}

func (s *Subscriber) initLoggingWithTrack(publisher *Publisher) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.logger = s.call.logger.WithFields(logrus.Fields{
		"track_id":   publisher.TrackID(),
		"track_kind": publisher.Kind(),
		"stream_id":  publisher.StreamID(),
	})
}

func (s *Subscriber) Subscribe(publisher *Publisher) {
	s.initLoggingWithTrack(publisher)

	track, err := webrtc.NewTrackLocalStaticRTP(
		publisher.Codec().RTPCodecCapability,
		publisher.TrackID(),
		publisher.StreamID(),
	)
	if err != nil {
		s.logger.WithError(err).Error("failed to create local static RTP track")
	}

	sender, err := s.call.PeerConnection.AddTrack(track)
	if err != nil {
		s.logger.WithError(err).Error("failed to add track to peer connection")
	}

	s.mutex.Lock()
	if publisher.Kind() == webrtc.RTPCodecTypeAudio {
		s.maxSpatialLayer = DefaultAudioSpatialLayer
	} else {
		s.maxSpatialLayer = DefaultVideoSpatialLayer
	}
	s.Track = track
	s.Publisher = publisher
	s.sender = sender
	s.mutex.Unlock()

	s.RecalculateCurrentSpatialLayer()

	publisher.AddSubscriber(s)

	s.logger.Info("subscribed")
}

func (s *Subscriber) Unsubscribe() {
	if s.Publisher == nil {
		return
	}

	if s.call.PeerConnection.ConnectionState() != webrtc.PeerConnectionStateClosed {
		err := s.call.PeerConnection.RemoveTrack(s.sender)
		if err != nil {
			s.logger.WithError(err).Error("failed to remove track")
		}
	}

	s.call.RemoveSubscriber(s)

	s.mutex.Lock()
	s.Publisher = nil
	s.mutex.Unlock()

	s.logger.Info("unsubscribed")
}

// This method writes a given RTP packet to the subscriber's
// TrackLocalStaticRTP.
// If the layer passed to this method does not match the subscriber's layer,
// the packet will be ignored.
// Due to layer switching being essentially track switching, this method munges
// the RTP packet, so that the client doesn't see any jumps in sequence numbers,
// timestamps or SSRC.
func (s *Subscriber) WriteRTP(packet *rtp.Packet, layer SpatialLayer) error {
	if s.CurrentSpatialLayer != layer {
		return nil
	}

	if s.lastSSRC != packet.SSRC {
		s.logger.Infof("SSRC changed %s != %s", packet.SSRC, s.lastSSRC)
		s.lastSSRC = packet.SSRC

		s.snOffset = packet.SequenceNumber - s.lastSN - 1
		s.tsOffset = packet.Timestamp - s.lastTS
	}

	packet.SSRC = s.lastSSRC
	packet.SequenceNumber = packet.SequenceNumber - s.snOffset
	packet.Timestamp = packet.Timestamp - s.tsOffset

	s.lastSN = packet.SequenceNumber
	s.lastTS = packet.Timestamp

	return s.Track.WriteRTP(packet)
}

func (s *Subscriber) SetSettings(width int, height int) {
	if width == 0 || height == 0 {
		return
	}

	s.maxSpatialLayer = s.Publisher.ResolutionToLayer(width, height)
	s.RecalculateCurrentSpatialLayer()
}

func (s *Subscriber) RecalculateCurrentSpatialLayer() {
	best := SpatialLayerInvalid

	for _, track := range s.Publisher.Tracks {
		layer := RIDToSpatialLayer(track.RID())
		if layer >= best && layer <= s.maxSpatialLayer {
			best = layer
		}
	}

	s.mutex.Lock()
	s.CurrentSpatialLayer = best
	s.mutex.Unlock()

	s.logger.WithField("current_spatial_layer", s.CurrentSpatialLayer).Info("changed current spatial layer")
}
