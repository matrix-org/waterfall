package track

import (
	"fmt"

	"github.com/matrix-org/waterfall/pkg/conference/publisher"
	"github.com/matrix-org/waterfall/pkg/conference/subscription"
	"github.com/matrix-org/waterfall/pkg/webrtc_ext"
	"github.com/pion/webrtc/v3"
)

type trackSubscription struct {
	subscription subscription.Subscription
	currentLayer webrtc_ext.SimulcastLayer
}

func (p *PublishedTrack[SubscriberID]) processSubscriptionEvents(
	sub *trackSubscription,
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
		publisher.RemoveSubscription(sub.subscription)
	}

	for subscriberID, subscription := range p.subscriptions {
		if subscription == sub {
			delete(p.subscriptions, subscriberID)
			break
		}
	}
}

func (p *PublishedTrack[SubscriberID]) processKeyFrameRequest(sub *trackSubscription) error {
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
