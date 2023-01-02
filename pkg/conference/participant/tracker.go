package participant

import (
	"fmt"
	"time"

	"github.com/matrix-org/waterfall/pkg/peer"
	"github.com/pion/rtp"
	"github.com/thoas/go-funk"
)

type (
	Subscriptions    map[ID]*peer.Subscription
	TrackSubscribers map[TrackID]Subscriptions
)

// Tracks participants and their corresponding tracks.
// These are grouped together as the field in this structure must be kept synchronized.
type Tracker struct {
	participants    map[ID]*Participant
	subscribers     TrackSubscribers
	publishedTracks map[TrackID]PublishedTrack
}

func NewParticipantTracker() *Tracker {
	return &Tracker{
		participants:    make(map[ID]*Participant),
		subscribers:     make(TrackSubscribers),
		publishedTracks: make(map[TrackID]PublishedTrack),
	}
}

// Adds a new participant in the list.
func (t *Tracker) AddParticipant(participant *Participant) {
	t.participants[participant.ID] = participant
}

// Gets an existing participant if any.
func (t *Tracker) GetParticipant(participantID ID) *Participant {
	return t.participants[participantID]
}

func (t *Tracker) HasParticipants() bool {
	return len(t.participants) != 0
}

// Iterates over participants and calls a closure on each of the participants.
func (t *Tracker) ForEachParticipant(fn func(ID, *Participant)) {
	for id, participant := range t.participants {
		fn(id, participant)
	}
}

// Removes the participant from the conference closing all its tracks. Returns a set of **streams** that are to be
// removed. The return type is odd since Go does not natively support sets, so we emulate it with a map.
func (t *Tracker) RemoveParticipant(participantID ID) map[string]bool {
	participant := t.GetParticipant(participantID)
	if participant == nil {
		return make(map[string]bool)
	}

	// Terminate the participant and remove it from the list.
	participant.Peer.Terminate()
	close(participant.HeartbeatPong)
	delete(t.participants, participantID)

	// Remove the participant's tracks from all participants who might have subscribed to them.
	streamIdentifiers := make(map[string]bool)
	for trackID, track := range t.publishedTracks {
		if track.Owner == participantID {
			// Odd way to add to a set in Go.
			streamIdentifiers[track.Info.StreamID] = true
			t.RemoveTrack(trackID)
		}
	}

	return streamIdentifiers
}

// Adds a new track to the list of published tracks, i.e. by calling it we inform the tracker that there is new track
// that has been published and that we must take into account from now on.
func (t *Tracker) AddTrack(
	participantID ID,
	info peer.TrackInfo,
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
			Owner:    participantID,
			Info:     info,
			Layers:   layers,
			Metadata: metadata,
		}

		return
	}

	// If it's just a new layer, let's add it to the list of layers of the existing published track.
	if info.Layer != peer.SimulcastLayerNone && !funk.Contains(track.Layers, info.Layer) {
		track.Layers = append(track.Layers, info.Layer)
		t.publishedTracks[info.TrackID] = track
	}
}

// Finds a track by the track ID.
func (t *Tracker) FindTrack(id TrackID) *PublishedTrack {
	if track, found := t.publishedTracks[id]; found {
		return &track
	}

	return nil
}

// Iterates over published tracks and calls a closure upon each track.
func (t *Tracker) ForEachTrack(fn func(TrackID, PublishedTrack)) {
	for id, track := range t.publishedTracks {
		fn(id, track)
	}
}

// Updates metadata associated with a given track.
func (t *Tracker) UpdateTrackMetadata(id TrackID, metadata TrackMetadata) {
	if track, found := t.publishedTracks[id]; found {
		track.Metadata = metadata
		t.publishedTracks[id] = track
	}
}

// Informs the tracker that one of the previously published tracks is gone.
func (t *Tracker) RemoveTrack(id TrackID) {
	subscribers := t.subscribers[id]

	// Iterate over all subscriptions and end them.
	for subscriberID, subscription := range subscribers {
		subscription.Unsubscribe()
		delete(subscribers, subscriberID)
	}

	delete(t.subscribers, id)
}

// Subscribes a given participant to the tracks that are passed as a parameter.
func (t *Tracker) Subscribe(participantID ID, tracks []peer.TrackInfo) {
	if participant := t.GetParticipant(participantID); participant != nil {
		for _, track := range tracks {
			subscription := participant.Peer.SubscribeTo(track)
			if subscription == nil {
				continue
			}

			// If we're a first subscriber, we need to initialize the list of subscribers.
			// Otherwise it will panic (Go specifics when working with maps).
			if _, found := t.subscribers[track.TrackID]; !found {
				t.subscribers[track.TrackID] = make(Subscriptions)
			}

			// Sanity check.
			subscribers := t.subscribers[track.TrackID]
			if _, ok := subscribers[participantID]; ok {
				participant.Logger.Errorf("Bug: already subsribed to %s!", track.TrackID)
			}

			subscribers[participantID] = subscription
		}
	}
}

// Returns a subscription that corresponds to the `participantID` subscriber for the `trackID`. If no such participant
// is subscribed to a track or no such track exists, `nil` would be returned.
func (t *Tracker) GetSubscriber(trackID TrackID, participantID ID) *peer.Subscription {
	if subscribers, found := t.subscribers[trackID]; found {
		return subscribers[participantID]
	}

	return nil
}

// Unsubscribes a given `participantID` from the given tracks.
func (t *Tracker) Unsubscribe(participantID ID, tracks []TrackID) {
	for _, trackID := range tracks {
		if subscribers, ok := t.subscribers[trackID]; ok {
			if subscription := subscribers[participantID]; subscription != nil {
				subscription.Unsubscribe()
				delete(subscribers, participantID)
			}
		}
	}
}

// Processes an RTP packet received on a given track.
func (t *Tracker) ProcessRTP(info peer.TrackInfo, packet *rtp.Packet) {
	for _, subscription := range t.subscribers[info.TrackID] {
		if subscription.TrackInfo().Layer == info.Layer {
			subscription.WriteRTP(packet)
		}
	}
}

// Processes RTCP packets received on a given track.
func (t *Tracker) ProcessRTCP(info peer.TrackInfo, packets []peer.RTCPPacket) error {
	const sendKeyFrameInterval = 500 * time.Millisecond

	published, found := t.publishedTracks[info.TrackID]
	if !found {
		return fmt.Errorf("no such track: %s", info.TrackID)
	}

	participant := t.GetParticipant(published.Owner)
	if participant == nil {
		return fmt.Errorf("no such participant: %s", published.Owner)
	}

	// We don't want to send keyframes too often, so we'll send them only once in a while.
	if published.canSendKeyframeAt.Before(time.Now()) {
		if err := participant.Peer.WriteRTCP(info, packets); err != nil {
			return err
		}

		published.canSendKeyframeAt = time.Now().Add(sendKeyFrameInterval)
		t.publishedTracks[info.TrackID] = published
	}

	return nil
}
