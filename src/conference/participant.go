package conference

import (
	"encoding/json"
	"errors"

	"github.com/matrix-org/waterfall/src/peer"
	"github.com/matrix-org/waterfall/src/signaling"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

var ErrInvalidSFUMessage = errors.New("invalid SFU message")

type Participant struct {
	id              peer.ID
	peer            *peer.Peer
	remoteSessionID id.SessionID
	streamMetadata  event.CallSDPStreamMetadata
	publishedTracks map[event.SFUTrackDescription]*webrtc.TrackLocalStaticRTP
}

func (p *Participant) asMatrixRecipient() signaling.MatrixRecipient {
	return signaling.MatrixRecipient{
		ID:              p.id,
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

func (c *Conference) getParticipant(peerID peer.ID, optionalErrorMessage error) *Participant {
	participant, ok := c.participants[peerID]
	if !ok {
		logEntry := c.logger.WithFields(logrus.Fields{
			"user_id":   peerID.UserID,
			"device_id": peerID.DeviceID,
		})

		if optionalErrorMessage != nil {
			logEntry.WithError(optionalErrorMessage)
		} else {
			logEntry.Error("Participant not found")
		}

		return nil
	}

	return participant
}

func (c *Conference) getStreamsMetadata(forParticipant peer.ID) event.CallSDPStreamMetadata {
	streamsMetadata := make(event.CallSDPStreamMetadata)
	for id, participant := range c.participants {
		if forParticipant != id {
			for streamID, metadata := range participant.streamMetadata {
				streamsMetadata[streamID] = metadata
			}
		}
	}

	return streamsMetadata
}

func (c *Conference) getTracks(identifiers []event.SFUTrackDescription) []*webrtc.TrackLocalStaticRTP {
	tracks := make([]*webrtc.TrackLocalStaticRTP, len(identifiers))
	for _, participant := range c.participants {
		// Check if this participant has any of the tracks that we're looking for.
		for _, identifier := range identifiers {
			if track, ok := participant.publishedTracks[identifier]; ok {
				tracks = append(tracks, track)
			}
		}
	}
	return tracks
}
