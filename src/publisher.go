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
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix/event"
)

const (
	minimalPLIInterval = time.Millisecond * 500
	bufferSize         = 1500
)

type Publisher struct {
	Track *webrtc.TrackRemote
	Call  *Call

	mutex       sync.RWMutex
	logger      *logrus.Entry
	subscribers []*Subscriber

	lastPLI atomic.Int64
}

func NewPublisher(
	track *webrtc.TrackRemote,
	call *Call,
) *Publisher {
	publisher := new(Publisher)

	publisher.Track = track
	publisher.Call = call

	publisher.subscribers = []*Subscriber{}
	publisher.logger = call.logger.WithFields(logrus.Fields{
		"track_id":   track.ID(),
		"track_kind": track.Kind(),
		"stream_id":  track.StreamID(),
	})

	call.mutex.Lock()
	call.Publishers = append(call.Publishers, publisher)
	call.mutex.Unlock()

	go publisher.WriteToSubscribers()

	publisher.logger.Info("published track")

	return publisher
}

func (p *Publisher) Subscribe(call *Call) {
	subscriber := NewSubscriber(call)
	subscriber.Subscribe(p)
	p.AddSubscriber(subscriber)
}

func (p *Publisher) Stop() {
	removed := p.Call.RemovePublisher(p)

	if len(p.subscribers) == 0 && !removed {
		return
	}

	for _, subscriber := range p.subscribers {
		subscriber.Unsubscribe()
		p.RemoveSubscriber(subscriber)
	}

	p.logger.Info("unpublished track")
}

func (p *Publisher) AddSubscriber(subscriber *Subscriber) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.subscribers = append(p.subscribers, subscriber)
}

func (p *Publisher) RemoveSubscriber(toDelete *Subscriber) {
	newSubscribers := []*Subscriber{}

	p.mutex.Lock()
	for _, subscriber := range p.subscribers {
		if subscriber != toDelete {
			newSubscribers = append(newSubscribers, subscriber)
		}
	}

	p.subscribers = newSubscribers
	p.mutex.Unlock()
}

func (p *Publisher) Matches(trackDescription event.SFUTrackDescription) bool {
	if p.Track.ID() != trackDescription.TrackID {
		return false
	}

	if p.Track.StreamID() != trackDescription.StreamID {
		return false
	}

	return true
}

func (p *Publisher) WriteRTCP(packets []rtcp.Packet) {
	packetsToSend := []rtcp.Packet{}
	readSSRC := uint32(p.Track.SSRC())

	for _, packet := range packets {
		switch typedPacket := packet.(type) {
		// We mung the packets here, so that the SSRCs match what the
		// receiver expects:
		// The media SSRC is the SSRC of the media about which the packet is
		// reporting; therefore, we mung it to be the SSRC of the publishing
		// participant's track. Without this, it would be SSRC of the SFU's
		// track which isn't right
		case *rtcp.PictureLossIndication:
			// Since we sometimes spam the sender with PLIs, make sure we don't send
			// them way too often
			if time.Now().UnixNano()-p.lastPLI.Load() < minimalPLIInterval.Nanoseconds() {
				continue
			}

			p.lastPLI.Store(time.Now().UnixNano())

			typedPacket.MediaSSRC = readSSRC
			packetsToSend = append(packetsToSend, typedPacket)
		case *rtcp.FullIntraRequest:
			typedPacket.MediaSSRC = readSSRC
			packetsToSend = append(packetsToSend, typedPacket)
		}

		packetsToSend = append(packetsToSend, packet)
	}

	if len(packetsToSend) != 0 {
		if err := p.Call.PeerConnection.WriteRTCP(packetsToSend); err != nil {
			if !errors.Is(err, io.ErrClosedPipe) {
				p.logger.WithError(err).Warn("failed to write RTCP on track")
			}
		}
	}
}

func (p *Publisher) WriteToSubscribers() {
	buff := make([]byte, bufferSize)

	for {
		index, _, err := p.Track.Read(buff)
		if err != nil {
			if errors.Is(err, io.EOF) {
				p.Stop()
				return
			}

			p.logger.WithError(err).Warn("failed to read track")
		}

		for _, subscriber := range p.subscribers {
			if _, err = subscriber.Track.Write(buff[:index]); err != nil {
				if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, io.EOF) {
					subscriber.Unsubscribe()
					p.RemoveSubscriber(subscriber)

					return
				}

				p.logger.WithError(err).Warn("failed to write to track")
			}
		}
	}
}
