package track

import (
	"fmt"
	"sync"

	"github.com/matrix-org/waterfall/pkg/conference/subscription"
	"github.com/matrix-org/waterfall/pkg/telemetry"
	"github.com/matrix-org/waterfall/pkg/webrtc_ext"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
)

// A subscruber identifier is something that is comparable and convertable to a String.
type SubscriberIdentifier interface {
	comparable
	fmt.Stringer
}

type TrackID = string

// Represents a track that a peer has published (has already started sending to the SFU).
type PublishedTrack[SubscriberID SubscriberIdentifier] struct {
	// Logger.
	logger *logrus.Entry
	// Telemetry.
	telemetry *telemetry.Telemetry
	// Info about the track.
	info webrtc_ext.TrackInfo
	// Owner of a published track.
	owner trackOwner[SubscriberID]

	// We must protect the data with a mutex since we want the `PublishedTrack` to remain thread-safe.
	mutex sync.Mutex
	// Currently active subscriptions for this track.
	subscriptions map[SubscriberID]*trackSubscription[SubscriberID]
	// Audio track data. The content will be `nil` if it's not an audio track.
	audio *audioTrack
	// Video track. The content will be `nil` if it's not a video track.
	video *videoTrack
	// Track metadata.
	metadata TrackMetadata

	// Wait group for all active publishers.
	activePublishers *sync.WaitGroup
	// A signal to publishers **to stop** them all.
	stopPublishers chan struct{}
	// A aignal to inform the caller that all publishers of this track **have been stopped**.
	done chan struct{}
}

func NewPublishedTrack[SubscriberID SubscriberIdentifier](
	ownerID SubscriberID,
	requestKeyFrame func(track *webrtc.TrackRemote) error,
	track *webrtc.TrackRemote,
	metadata TrackMetadata,
	logger *logrus.Entry,
	telemetryBuilder *telemetry.ChildBuilder,
) (*PublishedTrack[SubscriberID], error) {
	telemetry := telemetryBuilder.Create(
		"PublishedTrack",
		attribute.String("track_id", track.ID()),
		attribute.String("type", track.Kind().String()),
	)

	published := &PublishedTrack[SubscriberID]{
		logger:           logger.WithField("track", track.ID()),
		info:             webrtc_ext.TrackInfoFromTrack(track),
		telemetry:        telemetry,
		owner:            trackOwner[SubscriberID]{ownerID, requestKeyFrame},
		subscriptions:    make(map[SubscriberID]*trackSubscription[SubscriberID]),
		audio:            &audioTrack{outputTrack: nil},
		video:            &videoTrack{publishers: make(map[webrtc_ext.SimulcastLayer]*trackPublisher)},
		metadata:         metadata,
		activePublishers: &sync.WaitGroup{},
		stopPublishers:   make(chan struct{}),
		done:             make(chan struct{}),
	}

	switch published.info.Kind {
	case webrtc.RTPCodecTypeAudio:
		// Create a local track, all our SFU clients that are subscribed to this
		// peer (publisher) wil be fed via this track.
		localTrack, err := webrtc.NewTrackLocalStaticRTP(
			track.Codec().RTPCodecCapability,
			track.ID(),
			track.StreamID(),
		)
		if err != nil {
			telemetry.Fail(err)
			telemetry.End()
			return nil, err
		}

		published.audio.outputTrack = localTrack

		// Start audio publisher in a separate goroutine.
		published.activePublishers.Add(1)
		go func() {
			defer published.activePublishers.Done()
			if err := forward(track, localTrack, published.stopPublishers); err != nil {
				logger.Infof("audio publisher stopped: %v", err)
			}
		}()

	case webrtc.RTPCodecTypeVideo:
		// Start video publisher.
		published.addVideoPublisher(track)
	}

	// Wait for all publishers to stop.
	go func() {
		defer close(published.done)
		defer telemetry.End()
		published.activePublishers.Wait()
	}()

	return published, nil
}

// Adds a new publisher to the existing `PublishedTrack`, this happens if we
// have multiple qualities (layers) on a single track.
func (p *PublishedTrack[SubscriberID]) AddPublisher(track *webrtc.TrackRemote) error {
	if p.isClosed() {
		return fmt.Errorf("track is already closed")
	}

	info := webrtc_ext.TrackInfoFromTrack(track)
	if info.TrackID != p.info.TrackID || p.info.Kind.String() != info.Kind.String() {
		return fmt.Errorf("track mismatch")
	}

	// Such publisher already exists. Let's replace the track that provides frames with a new one.
	simulcast := webrtc_ext.RIDToSimulcastLayer(track.RID())

	// Lock the mutex since we access the publishers from multiple threads.
	p.mutex.Lock()
	defer p.mutex.Unlock()

	// If the publisher for this track already exists, let's replace the track. This may happen during
	// the negotiation when the SSRC changes and Pion fires a new track for the track that has already
	// been published.
	if pub := p.video.publishers[simulcast]; pub != nil {
		p.telemetry.AddEvent("replacing publisher", attribute.String("simulcast", simulcast.String()))
		pub.replaceTrack(track)
		return nil
	}

	// Add a publisher and start polling it.
	p.addVideoPublisher(track)
	return nil
}

