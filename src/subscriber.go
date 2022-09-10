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
	maxSpatialLayer SpatialLayer
	// The spatial layer from which are actually reading
	CurrentSpatialLayer SpatialLayer

	// For RTP packet header munging (see WriteRTP())
	snOffset uint16
	tsOffset uint32
	ssrc     uint32
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

	lastSSRC := atomic.LoadUint32(&s.lastSSRC)
	sendPLI := false

	if lastSSRC == 0 {
		lastSSRC = packet.SSRC
		sendPLI = true
	} else if lastSSRC != packet.SSRC {
		lastSSRC = packet.SSRC
		sendPLI = true

		s.snOffset = packet.SequenceNumber - s.lastSN - 1
		s.tsOffset = packet.Timestamp - s.lastTS - 1
	}

	if sendPLI {
		// Manually request a keyframe from the sender since waiting for the
		// receiver to send a PLI would take too long and result in a few
		// second freeze of the video
		if err := s.Publisher.Call.PeerConnection.WriteRTCP([]rtcp.Packet{
			&rtcp.PictureLossIndication{MediaSSRC: packet.SSRC, SenderSSRC: s.ssrc},
		}); err != nil {
			if errors.Is(err, io.ErrClosedPipe) {
				return err
			}

			s.logger.WithError(err).Warn("failed to write RTCP on track")
		}

		s.logger.Info("SSRC changed: sending PLI")
	}

	packet.SSRC = lastSSRC
	packet.SequenceNumber -= s.snOffset
	packet.Timestamp -= s.tsOffset

	s.lastSN = packet.SequenceNumber
	s.lastTS = packet.Timestamp
	atomic.StoreUint32(&s.lastSSRC, lastSSRC)

	return s.Track.WriteRTP(packet)
}

func (s *Subscriber) SetSettings(width int, height int) {
	if width == 0 || height == 0 {
		return
	}

	newLayer := s.Publisher.ResolutionToLayer(width, height)
	if newLayer != s.maxSpatialLayer {
		s.maxSpatialLayer = newLayer
		s.RecalculateCurrentSpatialLayer()
	}
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

	s.logger.WithField("layer", s.CurrentSpatialLayer).Info("changed current spatial layer")
}

func (s *Subscriber) writeRTCP() {
	if s.Track.Kind() != webrtc.RTPCodecTypeVideo {
		return
	}

	for {
		packets, _, err := s.sender.ReadRTCP()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}

			s.logger.WithError(err).Warn("failed to read RTCP on track")
		}

		packetsToForward := []rtcp.Packet{}
		lastSSRC := atomic.LoadUint32(&s.lastSSRC)

		for _, packet := range packets {
			switch typedPacket := packet.(type) {
			// We mung the packets here, so that the SSRCs match what the
			// receiver expects
			case *rtcp.PictureLossIndication:
				typedPacket.SenderSSRC = s.ssrc
				typedPacket.MediaSSRC = lastSSRC
				packetsToForward = append(packetsToForward, typedPacket)
			case *rtcp.FullIntraRequest:
				typedPacket.SenderSSRC = s.ssrc
				typedPacket.MediaSSRC = lastSSRC
				packetsToForward = append(packetsToForward, typedPacket)
			}
		}

		// TODO: Change layers based on RTCP

		if len(packetsToForward) < 1 {
			continue
		}

		s.logger.Info("forwarding RTCP")

		err = s.Publisher.Call.PeerConnection.WriteRTCP(packetsToForward)
		if err != nil {
			if errors.Is(err, io.ErrClosedPipe) {
				return
			}

			s.logger.WithError(err).Warn("failed to write RTCP on track")
		}
	}
}
