package track

import (
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
	simulcast := webrtc_ext.RIDToSimulcastLayer(track.RID())
	pub, done := publisher.NewPublisher(
		&publisher.RemoteTrack{track},
		p.stopPublishers,
		p.logger.WithField("layer", simulcast),
	)

	p.video.publishers[simulcast] = pub

	defer p.telemetry.AddEvent("video publisher started", attribute.String("simulcast", simulcast.String()))

	// Listen on `done` and remove the track once it's done.
	p.activePublishers.Add(1)
	go func() {
		defer p.activePublishers.Done()
		defer p.telemetry.AddEvent("video publisher stopped", attribute.String("simulcast", simulcast.String()))
		<-done

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
