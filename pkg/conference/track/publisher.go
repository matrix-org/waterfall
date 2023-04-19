package track

import (
	"time"

	"github.com/matrix-org/waterfall/pkg/conference/publisher"
	"github.com/matrix-org/waterfall/pkg/telemetry"
	"github.com/matrix-org/waterfall/pkg/webrtc_ext"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
)

// Represents a single publisher (i.e. a single `RemoteTrack`), in most cases it's a single simulcast layer.
type trackPublisher struct {
	// The actual publisher (an entity that wraps an abstract remote track and reads frames from it while
	// forwarding to the subscribers that are attached to the publisher).
	publisher *publisher.Publisher
	// A channel to observe status changes on the publisher (stalled, recovered, stopped).
	eventsChannel <-chan publisher.Status
	// Keyframe request function.
	requestKeyFrameFn func(*webrtc.TrackRemote) error
	// A simulcast layer that this publisher is responsible for.
	layer webrtc_ext.SimulcastLayer
	// Scoped logger.
	logger *logrus.Entry
	// Scoped telemetry.
	telemetry *telemetry.Telemetry
}

func newTrackPublisher(
	track *webrtc.TrackRemote,
	reqKeyFrameFn func(track *webrtc.TrackRemote) error,
	stopPublishers <-chan struct{},
	stallTimeout time.Duration,
	layer webrtc_ext.SimulcastLayer,
	logger *logrus.Entry,
	telemetry *telemetry.Telemetry,
) *trackPublisher {
	pub, pubCh := publisher.NewPublisher(
		&publisher.RemoteTrack{track},
		stopPublishers,
		stallTimeout,
		logger,
	)

	return &trackPublisher{pub, pubCh, reqKeyFrameFn, layer, logger, telemetry}
}

func (p *trackPublisher) addSubscription(subscription publisher.Subscription) {
	p.publisher.AddSubscription(subscription)
	p.requestKeyFrame()
}

func (p *trackPublisher) removeSubscription(subscription publisher.Subscription) {
	p.publisher.RemoveSubscription(subscription)
}

func (p *trackPublisher) removeSubscriptions() []publisher.Subscription {
	return p.publisher.RemoveSubscriptions()
}

func (p *trackPublisher) replaceTrack(track *webrtc.TrackRemote) {
	p.publisher.ReplaceTrack(&publisher.RemoteTrack{track})
}

func (p *trackPublisher) isStalled() bool {
	return p.publisher.IsStalled()
}

func (p *trackPublisher) requestKeyFrame() error {
	track := p.publisher.GetTrack().(*publisher.RemoteTrack) //nolint:forcetypeassert
	return p.requestKeyFrameFn(track.Track)
}
