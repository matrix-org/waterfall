package peer

import (
	"github.com/pion/webrtc/v3"
)

type Message = interface{}

type JoinedTheCall struct {
	Sender ID
}

type LeftTheCall struct {
	Sender ID
}

type NewTrackPublished struct {
	Sender ID
	Track  *webrtc.TrackLocalStaticRTP
}

type PublishedTrackFailed struct {
	Sender ID
	Track  *webrtc.TrackLocalStaticRTP
}

type NewICECandidate struct {
	Sender    ID
	Candidate *webrtc.ICECandidate
}

type ICEGatheringComplete struct {
	Sender ID
}

type RenegotiationRequired struct {
	Sender ID
	Offer  *webrtc.SessionDescription
}

type DataChannelMessage struct {
	Sender  ID
	Message string
}

type DataChannelAvailable struct {
	Sender ID
}
