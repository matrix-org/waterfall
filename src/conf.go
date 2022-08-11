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
	"maunium.net/go/mautrix/id"
)

type localTrackInfo struct {
	streamID string
	trackID  string
	call     *call
}

type localTrackWithInfo struct {
	track *webrtc.TrackLocalStaticRTP
	info  localTrackInfo
}

type calls struct {
	callsMu sync.RWMutex
	calls   map[string]*call // By callID
}

type tracks struct {
	mutex  sync.RWMutex
	tracks []localTrackWithInfo
}

type conf struct {
	confID string
	calls  calls
	tracks tracks
}

func (c *conf) getCall(callID string, create bool) (*call, error) {
	c.calls.callsMu.Lock()
	defer c.calls.callsMu.Unlock()
	ca := c.calls.calls[callID]
	if ca == nil {
		if create {
			ca = &call{
				callID: callID,
				conf:   c,
			}
			ca.subscribedTracks.tracks = []localTrackInfo{}
			c.calls.calls[callID] = ca
		} else {
			return nil, errors.New("no such call")
		}
	}
	return ca, nil
}

func (c *conf) getLocalTrackIndicesByInfo(selectInfo localTrackInfo) (tracks []int) {
	foundIndices := []int{}
	for index, track := range c.tracks.tracks {
		info := track.info
		if selectInfo.call != nil && selectInfo.call != info.call {
			continue
		}
		if selectInfo.streamID != "" && selectInfo.streamID != info.streamID {
			continue
		}
		if selectInfo.trackID != "" && selectInfo.trackID != info.trackID {
			continue
		}
		foundIndices = append(foundIndices, index)
	}

	return foundIndices
}

func (c *conf) getLocalTrackByInfo(selectInfo localTrackInfo) (tracks []webrtc.TrackLocal) {
	indices := c.getLocalTrackIndicesByInfo(selectInfo)
	foundTracks := []webrtc.TrackLocal{}
	for _, index := range indices {
		foundTracks = append(foundTracks, c.tracks.tracks[index].track)
	}

	return foundTracks
}

func (c *conf) removeTracksFromPeerConnectionsByInfo(removeInfo localTrackInfo) int {
	indices := c.getLocalTrackIndicesByInfo(removeInfo)

	// FIXME: the big O of this must be awful...
	for _, index := range indices {
		info := c.tracks.tracks[index].info

		for _, call := range c.calls.calls {
			for _, sender := range call.peerConnection.GetSenders() {
				if info.trackID == sender.Track().ID() {
					log.Printf("%s | removing %s track with StreamID %s", call.userID, sender.Track().Kind(), info.streamID)
					if err := sender.Stop(); err != nil {
						log.Printf("%s | failed to stop sender: %s", call.userID, err)
					}
					if err := call.peerConnection.RemoveTrack(sender); err != nil {
						log.Printf("%s | failed to remove track: %s", call.userID, err)
					}
				}
			}
		}
	}

	return len(indices)
}

func (c *conf) removeTracksFromConfByInfo(removeInfo localTrackInfo) {
	c.tracks.mutex.Lock()
	defer c.tracks.mutex.Unlock()

	indicesToRemove := c.getLocalTrackIndicesByInfo(removeInfo)

	newTracks := []localTrackWithInfo{}
	for index, track := range c.tracks.tracks {
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

	c.tracks.tracks = newTracks
}

func (c *conf) removeOldCallsByDeviceAndSessionIds(deviceID id.DeviceID, sessionID id.SessionID) error {
	var err error
	for _, call := range c.calls.calls {
		if call.deviceID == deviceID {
			if call.remoteSessionID == sessionID {
				err = errors.New("found existing call with equal DeviceID and SessionID")
			} else {
				call.terminate()
			}
		}
	}
	return err
}
