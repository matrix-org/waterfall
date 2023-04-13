package track

import (
	"fmt"
	"time"

	"github.com/matrix-org/waterfall/pkg/conference/publisher"
	"github.com/matrix-org/waterfall/pkg/telemetry"
	"github.com/matrix-org/waterfall/pkg/webrtc_ext"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
)

type trackOwner[SubscriberID comparable] struct {
	owner           SubscriberID
	requestKeyFrame func(track *webrtc.TrackRemote) error
}

type audioTrack struct {
	// The sink of this audio track packets.
	outputTrack *webrtc.TrackLocalStaticRTP
}

type videoTrack struct {
	// Publishers of each video layer.
	publishers map[webrtc_ext.SimulcastLayer]*publisher.Publisher
}

// Forward audio packets from the source track to the destination track.
func forward(sender *webrtc.TrackRemote, receiver *webrtc.TrackLocalStaticRTP, stop <-chan struct{}) error {
	for {
		// Read the data from the remote track.
		packet, _, readErr := sender.ReadRTP()
		if readErr != nil {
			return readErr
		}

		// Write the data to the local track.
		if writeErr := receiver.WriteRTP(packet); writeErr != nil {
			return writeErr
		}

		// Check if we need to stop processing packets.
		select {
		case <-stop:
			return nil
		default:
		}
	}
}

// Represents a single publisher (i.e. a single `RemoteTrack`), in most cases it's a single simulcast layer.
type trackPublisher struct {
	// The actual publisher (an entity that wraps an abstract remote track and reads frames from it while
	// forwarding to the subscribers that are attached to the publisher).
	publisher *publisher.Publisher
	// A channel to observe status changes on the publisher (stalled, recovered, stopped).
	eventsChannel <-chan publisher.Status
	// A simulcast layer that this publisher is responsible for.
	layer webrtc_ext.SimulcastLayer
	// Scoped logger.
	logger *logrus.Entry
	// Scoped telemetry.
	telemetry *telemetry.Telemetry
}

func (p *PublishedTrack[SubscriberID]) addVideoPublisher(track *webrtc.TrackRemote) {
	// Detect simulcast layer of a publisher and create loggers and scoped telemetry.
	simulcast := webrtc_ext.RIDToSimulcastLayer(track.RID())
	pubLogger := p.logger.WithField("layer", simulcast.String())

	// Create a new publisher for the track.
	pub, pubCh := publisher.NewPublisher(
		&publisher.RemoteTrack{track},
		p.stopPublishers,
		2*time.Second, // We consider publisher as stalled if there are no packets within 2 seconds.
		pubLogger,
	)

	trackPublisher := &trackPublisher{
		publisher:     pub,
		eventsChannel: pubCh,
		layer:         simulcast,
		logger:        pubLogger,
		telemetry:     p.telemetry.CreateChild("layer", attribute.String("layer", simulcast.String())),
	}
	p.video.publishers[simulcast] = trackPublisher.publisher

	// Start publisher's goroutine.
	p.activePublishers.Add(1)
	go func() {
		// Once this go-routine is done, inform that this publisher is stopped.
		defer p.activePublishers.Done()
		defer trackPublisher.telemetry.End()

		// Observe publisher's status events.
		for status := range trackPublisher.eventsChannel {
			switch status {
			case publisher.StatusStalled:
				// Publisher is not active (no packets received for a while).
				p.handleStalledPublisher(trackPublisher)

			case publisher.StatusRecovered:
				// Publisher is active again (new packets received).
				trackPublisher.logger.Info("Publisher is recovered")
				trackPublisher.telemetry.AddEvent("recovered")

				// Iterate over active subscriptions that don't have any active publisher
				// and assign them to this publisher.
				p.recoverOrphanedSubscriptions(trackPublisher)
			}
		}

		trackPublisher.telemetry.AddEvent("stopped, removing dependent subscriptions")

		// If we got there, then the publisher is stopped.
		p.mutex.Lock()
		defer p.mutex.Unlock()

		// Remove the publisher once it's gone.
		delete(p.video.publishers, trackPublisher.layer)

		// Now iterate over all subscriptions and find those that are now lost due to the publisher being stopped.
		// Try to find any other available publisher for this subscription (since these are all publishers/layers
		// of the same track). We do iteration over the publishers map to get a single (random) available publisher.
		// Golang does not have a function to get a random or "first" element of the map.
		//
		// TODO: Do we need to do it? Can publishers **fail** during the call and get created by Pion automatically?
		for layer, pub := range p.video.publishers {
			for _, sub := range pub.RemoveSubscriptions() {
				sub.(*trackSubscription[SubscriberID]).currentLayer = layer //nolint:forcetypeassert
				pub.AddSubscription(sub)
			}
			break
		}
	}()
}

func (p *PublishedTrack[SubscriberID]) handleStalledPublisher(pub *trackPublisher) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	// Let's check if we're muted. If we are, it's ok to not receive packets.
	if p.metadata.Muted {
		pub.logger.Info("Publisher is stalled but we're muted, ignoring")
		pub.telemetry.AddEvent("muted")
		return
	}

	// Otherwise, remove all subscriptions and switch them to the lowest layer if available.
	// We assume that the lowest layer is the latest to fail (normally, lowest layer always
	// receive packets even if other layers are stalled).

	// Now we just cast it to the actual type of the subscription (since we know the type).
	// This could have been avoided if we used **generics** with `publisher.Publisher` instead
	// of an interface. Then we could spare this type assertion.
	removed := pub.publisher.RemoveSubscriptions()
	subscriptions := make([]*trackSubscription[SubscriberID], len(removed))
	for i, sub := range removed {
		subscriptions[i] = sub.(*trackSubscription[SubscriberID]) //nolint:forcetypeassert
	}

	if lowLayer := p.video.publishers[webrtc_ext.SimulcastLayerLow]; lowLayer != nil {
		pub.logger.Info("Publisher is stalled, switching to the lowest layer")
		pub.telemetry.AddEvent("stalled, so subscriptions switched to the low layer")
		for _, sub := range subscriptions {
			lowLayer.AddSubscription(sub)
			sub.currentLayer = webrtc_ext.SimulcastLayerLow
		}
		return
	}

	// Otherwise, we have no other layer to switch to. Bummer.
	pub.logger.Warn("Publisher is stalled and we have no other layer to switch to")
	pub.telemetry.Fail(fmt.Errorf("stalled"))
	for _, sub := range subscriptions {
		sub.currentLayer = webrtc_ext.SimulcastLayerNone
	}
}

// Goes through the subscriptions that are not assigned to any publisher, i.e.
// those that used to have a publisher, i.e. the track that used to produce the
// RTP packets and that publisher went stalled (no packets received for a
// while). Such subscriptions don't receive any packets and so such remote
// track will be observed by the participant either as a grey frame (if it's a
// start of a call) or as a freeze (if it's in the middle of a call). We call
// this function to switch stalled subscriptions to use the given publisher.
func (p *PublishedTrack[SubscriberID]) recoverOrphanedSubscriptions(
	trackPublisher *trackPublisher,
) error {
	if trackPublisher.publisher.IsStalled() {
		return fmt.Errorf("publisher is stalled, can't use it to reactivate stalled subscriptions")
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()

	for _, subscription := range p.subscriptions {
		if subscription.currentLayer == webrtc_ext.SimulcastLayerNone {
			subscription.currentLayer = trackPublisher.layer
			trackPublisher.publisher.AddSubscription(subscription)
		}
	}

	return nil
}
