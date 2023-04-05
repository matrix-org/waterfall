package track

import (
	"fmt"

	"github.com/matrix-org/waterfall/pkg/conference/publisher"
	"github.com/matrix-org/waterfall/pkg/conference/subscription"
	"github.com/matrix-org/waterfall/pkg/webrtc_ext"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

// A composite type that wraps the `subscription` along with its related data, such as
// the layer to which the subscription is subscribed (if any) and the ID of the subscriber
// that uses this subscription.
type trackSubscription[SubscriberID SubscriberIdentifier] struct {
	subscription subscription.Subscription
	currentLayer webrtc_ext.SimulcastLayer
	subscriberID SubscriberID
}

// Implementation of `subscription.Subscription`.
func (s *trackSubscription[SubscriberID]) Unsubscribe() error {
	return s.subscription.Unsubscribe()
}

// Implementation of `subscription.Subscription`.
func (s *trackSubscription[SubscriberID]) WriteRTP(packet rtp.Packet) error {
	return s.subscription.WriteRTP(packet)
}

func (p *PublishedTrack[SubscriberID]) processSubscriptionEvents(
	sub *trackSubscription[SubscriberID],
	events <-chan subscription.KeyFrameRequest,
) {
	for range events {
		if err := p.processKeyFrameRequest(sub); err != nil {
			p.logger.WithError(err).Error("Failed to handle key frame request")
			p.telemetry.AddError(err)
		}
	}

	// If we got there than the subscription has stoppped. Remove it from the list.
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if publisher := p.video.publishers[sub.currentLayer]; publisher != nil {
		publisher.RemoveSubscription(sub)
	}

	delete(p.subscriptions, sub.subscriberID)
}

func (p *PublishedTrack[SubscriberID]) processKeyFrameRequest(sub *trackSubscription[SubscriberID]) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	publisher := p.video.publishers[sub.currentLayer]
	if publisher == nil {
		return fmt.Errorf("publisher with simulcast %s not found", sub.currentLayer)
	}

	track, err := extractRemoteTrack(publisher)
	if err != nil {
		return err
	}

	return p.owner.requestKeyFrame(track)
}

func extractRemoteTrack(pub *publisher.Publisher) (*webrtc.TrackRemote, error) {
	// Get the track that we need to request a key frame for.
	track := pub.GetTrack()
	remoteTrack, ok := track.(*publisher.RemoteTrack)
	if !ok {
		return nil, fmt.Errorf("not a remote track in publisher")
	}

	return remoteTrack.Track, nil
}
