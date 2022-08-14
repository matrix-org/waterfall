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
	"log"
	"strings"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
)

func CopyRemoteToLocal(trackRemote *webrtc.TrackRemote, trackLocal *webrtc.TrackLocalStaticRTP) {
	buff := make([]byte, 1500)
	for {
		i, _, err := trackRemote.Read(buff)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.Printf("failed read on StreamID %s TrackID %s: %s", trackLocal.StreamID(), trackRemote.ID(), err)
			}
			break
		}

		if _, err = trackLocal.Write(buff[:i]); err != nil {
			if !errors.Is(err, io.ErrClosedPipe) {
				log.Printf("failed write on StreamID %s TrackID %s: %s", trackLocal.StreamID(), trackLocal.ID(), err)
			}
			break
		}
	}
}

func WriteRTCP(trackRemote *webrtc.TrackRemote, peerConnection *webrtc.PeerConnection) {
	if !strings.Contains(trackRemote.Codec().MimeType, "video") {
		return
	}

	// FIXME: This is a potential performance killer. This can be less wasteful
	// by processing incoming RTCP events, then we would emit a NACK/PLI when a
	// viewer requests it
	// Send a PLI on an interval so that the publisher is pushing a keyframe
	// every 200ms
	ticker := time.NewTicker(time.Millisecond * 200)
	for range ticker.C {
		err := peerConnection.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(trackRemote.SSRC())}})
		if err != nil && !errors.Is(err, io.ErrClosedPipe) {
			log.Printf("ending RTCP write on TrackID %s: %s", trackRemote.ID(), err)
			break
		}
	}
}
