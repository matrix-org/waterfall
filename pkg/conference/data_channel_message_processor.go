package conference

import (
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
	"golang.org/x/exp/slices"
	"maunium.net/go/mautrix/event"
)

// Handle the `FocusEvent` from the DataChannel message.
func (c *Conference) processTrackSubscriptionDCMessage(
	participant *Participant, msg event.FocusCallTrackSubscriptionEventContent,
) {
	participant.logger.Info("Received track subscription request over DC")

	// Find tracks based on what we were asked for.
	tracks := c.getTracks(msg.Subscribe)

	participant.logger.WithFields(logrus.Fields{
		"tracks_we_got":  tracks,
		"tracks_we_want": msg,
	}).Debug("Tracks to subscribe to")

	// Let's check if we have all the tracks that we were asked for are there.
	// If not, we will list which are not available (later on we must inform participant
	// about it unless the participant retries it).
	if len(tracks) != len(msg.Subscribe) {
		for _, expected := range msg.Subscribe {
			found := slices.IndexFunc(tracks, func(track *webrtc.TrackLocalStaticRTP) bool {
				return track.ID() == expected.TrackID
			})

			if found == -1 {
				c.logger.Warnf("Track not found: %s", expected.TrackID)
			}
		}
	}

	// Subscribe to the found tracks
	for _, track := range tracks {
		participant.logger.WithField("track_id", track.ID()).Debug("Subscribing to track")
		if err := participant.peer.SubscribeTo(track); err != nil {
			participant.logger.Errorf("Failed to subscribe to track: %v", err)
			return
		}
	}

	// TODO: Handle unsubscribe
}

func (c *Conference) processNegotiateDCMessage(participant *Participant, msg event.FocusCallNegotiateEventContent) {
	participant.streamMetadata = msg.SDPStreamMetadata

	if msg.Description.Type == event.CallDataTypeOffer {
		participant.logger.WithField("SDP", msg.Description.SDP).Trace("Received SDP offer over DC")

		answer, err := participant.peer.ProcessSDPOffer(msg.Description.SDP)
		if err != nil {
			participant.logger.Errorf("Failed to set SDP offer: %v", err)
			return
		}

		participant.sendDataChannelMessage(event.Event{
			Type: event.FocusCallNegotiate,
			Content: event.Content{
				Parsed: event.FocusCallNegotiateEventContent{
					Description: event.CallData{
						Type: event.CallDataType(answer.Type.String()),
						SDP:  answer.SDP,
					},
					SDPStreamMetadata: c.getAvailableStreamsFor(participant.id),
				},
			},
		})
	} else if msg.Description.Type == event.CallDataTypeAnswer {
		participant.logger.WithField("SDP", msg.Description.SDP).Trace("Received SDP answer over DC")

		if err := participant.peer.ProcessSDPAnswer(msg.Description.SDP); err != nil {
			participant.logger.Errorf("Failed to set SDP answer: %v", err)
			return
		}
	} else {
		participant.logger.Errorf("Unknown SDP description type")
	}
}

func (c *Conference) processPongDCMessage(participant *Participant) {
	participant.peer.ProcessPong()
}

func (c *Conference) processMetadataDCMessage(
	participant *Participant, msg event.FocusCallSDPStreamMetadataChangedEventContent,
) {
	participant.streamMetadata = msg.SDPStreamMetadata
	c.resendMetadataToAllExcept(participant.id)
}