// Stops the published track and all related publishers. You should not use the
// `PublishedTrack` after calling this method.
func (p *PublishedTrack[SubscriberID]) Stop() {
	// Command all publishers to stop, unless already stopped.
	if !p.isClosed() {
		p.telemetry.AddEvent("stopping")
		close(p.stopPublishers)
	}
}

// Create a new subscription for a given subscriber or update the existing one if necessary.
func (p *PublishedTrack[SubscriberID]) Subscribe(
	subscriberID SubscriberID,
	controller subscription.SubscriptionController,
	desiredWidth int,
	desiredHeight int,
	logger *logrus.Entry,
) error {
	if p.isClosed() {
		return fmt.Errorf("track is already closed")
	}

	// Lock the mutex as we access subscriptions and publishers from multiple threads.
	p.mutex.Lock()
	defer p.mutex.Unlock()

	// Let's calculate the desired simulcast layer (if any).
	var layer webrtc_ext.SimulcastLayer
	if p.isSimulcast() {
		layers := make(map[webrtc_ext.SimulcastLayer]struct{}, len(p.video.publishers))
		for layer, publisher := range p.video.publishers {
			if !publisher.isStalled() {
				layers[layer] = struct{}{}
			}
		}
		layer = getOptimalLayer(layers, p.metadata, desiredWidth, desiredHeight)
	}

	// If the subscription exists, let's see if we need to update it.
	if sub := p.subscriptions[subscriberID]; sub != nil {
		// If we do, let's switch the layer.
		if sub.currentLayer != layer {
			p.video.publishers[sub.currentLayer].removeSubscription(sub)
			p.video.publishers[layer].addSubscription(sub)
			sub.currentLayer = layer
		}

		// Subsription is up-to-date, nothing to change.
		return nil
	}

	sub, ch, err := func() (subscription.Subscription, <-chan subscription.KeyFrameRequest, error) {
		// Subscription does not exist, so let's create it.
		switch p.info.Kind {
		case webrtc.RTPCodecTypeVideo:
			sub, ch, err := subscription.NewVideoSubscription(
				p.info,
				controller,
				logger.WithField("track", p.info.TrackID),
				p.telemetry.ChildBuilder(attribute.String("id", subscriberID.String())),
			)
			return sub, ch, err
		case webrtc.RTPCodecTypeAudio:
			sub, err := subscription.NewAudioSubscription(p.audio.outputTrack, controller)
			return sub, nil, err
		default:
			return nil, nil, fmt.Errorf("unsupported track kind: %v", p.info.Kind)
		}
	}()
	if err != nil {
		p.telemetry.AddError(fmt.Errorf("failed to create subscription: %w", err))
		return err
	}

	// Add the subscription to the list of subscriptions.
	subscription := &trackSubscription[SubscriberID]{sub, layer, subscriberID}
	p.subscriptions[subscriberID] = subscription

	// And if it's a video subscription, add it to the list of subscriptions that get the feed from the publisher.
	if p.info.Kind == webrtc.RTPCodecTypeVideo {
		p.video.publishers[layer].addSubscription(subscription)
		go p.processSubscriptionEvents(subscription, ch)
	}

	p.logger.WithField("subscriber", subscriberID).WithField("layer", layer).Info("New subscription")
	return nil
}

// Remove subscriptions with a given subscriber id.
func (p *PublishedTrack[SubscriberID]) Unsubscribe(subscriberID SubscriberID) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if sub := p.subscriptions[subscriberID]; sub != nil {
		sub.Unsubscribe()
		delete(p.subscriptions, subscriberID)

		if p.info.Kind == webrtc.RTPCodecTypeVideo {
			p.video.publishers[sub.currentLayer].removeSubscription(sub)
		}
	}
}

func (p *PublishedTrack[SubscriberID]) Owner() SubscriberID {
	return p.owner.owner
}

func (p *PublishedTrack[SubscriberID]) Info() webrtc_ext.TrackInfo {
	return p.info
}

func (p *PublishedTrack[SubscriberID]) Done() <-chan struct{} {
	return p.done
}

func (p *PublishedTrack[SubscriberID]) Metadata() TrackMetadata {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.metadata
}

func (p *PublishedTrack[SubscriberID]) SetMetadata(metadata TrackMetadata) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.metadata = metadata
}

func (p *PublishedTrack[SubscriberID]) isClosed() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}
