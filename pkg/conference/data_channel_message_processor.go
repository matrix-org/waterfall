package conference

import (
	"github.com/pion/webrtc/v3"
	"golang.org/x/exp/slices"
	"maunium.net/go/mautrix/event"
)

// Handle the `SFUMessage` event from the DataChannel message.
func (c *Conference) processSelectDCMessage(participant *Participant, msg event.SFUMessage) {
	participant.logger.Info("Received select request over DC")

	// Find tracks based on what we were asked for.
	tracks := c.getTracks(msg.Start)

	// Let's check if we have all the tracks that we were asked for are there.
	// If not, we will list which are not available (later on we must inform participant
	// about it unless the participant retries it).
	if len(tracks) != len(msg.Start) {
		for _, expected := range msg.Start {
			found := slices.IndexFunc(tracks, func(track *webrtc.TrackLocalStaticRTP) bool {
				return track.ID() == expected.TrackID
			})

			if found == -1 {
				c.logger.Warnf("Track not found: %s", expected.TrackID)
			}
		}
	}

	// Subscribe to the found tracks.
	for _, track := range tracks {
		if err := participant.peer.SubscribeTo(track); err != nil {
			participant.logger.Errorf("Failed to subscribe to track: %v", err)
			return
		}
	}
}

func (c *Conference) processAnswerDCMessage(participant *Participant, msg event.SFUMessage) {
	participant.logger.Info("Received SDP answer over DC")

	if err := participant.peer.ProcessSDPAnswer(msg.SDP); err != nil {
		participant.logger.Errorf("Failed to set SDP answer: %v", err)
		return
	}
}

func (c *Conference) processPublishDCMessage(participant *Participant, msg event.SFUMessage) {
	participant.logger.Info("Received SDP offer over DC")

	answer, err := participant.peer.ProcessSDPOffer(msg.SDP)
	if err != nil {
		participant.logger.Errorf("Failed to set SDP offer: %v", err)
		return
	}

	participant.streamMetadata = msg.Metadata

	participant.sendDataChannelMessage(event.SFUMessage{
		Op:       event.SFUOperationAnswer,
		SDP:      answer.SDP,
		Metadata: c.getAvailableStreamsFor(participant.id),
	})
}

func (c *Conference) processAliveDCMessage(participant *Participant) {
	participant.peer.ProcessHeartbeat()
}

func (c *Conference) processMetadataDCMessage(participant *Participant, msg event.SFUMessage) {
	participant.streamMetadata = msg.Metadata
	c.resendMetadataToAllExcept(participant.id)
}
