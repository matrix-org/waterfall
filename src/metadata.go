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

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type Metadata struct {
	mutex      sync.RWMutex
	data       event.CallSDPStreamMetadata
	conference *Conference
}

func NewMetadata(conference *Conference) *Metadata {
	metadata := new(Metadata)

	metadata.data = make(event.CallSDPStreamMetadata)
	metadata.conference = conference

	return metadata
}

func (m *Metadata) Update(deviceID id.DeviceID, metadata event.CallSDPStreamMetadata) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Update existing and add new
	for streamID, info := range metadata {
		m.data[streamID] = info
	}
	// Remove removed
	for streamID, info := range m.data {
		_, exists := metadata[streamID]
		if info.DeviceID == deviceID && !exists {
			delete(m.data, streamID)
		}
	}
}

func (m *Metadata) RemoveByDevice(deviceID id.DeviceID) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	for streamID, info := range m.data {
		if info.DeviceID == deviceID {
			delete(m.data, streamID)
		}
	}
}

// Get metadata to send to deviceID. This will not include the device's own
// metadata and metadata which includes tracks which we have not received yet.
func (m *Metadata) GetForDevice(deviceID id.DeviceID) event.CallSDPStreamMetadata {
	metadata := make(event.CallSDPStreamMetadata)

	m.mutex.RLock()
	defer m.mutex.RUnlock()

	for _, publisher := range m.conference.GetPublishers() {
		if deviceID == publisher.Call.DeviceID {
			continue
		}

		streamID := publisher.Track.StreamID()
		trackID := publisher.Track.ID()

		info, exists := metadata[streamID]
		if exists {
			info.Tracks[publisher.Track.ID()] = event.CallSDPStreamMetadataTrack{}
			metadata[streamID] = info
		} else {
			metadata[streamID] = event.CallSDPStreamMetadataObject{
				UserID:     publisher.Call.UserID,
				DeviceID:   publisher.Call.DeviceID,
				Purpose:    m.data[streamID].Purpose,
				AudioMuted: m.data[streamID].AudioMuted,
				VideoMuted: m.data[streamID].VideoMuted,
				Tracks: event.CallSDPStreamMetadataTracks{
					trackID: {},
				},
			}
		}
	}

	return metadata
}
