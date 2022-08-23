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

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix/event"
)

var rtpPacketPool = sync.Pool{
	New: func() interface{} {
		return &rtp.Packet{}
	},
}

const bufferSize = 1500

type Publisher struct {
	mutex       sync.RWMutex
	track       *webrtc.TrackRemote
	subscribers []*Subscriber
	logger      *logrus.Entry
	call        *Call
}

func NewPublisher(
	track *webrtc.TrackRemote,
	call *Call,
) *Publisher {
	publisher := new(Publisher)

	publisher.track = track
	publisher.call = call
	publisher.subscribers = []*Subscriber{}
	publisher.logger = call.logger.WithFields(logrus.Fields{
		"track_id":   track.ID(),
		"track_kind": track.Kind(),
		"stream_id":  track.StreamID(),
	})

	go publisher.WriteToSubscribers()

	return publisher
}

func (p *Publisher) AddSubscriber(subscriber *Subscriber) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.subscribers = append(p.subscribers, subscriber)
}

func (p *Publisher) GetSubscribers() []*Subscriber {
	return p.subscribers
}

func (p *Publisher) Matches(trackDescription event.SFUTrackDescription) bool {
	if p.track.ID() != trackDescription.TrackID {
		return false
	}

	if p.track.StreamID() != trackDescription.StreamID {
		return false
	}

	return true
}

func (p *Publisher) WriteToSubscribers() {
	buff := make([]byte, bufferSize)

	for {
		index, _, err := p.track.Read(buff)
		if err != nil {
			if errors.Is(err, io.EOF) {
				p.logger.WithError(err).Warn("EOF")
				return
			}

			p.logger.WithError(err).Warn("failed to read track")
		}

		for _, subscriber := range p.subscribers {
			if _, err = subscriber.track.Write(buff[:index]); err != nil {
				if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, io.EOF) {
					p.DeleteSubscriber(subscriber)
					return
				}

				p.logger.WithError(err).Warn("failed to write to track")
			}
		}
	}
}

func (p *Publisher) DeleteSubscriber(toDelete *Subscriber) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	newSubscribers := []*Subscriber{}

	for _, subscriber := range p.subscribers {
		if subscriber != toDelete {
			newSubscribers = append(newSubscribers, subscriber)
		}
	}

	p.subscribers = newSubscribers
}
