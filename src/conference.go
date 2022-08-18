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
	"log"
	"sync"

	"github.com/pion/webrtc/v3"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

var ErrNoSuchCall = errors.New("no such call")
var ErrFoundExistingCallWithSameSessionAndDeviceID = errors.New("found existing call with equal DeviceID and SessionID")

type LocalTrackInfo struct {
	StreamID string
	TrackID  string
	Call     *Call
}

type LocalTrackWithInfo struct {
	Track *webrtc.TrackLocalStaticRTP
	Info  LocalTrackInfo
}

type Calls struct {
	CallsMu sync.RWMutex
	Calls   map[string]*Call // By callID
}

type Tracks struct {
	Mutex  sync.RWMutex
	Tracks []LocalTrackWithInfo
}

type Metadata struct {
	Mutex    sync.RWMutex
	Metadata event.CallSDPStreamMetadata
}

type Conference struct {
	ConfID   string
	Calls    Calls
	Tracks   Tracks
	Metadata Metadata
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

func (c *Conference) getLocalTrackIndicesByInfo(selectInfo LocalTrackInfo) []int {
	c.Tracks.Mutex.Lock()
	defer c.Tracks.Mutex.Unlock()

	foundIndices := []int{}

	for index, track := range c.Tracks.Tracks {
		info := track.Info
		if selectInfo.Call != nil && selectInfo.Call != info.Call {
			continue
		}

		if selectInfo.StreamID != "" && selectInfo.StreamID != info.StreamID {
			continue
		}

		if selectInfo.TrackID != "" && selectInfo.TrackID != info.TrackID {
			continue
		}

		foundIndices = append(foundIndices, index)
	}

	return foundIndices
}

func (c *Conference) GetLocalTrackByInfo(selectInfo LocalTrackInfo) []webrtc.TrackLocal {
	indices := c.getLocalTrackIndicesByInfo(selectInfo)

	c.Tracks.Mutex.Lock()
	defer c.Tracks.Mutex.Unlock()

	foundTracks := []webrtc.TrackLocal{}
	for _, index := range indices {
		foundTracks = append(foundTracks, c.Tracks.Tracks[index].Track)
	}

	return foundTracks
}

func (c *Conference) RemoveTracksFromPeerConnectionsByInfo(removeInfo LocalTrackInfo) int {
	indices := c.getLocalTrackIndicesByInfo(removeInfo)

	c.Tracks.Mutex.Lock()
	defer c.Tracks.Mutex.Unlock()

	// FIXME: the big O of this must be awful...
	for _, index := range indices {
		info := c.Tracks.Tracks[index].Info

		for _, call := range c.Calls.Calls {
			for _, sender := range call.PeerConnection.GetSenders() {
				if info.TrackID == sender.Track().ID() {
					log.Printf(
						"%s | removing %s StreamID %s TrackID %s",
						call.UserID,
						sender.Track().Kind(),
						sender.Track().StreamID(),
						sender.Track().ID(),
					)

					if err := sender.Stop(); err != nil {
						log.Printf("%s | failed to stop sender: %s", call.UserID, err)
					}

					if err := call.PeerConnection.RemoveTrack(sender); err != nil {
						log.Printf("%s | failed to remove track: %s", call.UserID, err)
					}
				}
			}
		}
	}

	return len(indices)
}

func (c *Conference) RemoveTracksFromConfByInfo(removeInfo LocalTrackInfo) {
	indicesToRemove := c.getLocalTrackIndicesByInfo(removeInfo)

	c.Tracks.Mutex.Lock()
	defer c.Tracks.Mutex.Unlock()

	newTracks := []LocalTrackWithInfo{}

	for index, track := range c.Tracks.Tracks {
		keep := true

		for _, indexToRemove := range indicesToRemove {
			if indexToRemove == index {
				keep = false
			}
		}

		if keep {
			newTracks = append(newTracks, track)
		}
	}

	c.Tracks.Tracks = newTracks
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
	// First we copy the metadata
	metadata := make(event.CallSDPStreamMetadata)

	c.Metadata.Mutex.Lock()
	for streamID, info := range c.Metadata.Metadata {
		metadata[streamID] = info
	}
	c.Metadata.Mutex.Unlock()
	// Loop over the copied metadata
	for streamID, info := range metadata {
		// Delete metadata received from the device that we're sending metadata to
		if info.DeviceID == deviceID {
			delete(metadata, streamID)
			continue
		}
		// Loop over the tracks in the copied metadata
		for trackID := range info.Tracks {
			// Delete metadata, if we're the client hasn't published a track that is
			// included in the metadata yet
			if len(c.getLocalTrackIndicesByInfo(LocalTrackInfo{
				StreamID: streamID,
				TrackID:  trackID,
			})) == 0 {
				delete(metadata, streamID)
				break
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
		}
	}
}
