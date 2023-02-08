package participant

import (
	"github.com/matrix-org/waterfall/pkg/conference/subscription"
	"github.com/matrix-org/waterfall/pkg/webrtc_ext"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
	"golang.org/x/exp/slices"
)

// Tracks participants and their corresponding tracks.
// These are grouped together as the field in this structure must be kept synchronized.
type Tracker struct {
	participants    map[ID]*Participant
	publishedTracks map[TrackID]*PublishedTrack
}

func NewParticipantTracker() *Tracker {
	return &Tracker{
		participants:    make(map[ID]*Participant),
		publishedTracks: make(map[TrackID]*PublishedTrack),
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
	close(participant.Pong)
	delete(t.participants, participantID)

	// Remove the participant's tracks from all participants who might have subscribed to them.
	streamIdentifiers := make(map[string]bool)
	for trackID, track := range t.publishedTracks {
		if track.Owner == participantID {
			// Odd way to add to a set in Go.
			streamIdentifiers[track.Info.StreamID] = true
			t.RemovePublishedTrack(trackID)
		}
	}

	// Go over all subscriptions and remove the participant from them.
	// TODO: Perhaps we could simply react to the subscrpitions dying and remove them from the list.
	for _, publishedTrack := range t.publishedTracks {
		if subscription, found := publishedTrack.Subscriptions[participantID]; found {
			subscription.Unsubscribe()
			delete(publishedTrack.Subscriptions, participantID)
		}
	}

	return streamIdentifiers
}

// Adds a new track to the list of published tracks, i.e. by calling it we inform the tracker that there is new track
// that has been published and that we must take into account from now on.
func (t *Tracker) AddPublishedTrack(
	participantID ID,
	info webrtc_ext.TrackInfo,
	simulcast webrtc_ext.SimulcastLayer,
	metadata TrackMetadata,
	outputTrack *webrtc.TrackLocalStaticRTP,
) {
	// If this is a new track, let's add it to the list of published and inform participants.
	track, found := t.publishedTracks[info.TrackID]
	if !found {
		layers := []webrtc_ext.SimulcastLayer{}
		if simulcast != webrtc_ext.SimulcastLayerNone {
			layers = append(layers, simulcast)
		}

		t.publishedTracks[info.TrackID] = &PublishedTrack{
			Owner:       participantID,
			Info:        info,
			Layers:      layers,
			Metadata:    metadata,
			OutputTrack: outputTrack,
			Subscriptions: make(map[ID]subscription.Subscription),
		}

		return
	}

	// If it's just a new layer, let's add it to the list of layers of the existing published track.
	fn := func(layer webrtc_ext.SimulcastLayer) bool { return layer == simulcast }
	if simulcast != webrtc_ext.SimulcastLayerNone && slices.IndexFunc(track.Layers, fn) == -1 {
		track.Layers = append(track.Layers, simulcast)
		t.publishedTracks[info.TrackID] = track
	}
}

// Finds a track by the track ID.
func (t *Tracker) FindPublishedTrack(id TrackID) *PublishedTrack {
	if track, found := t.publishedTracks[id]; found {
		return track
	}

	return nil
}

// Iterates over published tracks and calls a closure upon each track.
func (t *Tracker) ForEachPublishedTrack(fn func(TrackID, *PublishedTrack)) {
	for id, track := range t.publishedTracks {
		fn(id, track)
	}
}

// Updates metadata associated with a given track.
func (t *Tracker) UpdatePublishedTrackMetadata(id TrackID, metadata TrackMetadata) {
	if track, found := t.publishedTracks[id]; found {
		track.Metadata = metadata
		t.publishedTracks[id] = track
	}
}

// Informs the tracker that one of the previously published tracks is gone.
func (t *Tracker) RemovePublishedTrack(id TrackID) {
	if publishedTrack, found := t.publishedTracks[id]; found {
		// Iterate over all subscriptions and end them.
		for subscriberID, subscription := range publishedTrack.Subscriptions {
			subscription.Unsubscribe()
			delete(publishedTrack.Subscriptions, subscriberID)
		}

		delete(t.publishedTracks, id)
	}
}

type SubscribeRequest struct {
	webrtc_ext.TrackInfo
	Simulcast webrtc_ext.SimulcastLayer
}

// Subscribes a given participant to the tracks that are passed as a parameter.
func (t *Tracker) Subscribe(participantID ID, requests []SubscribeRequest) {
	participant := t.GetParticipant(participantID)
	if participant == nil {
		return
	}

	for _, request := range requests {
		published := t.FindPublishedTrack(request.TrackID)
		if published == nil {
			participant.Logger.Errorf("Can't subscribe to non-existent track %s", request.TrackID)
			continue
		}

		if published.Subscriptions[participantID] != nil {
			participant.Logger.Errorf("already subscribed to %s", request.TrackID)
			continue
		}

		var (
			sub subscription.Subscription
			err error
		)

		switch request.Kind {
		case webrtc.RTPCodecTypeVideo:
			owner := t.GetParticipant(published.Owner)
			if owner == nil {
				participant.Logger.Errorf("Can't subscribe to non-existent owner %s", published.Owner)
				continue
			}

			sub, err = subscription.NewVideoSubscription(
				request.TrackInfo,
				request.Simulcast,
				participant.Peer,
				func(track webrtc_ext.TrackInfo, simulcast webrtc_ext.SimulcastLayer) error {
					return owner.Peer.RequestKeyFrame(track, simulcast)
				},
				participant.Logger,
			)
		case webrtc.RTPCodecTypeAudio:
			sub, err = subscription.NewAudioSubscription(
				published.OutputTrack,
				participant.Peer,
			)
		}

		if err != nil {
			participant.Logger.Errorf("failed to create subscription: %s", err)
			continue
		}

		published.Subscriptions[participantID] = sub
		participant.Logger.Infof("Subscribed to %s (%s)", request.TrackID, request.Simulcast)
	}
}

// Returns a subscription that corresponds to the `participantID` subscriber for the `trackID`. If no such participant
// is subscribed to a track or no such track exists, `nil` would be returned.
func (t *Tracker) GetSubscription(trackID TrackID, participantID ID) subscription.Subscription {
	if published := t.FindPublishedTrack(trackID); published != nil {
		return published.Subscriptions[participantID]
	}

	return nil
}

// Unsubscribes a given `participantID` from the given tracks.
func (t *Tracker) Unsubscribe(participantID ID, tracks []TrackID) {
	for _, trackID := range tracks {
		if published := t.FindPublishedTrack(trackID); published != nil {
			if subscription := published.Subscriptions[participantID]; subscription != nil {
				subscription.Unsubscribe()
				delete(published.Subscriptions, participantID)
			}
		}
	}
}

// Processes an RTP packet received on a given track.
func (t *Tracker) ProcessRTP(info webrtc_ext.TrackInfo, simulcast webrtc_ext.SimulcastLayer, packet *rtp.Packet) {
	if published := t.publishedTracks[info.TrackID]; published != nil {
		for _, sub := range published.Subscriptions {
			if sub.Simulcast() == simulcast {
				if err := sub.WriteRTP(*packet); err != nil {
					logrus.Errorf("Dropping an RTP packet on %s (%s): %s", info.TrackID, simulcast, err)
				}
			}
		}
	}
}
