package track

import (
	"fmt"

	"github.com/matrix-org/waterfall/pkg/conference/publisher"
	"github.com/matrix-org/waterfall/pkg/webrtc_ext"
	"github.com/matrix-org/waterfall/pkg/worker"
	"github.com/pion/webrtc/v3"
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
	// Key frame request handler.
	keyframeHandler *worker.Worker[webrtc_ext.SimulcastLayer]
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

func (p *PublishedTrack[SubscriberID]) addVideoPublisher(track *webrtc.TrackRemote) {
	// Detect simulcast layer of a publisher and create loggers and scoped telemetry.
	simulcast := webrtc_ext.RIDToSimulcastLayer(track.RID())
	layerTelemetry := p.telemetry.CreateChild("layer", attribute.String("layer", simulcast.String()))
	layerLogger := p.logger.WithField("layer", simulcast.String())

	// Create a new publisher for the track.
	pub, statusCh := publisher.NewPublisher(&publisher.RemoteTrack{track}, p.stopPublishers, layerLogger)
	p.video.publishers[simulcast] = pub

	// Observe the status of the publisher.
	p.activePublishers.Add(1)
	go func() {
		// Once this go-routine is done, inform that this publisher is stopped.
		defer p.activePublishers.Done()
		defer layerTelemetry.End()

		// Observe publisher's status events.
		for status := range statusCh {
			switch status {
			// Publisher is not active (no packets received for a while).
			case publisher.StatusStalled:
				p.mutex.Lock()
				defer p.mutex.Unlock()

				// Let's check if we're muted. If we are, it's ok to not receive packets.
				if p.metadata.Muted {
					layerLogger.Info("Publisher is stalled but we're muted, ignoring")
					layerTelemetry.AddEvent("muted")
					continue
				}

				// Otherwise, remove all subscriptions and switch them to the lowest layer if available.
				// We assume that the lowest layer is the latest to fail (normally, lowest layer always
				// receive packets even if other layers are stalled).
				subscriptions := pub.DrainSubscriptions()
				lowLayer := p.video.publishers[webrtc_ext.SimulcastLayerLow]
				if lowLayer != nil {
					layerLogger.Info("Publisher is stalled, switching to the lowest layer")
					layerTelemetry.AddEvent("stalled, switched to the low layer")
					lowLayer.AddSubscription(subscriptions...)
					continue
				}

				// Otherwise, we have no other layer to switch to. Bummer.
				layerLogger.Warn("Publisher is stalled and we have no other layer to switch to")
				layerTelemetry.Fail(fmt.Errorf("stalled"))
				continue

			// Publisher is active again (new packets received).
			case publisher.StatusRecovered:
				// Currently, we don't have any actions when the publisher is recovered, i.e. we
				// do not switch subscriptions that **used to be subscribed to this layer** back.
				// But we may want to do it once we have congestion control and bandwidth allocation.
			}
		}

		p.mutex.Lock()
		defer p.mutex.Unlock()

		// Remove the publisher once it's gone.
		delete(p.video.publishers, simulcast)

		// Find any other available layer, so that we can switch subscriptions that lost their publisher
		// to a new publisher (at least they'll get some data).
		var (
			availableLayer     webrtc_ext.SimulcastLayer
			availablePublisher *publisher.Publisher
		)
		for layer, pub := range p.video.publishers {
			availableLayer = layer
			availablePublisher = pub
			break
		}

		// Now iterate over all subscriptions and find those that are now lost due to the publisher being away.
		for subID, sub := range p.subscriptions {
			if sub.Simulcast() == simulcast {
				// If there is some other publisher on a different layer, let's switch to it
				if availablePublisher != nil {
					sub.SwitchLayer(availableLayer)
					pub.AddSubscription(sub)
				} else {
					// Otherwise, let's just remove the subscription.
					sub.Unsubscribe()
					delete(p.subscriptions, subID)
				}
			}
		}
	}()
}

func (p *PublishedTrack[SubscriberID]) isClosed() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}
