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

var ErrNoSuchCall = errors.New("no such call")
var ErrFoundExistingCallWithSameSessionAndDeviceID = errors.New("found existing call with equal DeviceID and SessionID")

type Calls struct {
	CallsMu sync.RWMutex
	Calls   map[string]*Call // By callID
}

type Metadata struct {
	Mutex    sync.RWMutex
	Metadata event.CallSDPStreamMetadata
}

type Conference struct {
	Mutex    sync.Mutex
	ConfID   string
	Calls    Calls
	Metadata Metadata
	logger   *logrus.Entry
}

func (c *Conference) GetCall(callID string, create bool) (*Call, error) {
	c.Calls.CallsMu.Lock()
	defer c.Calls.CallsMu.Unlock()
	call := c.Calls.Calls[callID]

	if call == nil {
		if create {
			call = &Call{
				CallID: callID,
				Conf:   c,
			}
			c.Calls.Calls[callID] = call
		} else {
			return nil, ErrNoSuchCall
		}
	}

	return call, nil
}

func (c *Conference) RemoveOldCallsByDeviceAndSessionIDs(deviceID id.DeviceID, sessionID id.SessionID) error {
	var err error

	for _, call := range c.Calls.Calls {
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

func (c *Conference) UpdateSDPStreamMetadata(deviceID id.DeviceID, metadata event.CallSDPStreamMetadata) {
	c.Metadata.Mutex.Lock()
	defer c.Metadata.Mutex.Unlock()

	// Update existing and add new
	for streamID, info := range metadata {
		c.Metadata.Metadata[streamID] = info
	}
	// Remove removed
	for streamID, info := range c.Metadata.Metadata {
		_, exists := metadata[streamID]
		if info.DeviceID == deviceID && !exists {
			delete(c.Metadata.Metadata, streamID)
		}
	}
}

// Get metadata to send to deviceID. This will not include the device's own
// metadata and metadata which includes tracks which we have not received yet.
func (c *Conference) GetRemoteMetadataForDevice(deviceID id.DeviceID) event.CallSDPStreamMetadata {
	metadata := make(event.CallSDPStreamMetadata)

	for _, publisher := range c.GetPublishers() {
		if deviceID == publisher.call.DeviceID {
			continue
		}

		streamID := publisher.track.StreamID()
		trackID := publisher.track.ID()

		info, exists := metadata[streamID]
		if exists {
			info.Tracks[publisher.track.ID()] = event.CallSDPStreamMetadataTrack{}
			metadata[streamID] = info
		} else {
			metadata[streamID] = event.CallSDPStreamMetadataObject{
				UserID:   publisher.call.UserID,
				DeviceID: publisher.call.DeviceID,
				Purpose:  c.Metadata.Metadata[streamID].Purpose,
				Tracks: event.CallSDPStreamMetadataTracks{
					trackID: {},
				},
			}
		}
	}

	return metadata
}

func (c *Conference) RemoveMetadataByDeviceID(deviceID id.DeviceID) {
	c.Metadata.Mutex.Lock()
	defer c.Metadata.Mutex.Unlock()

	for streamID, info := range c.Metadata.Metadata {
		if info.DeviceID == deviceID {
			delete(c.Metadata.Metadata, streamID)
		}
	}
}

func (c *Conference) SendUpdatedMetadataFromCall(callID string) {
	for _, call := range c.Calls.Calls {
		if call.CallID != callID {
			call.SendDataChannelMessage(event.SFUMessage{Op: event.SFUOperationMetadata})
			call.SendDataChannelMessage(event.SFUMessage{Op: event.SFUOperationPublish})
		}
	}
}

func (c *Conference) GetPublishers() []*Publisher {
	publishers := []*Publisher{}

	c.Calls.CallsMu.RLock()
	for _, call := range c.Calls.Calls {
		publishers = append(publishers, call.Publishers...)
	}
	c.Calls.CallsMu.RUnlock()

	return publishers
}
