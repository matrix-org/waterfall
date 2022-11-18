package peer

import (
	"github.com/pion/webrtc/v3"
)

type MessageChannel chan interface{}

type PeerJoinedTheCall struct {
	Sender ID
}

type PeerLeftTheCall struct {
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

type NewOffer struct {
	Sender ID
	Offer  *webrtc.SessionDescription
}

type DataChannelOpened struct {
	Sender ID
}

type DataChannelClosed struct {
	Sender ID
}

type DataChannelMessage struct {
	Sender  ID
	Message string
}

type DataChannelError struct {
	Sender ID
	Err    error
}
