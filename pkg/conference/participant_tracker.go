package conference

import (
	"time"

	"github.com/matrix-org/waterfall/pkg/peer"
	"github.com/pion/rtp"
	"github.com/thoas/go-funk"
)

type (
	Subscribers      map[ParticipantID]*peer.Subscription
	TrackSubscribers map[TrackID]Subscribers
)

// Tracks participants and their corresponding tracks.
// These are grouped together as the field in this structure must be kept synchronized.
type ParticipantTracker struct {
	participants    map[ParticipantID]*Participant
	subscribers     TrackSubscribers
	publishedTracks map[TrackID]PublishedTrack
}

func NewParticipantTracker() *ParticipantTracker {
	return &ParticipantTracker{
		participants:    make(map[ParticipantID]*Participant),
		subscribers:     make(TrackSubscribers),
		publishedTracks: make(map[TrackID]PublishedTrack),
	}
}

func (t *ParticipantTracker) addParticipant(participant *Participant) {
	t.participants[participant.id] = participant
}

func (t *ParticipantTracker) getParticipant(participantID ParticipantID) *Participant {
	return t.participants[participantID]
}

// Removes the participant from the conference closing all its tracks. Returns a set of **streams** that are to be
// removed. The return type is odd since Go does not natively support sets, so we emulate it with a map.
func (t *ParticipantTracker) removeParticipant(participantID ParticipantID) map[string]bool {
	participant := t.getParticipant(participantID)
	if participant == nil {
		return make(map[string]bool)
	}

	// Terminate the participant and remove it from the list.
	participant.peer.Terminate()
	close(participant.heartbeatPong)
	delete(t.participants, participantID)

	// Remove the participant's tracks from all participants who might have subscribed to them.
	streamIdentifiers := make(map[string]bool)
	for trackID, track := range t.publishedTracks {
		if track.owner == participantID {
			// Odd way to add to a set in Go.
			streamIdentifiers[track.info.StreamID] = true
			t.removeTrack(trackID)
		}
	}

	return streamIdentifiers
}

func (t *ParticipantTracker) addTrack(
	participantID ParticipantID,
	info peer.ExtendedTrackInfo,
	metadata TrackMetadata,
) {
	// If this is a new track, let's add it to the list of published and inform participants.
	track, found := t.publishedTracks[info.TrackID]
	if !found {
		layers := []peer.SimulcastLayer{}
		if info.Layer != peer.SimulcastLayerNone {
			layers = append(layers, info.Layer)
		}

		t.publishedTracks[info.TrackID] = PublishedTrack{
			owner:    participantID,
			info:     info.TrackInfo,
			layers:   layers,
			metadata: metadata,
		}
	}

	// If it's just a new layer, let's add it to the list of layers of the existing published track.
	if info.Layer != peer.SimulcastLayerNone && !funk.Contains(track.layers, info.Layer) {
		track.layers = append(track.layers, info.Layer)
		t.publishedTracks[info.TrackID] = track
	}
}

func (t *ParticipantTracker) removeTrack(id TrackID) {
	subscribers := t.subscribers[id]

	// Iterate over all subscriptions and end them.
	for subscriberID, subscription := range subscribers {
		subscription.Unsubscribe()
		delete(subscribers, subscriberID)
	}

	delete(t.subscribers, id)
}

func (t *ParticipantTracker) processRTP(info peer.ExtendedTrackInfo, packet *rtp.Packet) {
	for _, subscription := range t.subscribers[info.TrackID] {
		// We can't compare 2 structs in Go in many cases without using slow reflection,
		// so we compare the relevant fields manually.
		subscriptionInfo := subscription.TrackInfo()
		if subscriptionInfo.TrackID == info.TrackID && subscriptionInfo.Layer == info.Layer {
			subscription.WriteRTP(packet)
		}
	}
}

func (t *ParticipantTracker) processRTCP(participant *Participant, trackID TrackID, packets []peer.RTCPPacket) {
	const sendKeyFrameInterval = 500 * time.Millisecond

	if published, found := t.publishedTracks[trackID]; found {
		if published.canSendKeyframeAt.Before(time.Now()) {
			if err := participant.peer.WriteRTCP(trackID, packets); err == nil {
				published.canSendKeyframeAt = time.Now().Add(sendKeyFrameInterval)
			}
		}
	}
}

func (t *ParticipantTracker) getSubscribers(id TrackID) Subscribers {
	return t.subscribers[id]
}

func (t *ParticipantTracker) Subscribe(participantID ParticipantID, tracks []peer.ExtendedTrackInfo) {
	if participant := t.getParticipant(participantID); participant != nil {
		for _, track := range tracks {
			subscription := participant.peer.SubscribeTo(track)
			if subscription == nil {
				continue
			}

			_, firstSubscriber := t.subscribers[track.TrackID]

			if firstSubscriber {
				t.subscribers[track.TrackID] = make(Subscribers)
			}

			// Sanity check.
			subscribers := t.subscribers[track.TrackID]
			if _, ok := subscribers[participantID]; ok {
				participant.logger.Errorf("Bug: already subsribed to %s!", track.TrackID)
			}

			subscribers[participantID] = subscription
		}
	}
}

func (t *ParticipantTracker) Unsubscribe(participantID ParticipantID, tracks []TrackID) {
	for _, trackID := range tracks {
		if subscribers, ok := t.subscribers[trackID]; ok {
			if subscription := subscribers[participantID]; subscription != nil {
				subscription.Unsubscribe()
				delete(subscribers, participantID)
			}
		}
	}
}
