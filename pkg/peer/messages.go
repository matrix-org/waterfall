package peer

import (
	"github.com/matrix-org/waterfall/pkg/common"
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
	// Information about the track (ID etc).
	common.TrackInfo
	// SimulcastLayer configuration (can be `None` for non-simulcast tracks and for audio tracks).
	SimulcastLayer common.SimulcastLayer
	// Output track (if any) that could be used to send data to the peer. Will be `nil` if such
	// track does not exist, in which case the caller is expected to listen to `RtpPacketReceived`
	// messages.
	OutputTrack *webrtc.TrackLocalStaticRTP
}

type PublishedTrackFailed struct {
	common.TrackInfo
	SimulcastLayer common.SimulcastLayer
}

type RTPPacketReceived struct {
	common.TrackInfo
	SimulcastLayer common.SimulcastLayer
	Packet         *rtp.Packet
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

type KeyFrameRequestReceived struct {
	common.TrackInfo
	SimulcastLayer common.SimulcastLayer
}
