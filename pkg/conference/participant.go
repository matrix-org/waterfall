package conference

import (
	"fmt"
	"time"

	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/matrix-org/waterfall/pkg/peer"
	"github.com/matrix-org/waterfall/pkg/signaling"
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

// Represents a track that a peer has published (has already started sending to the SFU).
type PublishedTrack struct {
	// Info about the track.
	info peer.TrackInfo
	// Available simulcast layers.
	layers []peer.SimulcastLayer
	// The timestamp at which we are allowed to send the FIR or PLI request. We don't want to send them
	// too often, so we introduce some trivial rate limiting to not "enforce" too many key frames.
	canSendKeyframeAt time.Time
}

// Participant represents a participant in the conference.
type Participant struct {
	id              ParticipantID
	logger          *logrus.Entry
	peer            *peer.Peer[ParticipantID]
	remoteSessionID id.SessionID
	streamMetadata  event.CallSDPStreamMetadata
	publishedTracks map[string]PublishedTrack
	heartbeatPong   chan<- common.Pong
}

func (p *Participant) asMatrixRecipient() signaling.MatrixRecipient {
	return signaling.MatrixRecipient{
		UserID:          p.id.UserID,
		DeviceID:        p.id.DeviceID,
		CallID:          p.id.CallID,
		RemoteSessionID: p.remoteSessionID,
	}
}

func (p *Participant) sendDataChannelMessage(toSend event.Event) error {
	jsonToSend, err := toSend.MarshalJSON()
	if err != nil {
		return fmt.Errorf("Failed to marshal data channel message: %w", err)
	}

	if err := p.peer.SendOverDataChannel(string(jsonToSend)); err != nil {
		// TODO: We must buffer the message in this case and re-send it once the data channel is recovered!
		return fmt.Errorf("Failed to send data channel message: %w", err)
	}

	return nil
}
