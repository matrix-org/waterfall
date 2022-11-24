package peer

import (
	"github.com/pion/webrtc/v3"
)

type MessageContent = interface{}

type JoinedTheCall struct{}

type LeftTheCall struct{}

type NewTrackPublished struct {
	Track *webrtc.TrackLocalStaticRTP
}

type PublishedTrackFailed struct {
	Track *webrtc.TrackLocalStaticRTP
}

type NewICECandidate struct {
	Candidate *webrtc.ICECandidate
}

type ICEGatheringComplete struct{}

type RenegotiationRequired struct {
	Offer *webrtc.SessionDescription
}

type DataChannelMessage struct {
	Message string
}

type DataChannelAvailable struct{}
