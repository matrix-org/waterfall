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
	"strings"
	"time"

	"github.com/pion/randutil"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
)

const pliInterval = 200
const idLength = 32
const idRunes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

func GenerateID() (string, error) {
	return randutil.GenerateCryptoRandomString(idLength, idRunes)
}

func WriteRTCP(
	trackRemote *webrtc.TrackRemote,
	peerConnection *webrtc.PeerConnection,
	trackLogger *logrus.Entry,
) {
	if !strings.Contains(trackRemote.Codec().MimeType, "video") {
		return
	}

	// FIXME: This is a potential performance killer. This can be less wasteful
	// by processing incoming RTCP events, then we would emit a NACK/PLI when a
	// viewer requests it
	// Send a PLI on an interval so that the publisher is pushing a keyframe
	// every 200ms
	ticker := time.NewTicker(time.Millisecond * pliInterval)
	for range ticker.C {
		err := peerConnection.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(trackRemote.SSRC())}})
		if err != nil {
			if !errors.Is(err, io.ErrClosedPipe) {
				trackLogger.WithError(err).Warn("ending RTCP write on track")
			}

			break
		}
	}
}
