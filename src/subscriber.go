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

	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
)

type Subscriber struct {
	mutex     sync.RWMutex
	track     *webrtc.TrackLocalStaticRTP
	publisher *Publisher
	logger    *logrus.Entry
	call      *Call
}

func NewSubscriber(track *webrtc.TrackLocalStaticRTP, call *Call) *Subscriber {
	subscriber := new(Subscriber)

	subscriber.track = track
	subscriber.call = call
	subscriber.logger = call.logger.WithFields(logrus.Fields{
		"track_id":   (*track).ID(),
		"track_kind": (*track).Kind(),
		"stream_id":  (*track).StreamID(),
	})

	return subscriber
}

func (s *Subscriber) Subscribe(publisher *Publisher) {
	s.logger.Info("subscribing")

	publisher.AddSubscriber(s)

	s.mutex.Lock()
	s.publisher = publisher
	s.mutex.Unlock()
}

func (s *Subscriber) Unsubscribe() {
	if s.publisher == nil {
		return
	}

	s.logger.Info("unsubscribing")

	s.publisher.DeleteSubscriber(s)

	s.mutex.Lock()
	s.publisher = nil
	s.mutex.Unlock()
}

func (s *Subscriber) GetPublisher() *Publisher {
	return s.publisher
}

func (s *Subscriber) CanSubscribe(publisher *Publisher, requestedStreamID string) bool {
	if s.publisher != nil {
		return false
	}

	if publisher.track.Kind() != s.track.Kind() {
		return false
	}

	if requestedStreamID != "" && requestedStreamID != s.track.StreamID() {
		return false
	}

	return true
}
