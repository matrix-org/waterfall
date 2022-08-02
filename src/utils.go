package main

import (
	"log"

	"github.com/pion/webrtc/v3"
)

func copyRemoteToLocal(trackRemote *webrtc.TrackRemote, trackLocal *webrtc.TrackLocalStaticRTP) {
	buff := make([]byte, 1500)
	for {
		i, _, err := trackRemote.Read(buff)
		if err != nil || buff == nil {
			log.Printf("ending read on track with StreamID %s: %s", trackRemote.StreamID(), err)
			break
		}

		if _, err = trackLocal.Write(buff[:i]); err != nil {
			log.Printf("ending write on track with StreamID %s: %s", trackLocal.StreamID(), err)
			break
		}
	}

}
