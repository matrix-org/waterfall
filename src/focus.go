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

	"maunium.net/go/mautrix/event"
)

type Confs struct {
	confsMu sync.RWMutex
	confs   map[string]*Conference
}

type Focus struct {
	name  string
	confs Confs
}

func (f *Focus) Init(name string) {
	f.name = name
	f.confs.confs = make(map[string]*Conference)
}

func (f *Focus) GetConf(confID string, create bool) (*Conference, error) {
	f.confs.confsMu.Lock()
	defer f.confs.confsMu.Unlock()
	co := f.confs.confs[confID]
	if co == nil {
		if create {
			co = &Conference{
				ConfID: confID,
			}
			f.confs.confs[confID] = co
			co.Calls.Calls = make(map[string]*Call)
			co.Tracks.Tracks = []LocalTrackWithInfo{}
			co.Metadata.Metadata = make(event.CallSDPStreamMetadata)
		} else {
			return nil, errors.New("no such conf")
		}
	}
	return co, nil
}
