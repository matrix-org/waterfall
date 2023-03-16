package participant

import (
	"fmt"

	"github.com/matrix-org/waterfall/pkg/conference/track"
	"github.com/matrix-org/waterfall/pkg/webrtc_ext"
	"github.com/pion/webrtc/v3"
)

type TrackStoppedMessage struct {
	TrackID track.TrackID
	OwnerID ID
}

// Tracks participants and their corresponding tracks.
// These are grouped together as the field in this structure must be kept synchronized.
type Tracker struct {
	participants    map[ID]*Participant
	publishedTracks map[track.TrackID]*track.PublishedTrack[ID]

	publishedTrackStopped chan<- TrackStoppedMessage
	conferenceEnded       <-chan struct{}
}

func NewParticipantTracker(conferenceEnded <-chan struct{}) (*Tracker, <-chan TrackStoppedMessage) {
	publishedTrackStopped := make(chan TrackStoppedMessage)
	return &Tracker{
		participants:          make(map[ID]*Participant),
		publishedTracks:       make(map[track.TrackID]*track.PublishedTrack[ID]),
		publishedTrackStopped: publishedTrackStopped,
		conferenceEnded:       conferenceEnded,
	}, publishedTrackStopped
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

	defer participant.Telemetry.End()

	// Terminate the participant and remove it from the list.
	participant.Peer.Terminate()
	close(participant.Pong)
	delete(t.participants, participantID)

	// Remove the participant's tracks from all participants who might have subscribed to them.
	streamIdentifiers := make(map[string]bool)
	for trackID, track := range t.publishedTracks {
		if track.Owner() == participantID {
			// Odd way to add to a set in Go.
			streamIdentifiers[track.Info().StreamID] = true
			t.RemovePublishedTrack(trackID)
		}
	}

	// Go over all subscriptions and remove the participant from them.
	// TODO: Perhaps we could simply react to the subscrpitions dying and remove them from the list.
	for _, publishedTrack := range t.publishedTracks {
		publishedTrack.Unsubscribe(participantID)
	}

	return streamIdentifiers
}

// Adds a new track to the list of published tracks, i.e. by calling it we inform the tracker that there is new track
// that has been published and that we must take into account from now on.
func (t *Tracker) AddPublishedTrack(
	participantID ID,
	remoteTrack *webrtc.TrackRemote,
	metadata track.TrackMetadata,
) error {
	participant := t.participants[participantID]
	if participant == nil {
		return fmt.Errorf("participant %s does not exist", participantID)
	}

	// If this is a new track, let's add it to the list of published and inform participants.
	if published, found := t.publishedTracks[remoteTrack.ID()]; found {
		if err := published.AddPublisher(remoteTrack); err != nil {
			return err
		}

		return nil
	}

	published, err := track.NewPublishedTrack(
		participantID,
		participant.Peer.RequestKeyFrame,
		remoteTrack,
		metadata,
		participant.Logger,
		participant.Telemetry,
	)
	if err != nil {
		return err
	}

	// Wait for the track to complete and inform the conference about it.
	go func() {
		// Wait for the track to complete.
		<-published.Done()

		// Inform the conference that the track is gone. Or stop the go-routine if the conference stopped.
		select {
		case t.publishedTrackStopped <- TrackStoppedMessage{remoteTrack.ID(), participantID}:
		case <-t.conferenceEnded:
		}
	}()

	t.publishedTracks[remoteTrack.ID()] = published
	return nil
}

// Iterates over published tracks and calls a closure upon each track info.
func (t *Tracker) ForEachPublishedTrackInfo(fn func(ID, webrtc_ext.TrackInfo)) {
	for _, track := range t.publishedTracks {
		fn(track.Owner(), track.Info())
	}
}

// Updates metadata associated with a given track.
func (t *Tracker) UpdatePublishedTrackMetadata(id track.TrackID, metadata track.TrackMetadata) {
	if track, found := t.publishedTracks[id]; found {
		track.SetMetadata(metadata)
		t.publishedTracks[id] = track
	}
}

// Informs the tracker that one of the previously published tracks is gone.
func (t *Tracker) RemovePublishedTrack(id track.TrackID) {
	if publishedTrack, found := t.publishedTracks[id]; found {
		publishedTrack.Stop()
		delete(t.publishedTracks, id)
	}
}

// Subscribes a given participant to the track.
func (t *Tracker) Subscribe(participantID ID, trackID track.TrackID, requirements track.TrackMetadata) error {
	// Check if the participant exists that wants to subscribe exists.
	participant := t.participants[participantID]
	if participant == nil {
		return fmt.Errorf("participant %s does not exist", participantID)
	}

	// Check if the track that we want to subscribe exists.
	published := t.publishedTracks[trackID]
	if published == nil {
		return fmt.Errorf("track %s does not exist", trackID)
	}

	// Subscribe to the track.
	if err := published.Subscribe(participantID, participant.Peer, requirements, participant.Logger); err != nil {
		return err
	}

	return nil
}

// Unsubscribes a given `participantID` from the track.
func (t *Tracker) Unsubscribe(participantID ID, trackID track.TrackID) {
	if published := t.publishedTracks[trackID]; published != nil {
		published.Unsubscribe(participantID)
	}
}
