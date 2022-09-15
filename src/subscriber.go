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
	"errors"
	"io"
	"sync"
	"sync/atomic"

	"github.com/pion/rtcp"
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
	maxSpatialLayer atomic.Int32
	// The spatial layer from which are actually reading
	currentSpatialLayer atomic.Int32
	// The last SSRC we read from
	lastSSRC atomic.Uint32

	// For RTP packet header munging (see WriteRTP())
	snOffset uint16
	tsOffset uint32
	ssrc     uint32
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

	if publisher.Kind() == webrtc.RTPCodecTypeAudio {
		s.maxSpatialLayer.Store(int32(DefaultAudioSpatialLayer))
		s.currentSpatialLayer.Store(int32(DefaultAudioSpatialLayer))
	} else {
		s.maxSpatialLayer.Store(int32(DefaultVideoSpatialLayer))
		s.currentSpatialLayer.Store(int32(SpatialLayerInvalid))
	}

	s.mutex.Lock()
	s.Track = track
	s.Publisher = publisher
	s.sender = sender
	s.ssrc = uint32(sender.GetParameters().Encodings[0].SSRC)
	s.mutex.Unlock()

	s.RecalculateCurrentSpatialLayer()

	go s.writeRTCP()

	publisher.AddSubscriber(s)

	s.logger.Info("subscribed")
}

func (s *Subscriber) Unsubscribe() {
	if s.Publisher == nil {
		return
	}

	s.Publisher.RemoveSubscriber(s)
	s.call.RemoveSubscriber(s)

	s.mutex.Lock()
	s.Publisher = nil
	s.mutex.Unlock()

	if s.call.PeerConnection.ConnectionState() != webrtc.PeerConnectionStateClosed {
		err := s.call.PeerConnection.RemoveTrack(s.sender)
		if err != nil {
			s.logger.WithError(err).Error("failed to remove track")
		}
	}

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
	if SpatialLayer(s.currentSpatialLayer.Load()) != layer {
		return nil
	}

	// If we changed tracks, calculate an offset by which we shift the sequence
	// numbers and timestamps
	lastSSRC := s.lastSSRC.Load()
	if lastSSRC != packet.SSRC && lastSSRC != 0 {
		s.snOffset = packet.SequenceNumber - s.lastSN - 1
		s.tsOffset = packet.Timestamp - s.lastTS - 1
	}

	s.lastSSRC.Store(packet.SSRC)

	packet.SSRC = s.ssrc
	packet.SequenceNumber -= s.snOffset
	packet.Timestamp -= s.tsOffset

	s.lastSN = packet.SequenceNumber
	s.lastTS = packet.Timestamp

	return s.Track.WriteRTP(packet)
}

func (s *Subscriber) SetSettings(width int, height int) {
	if s.Publisher.Kind() != webrtc.RTPCodecTypeVideo {
		return
	}

	if width == 0 || height == 0 {
		return
	}

	newLayer := s.Publisher.ResolutionToLayer(width, height)
	if newLayer != SpatialLayer(s.maxSpatialLayer.Load()) {
		s.maxSpatialLayer.Store(int32(newLayer))
		s.RecalculateCurrentSpatialLayer()
	}
}

func (s *Subscriber) RecalculateCurrentSpatialLayer() {
	if s.Publisher.Kind() != webrtc.RTPCodecTypeVideo {
		return
	}

	// First we find the worst layer, which we will use as a fallback, this is
	// useful for case where the max layer is e.g. 0 but the worst available one
	// is 1
	worstLayer := SpatialLayerInvalid

	for _, track := range s.Publisher.Tracks {
		layer := RIDToSpatialLayer(track.RID())
		if worstLayer == SpatialLayerInvalid || layer < worstLayer {
			worstLayer = layer
		}
	}

	// Then we try to find the best layer which is less than maximum
	bestLayer := worstLayer

	for _, track := range s.Publisher.Tracks {
		layer := RIDToSpatialLayer(track.RID())
		if layer > bestLayer && layer <= SpatialLayer(s.maxSpatialLayer.Load()) {
			bestLayer = layer
		}
	}

	if bestLayer != SpatialLayer(s.currentSpatialLayer.Load()) {
		s.logger.WithField("layer", bestLayer).Info("switching current spatial layer")
		s.Publisher.RequestLayerSwitch(s, bestLayer)
	}
}

func (s *Subscriber) SetNewCurrentSpatialLayer(layer SpatialLayer) {
	s.currentSpatialLayer.Store(int32(layer))
	s.logger.WithField("layer", layer).Info("switched current spatial layer")
}

func (s *Subscriber) writeRTCP() {
	if s.Track.Kind() != webrtc.RTPCodecTypeVideo {
		return
	}

	for {
		lastSSRC := s.lastSSRC.Load()
		if lastSSRC == 0 {
			continue
		}

		packets, _, err := s.sender.ReadRTCP()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}

			s.logger.WithError(err).Warn("failed to read RTCP on track")
		}

		packetsToForward := []rtcp.Packet{}
		readSSRC := s.lastSSRC.Load()

		for _, packet := range packets {
			switch typedPacket := packet.(type) {
			// We mung the packets here, so that the SSRCs match what the
			// receiver expects
			case *rtcp.PictureLossIndication:
				typedPacket.SenderSSRC = s.ssrc
				typedPacket.MediaSSRC = readSSRC
				packetsToForward = append(packetsToForward, typedPacket)
			case *rtcp.FullIntraRequest:
				typedPacket.SenderSSRC = s.ssrc
				typedPacket.MediaSSRC = readSSRC
				packetsToForward = append(packetsToForward, typedPacket)
			}
		}

		// TODO: Change layers based on RTCP

		if len(packetsToForward) < 1 {
			continue
		}

		s.Publisher.WriteRTCP(packetsToForward)
	}
}
