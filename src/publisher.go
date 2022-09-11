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

	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix/event"
)

type Publisher struct {
	Tracks []*webrtc.TrackRemote
	Call   *Call

	mutex       sync.RWMutex
	logger      *logrus.Entry
	subscribers []*Subscriber
}

func NewPublisher(
	track *webrtc.TrackRemote,
	call *Call,
) *Publisher {
	publisher := new(Publisher)

	publisher.Tracks = []*webrtc.TrackRemote{}
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

	publisher.addTrack(track)

	return publisher
}

func (p *Publisher) TrackID() string {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	return p.Tracks[0].ID()
}

func (p *Publisher) StreamID() string {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	return p.Tracks[0].StreamID()
}

func (p *Publisher) Kind() webrtc.RTPCodecType {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	return p.Tracks[0].Kind()
}

func (p *Publisher) Codec() webrtc.RTPCodecParameters {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	return p.Tracks[0].Codec()
}

func (p *Publisher) TrackInfo() event.CallSDPStreamMetadataTrack {
	return p.Call.Conf.Metadata.GetTrackInfo(p.StreamID(), p.TrackID())
}

func (p *Publisher) ResolutionToLayer(width int, height int) SpatialLayer {
	widthRatio := p.TrackInfo().Width / width
	heightRatio := p.TrackInfo().Height / height

	switch {
	case widthRatio >= 4 || heightRatio >= 4:
		return SpatialLayerQuarter
	case widthRatio >= 2 || heightRatio >= 2:
		return SpatialLayerHalf
	default:
		return SpatialLayerFull
	}
}

// Adds track and returns true if trackID and streamID match, otherwise returns
// false.
func (p *Publisher) TryToAddTrack(track *webrtc.TrackRemote) bool {
	if p.Matches(event.SFUTrackDescription{
		TrackID:  track.ID(),
		StreamID: track.StreamID(),
	}) {
		p.addTrack(track)
		return true
	}

	return false
}

func (p *Publisher) Subscribe(call *Call) {
	subscriber := NewSubscriber(call)
	subscriber.Subscribe(p)
}

func (p *Publisher) Stop() {
	removed := p.Call.RemovePublisher(p)

	if len(p.subscribers) == 0 && !removed {
		return
	}

	for _, subscriber := range p.subscribers {
		subscriber.Unsubscribe()
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
	if p.Tracks[0] == nil {
		return false
	}

	if p.TrackID() != trackDescription.TrackID {
		return false
	}

	if p.StreamID() != trackDescription.StreamID {
		return false
	}

	return true
}

func (p *Publisher) writeToSubscribers(track *webrtc.TrackRemote) {
	for {
		packet, _, err := track.ReadRTP()
		if err != nil {
			if errors.Is(err, io.EOF) {
				p.Stop()
				return
			}

			p.logger.WithError(err).Warn("failed to read track")
		}

		for _, subscriber := range p.subscribers {
			if err = subscriber.WriteRTP(packet, RIDToSpatialLayer(track.RID())); err != nil {
				if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, io.EOF) {
					subscriber.Unsubscribe()
					break
				}

				p.logger.WithError(err).Warn("failed to write to track")
			}
		}
	}
}

func (p *Publisher) addTrack(track *webrtc.TrackRemote) {
	p.mutex.Lock()
	p.Tracks = append(p.Tracks, track)
	p.mutex.Unlock()

	p.logger.WithField("rid", track.RID()).Info("published track")

	for _, subscriber := range p.subscribers {
		subscriber.RecalculateCurrentSpatialLayer()
	}

	go p.writeToSubscribers(track)
}
