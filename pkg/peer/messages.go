package peer

import (
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
	"maunium.net/go/mautrix/event"
)

// Due to the limitation of Go, we're using the `interface{}` to be able to use switch the actual
// type of the message on runtime. The underlying types do not necessary need to be structures.
type MessageContent = interface{}

type JoinedTheCall struct{}

type LeftTheCall struct {
	Reason event.CallHangupReason
}

type NewTrackPublished struct {
	ExtendedTrackInfo
}

type PublishedTrackFailed struct {
	ExtendedTrackInfo
}

type RTPPacketReceived struct {
	ExtendedTrackInfo
	Packet *rtp.Packet
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

type RTCPReceived struct {
	TrackID string
	Packets []RTCPPacket
}

type RTCPPacket struct {
	Type    RTCPPacketType
	Content rtcp.Packet
}
