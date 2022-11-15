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
	"sync"

	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

var (
	ErrNoSuchCall                                  = errors.New("no such call")
	ErrFoundExistingCallWithSameSessionAndDeviceID = errors.New("found existing call with equal DeviceID and SessionID")
)

// Configuration for the group conferences (calls).
type ConferenceConfig struct {
	// Keep-alive timeout for WebRTC connections. If no keep-alive has been received
	// from the client for this duration, the connection is considered dead.
	KeepAliveTimeout int
}

type Conference struct {
	ConfID string
	Calls  map[string]*Call  // By callID
	Config *ConferenceConfig // TODO: this must be protected by a mutex actually

	mutex    sync.RWMutex
	logger   *logrus.Entry
	Metadata *Metadata
}

func NewConference(confID string, config *ConferenceConfig) *Conference {
	conference := new(Conference)

	conference.Config = config
	conference.ConfID = confID
	conference.Calls = make(map[string]*Call)
	conference.Metadata = NewMetadata(conference)
	conference.logger = logrus.WithFields(logrus.Fields{
		"conf_id": confID,
	})

	return conference
}

func (c *Conference) GetCall(callID string, create bool) (*Call, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	call := c.Calls[callID]

	if call == nil {
		if create {
			call = NewCall(callID, c)
			c.Calls[callID] = call
		} else {
			return nil, ErrNoSuchCall
		}
	}

	return call, nil
}

func (c *Conference) RemoveOldCallsByDeviceAndSessionIDs(deviceID id.DeviceID, sessionID id.SessionID) error {
	var err error

	for _, call := range c.Calls {
		if call.DeviceID == deviceID {
			if call.RemoteSessionID == sessionID {
				err = ErrFoundExistingCallWithSameSessionAndDeviceID
			} else {
				call.Terminate()
			}
		}
	}

	return err
}

func (c *Conference) SendUpdatedMetadataFromCall(callID string) {
	for _, call := range c.Calls {
		if call.CallID != callID {
			call.SendDataChannelMessage(event.SFUMessage{Op: event.SFUOperationMetadata})
		}
	}
}

func (c *Conference) GetPublishers() []*Publisher {
	publishers := []*Publisher{}

	c.mutex.RLock()
	for _, call := range c.Calls {
		publishers = append(publishers, call.Publishers...)
	}
	c.mutex.RUnlock()

	return publishers
}
