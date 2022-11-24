package conference

import (
	"encoding/json"
	"errors"

	"github.com/matrix-org/waterfall/pkg/peer"
	"github.com/matrix-org/waterfall/pkg/signaling"
	"github.com/pion/webrtc/v3"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

var ErrInvalidSFUMessage = errors.New("invalid SFU message")

// Things that we assume as identifiers for the participants in the call.
// There could be no 2 participants in the room with identical IDs.
type ParticipantID struct {
	UserID   id.UserID
	DeviceID id.DeviceID
}

// Participant represents a participant in the conference.
type Participant struct {
	id              ParticipantID
	peer            *peer.Peer[ParticipantID]
	remoteSessionID id.SessionID
	streamMetadata  event.CallSDPStreamMetadata
	publishedTracks map[event.SFUTrackDescription]*webrtc.TrackLocalStaticRTP
}

func (p *Participant) asMatrixRecipient() signaling.MatrixRecipient {
	return signaling.MatrixRecipient{
		UserID:          p.id.UserID,
		DeviceID:        p.id.DeviceID,
		RemoteSessionID: p.remoteSessionID,
	}
}

func (p *Participant) sendDataChannelMessage(toSend event.SFUMessage) error {
	jsonToSend, err := json.Marshal(toSend)
	if err != nil {
		return ErrInvalidSFUMessage
	}

	if err := p.peer.SendOverDataChannel(string(jsonToSend)); err != nil {
		// FIXME: We must buffer the message in this case and re-send it once the data channel is recovered!
		//        Or use Matrix signaling to inform the peer about the problem.
		return err
	}

	return nil
}
