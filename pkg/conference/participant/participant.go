package participant

import (
	"github.com/matrix-org/waterfall/pkg/peer"
	"github.com/matrix-org/waterfall/pkg/signaling"
	"github.com/matrix-org/waterfall/pkg/telemetry"
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// Things that we assume as identifiers for the participants in the call.
// There could be no 2 participants in the room with identical IDs.
type ID struct {
	UserID   id.UserID
	DeviceID id.DeviceID
	CallID   string
}

func (id ID) String() string {
	return string(id.UserID) + "/" + string(id.DeviceID)
}

// Participant represents a participant in the conference.
type Participant struct {
	ID              ID
	Peer            *peer.Peer[ID]
	RemoteSessionID id.SessionID
	Pong            chan<- Pong

	Logger    *logrus.Entry
	Telemetry *telemetry.Telemetry
}

func (p *Participant) AsMatrixRecipient() signaling.MatrixRecipient {
	return signaling.MatrixRecipient{
		UserID:          p.ID.UserID,
		DeviceID:        p.ID.DeviceID,
		CallID:          p.ID.CallID,
		RemoteSessionID: p.RemoteSessionID,
	}
}

func (p *Participant) SendDataChannelMessage(toSend event.Event) error {
	jsonToSend, err := toSend.MarshalJSON()
	if err != nil {
		p.Logger.Errorf("Failed to marshal data channel message: %s", err)
		return err
	}

	if err := p.Peer.SendOverDataChannel(string(jsonToSend)); err != nil {
		p.Logger.Errorf("Failed to send data channel message: %s", err)
		return err
	}

	return nil
}
