package conference

import (
	"encoding/json"
	"sync/atomic"

	"github.com/matrix-org/waterfall/pkg/peer"
	"github.com/matrix-org/waterfall/pkg/signaling"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// Things that we assume as identifiers for the participants in the call.
// There could be no 2 participants in the room with identical IDs.
type ParticipantID struct {
	UserID   id.UserID
	DeviceID id.DeviceID
	CallID   string
}

type PublishedTrack struct {
	track *webrtc.TrackLocalStaticRTP
	// The time when we sent the last PLI to the sender. We store this to avoid
	// spamming the sender.
	lastPLITimestamp atomic.Int64
}

// Participant represents a participant in the conference.
type Participant struct {
	id              ParticipantID
	logger          *logrus.Entry
	peer            *peer.Peer[ParticipantID]
	remoteSessionID id.SessionID
	streamMetadata  event.CallSDPStreamMetadata
	publishedTracks map[event.SFUTrackDescription]PublishedTrack
}

func (p *Participant) asMatrixRecipient() signaling.MatrixRecipient {
	return signaling.MatrixRecipient{
		UserID:          p.id.UserID,
		DeviceID:        p.id.DeviceID,
		CallID:          p.id.CallID,
		RemoteSessionID: p.remoteSessionID,
	}
}

func (p *Participant) sendDataChannelMessage(toSend event.SFUMessage) {
	jsonToSend, err := json.Marshal(toSend)
	if err != nil {
		p.logger.Error("Failed to marshal data channel message")
	}

	if err := p.peer.SendOverDataChannel(string(jsonToSend)); err != nil {
		// TODO: We must buffer the message in this case and re-send it once the data channel is recovered!
		p.logger.Error("Failed to send data channel message")
	}
}
