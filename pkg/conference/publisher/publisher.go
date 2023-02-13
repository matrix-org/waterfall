package publisher

import (
	"errors"
	"sync"

	"github.com/pion/rtp"
	"github.com/sirupsen/logrus"
)

var ErrSubscriptionExists = errors.New("subscription already exists")

type Subscription interface {
	// WriteRTP **must not** block (wait on I/O).
	WriteRTP(packet rtp.Packet) error
}

type Track interface {
	// ReadPacket **may** block (wait on I/O).
	ReadPacket() (*rtp.Packet, error)
}

// An abstract publisher that reads the packets from the track and forwards them to all subscribers.
type Publisher struct {
	logger *logrus.Entry

	mu            sync.Mutex
	track         Track
	subscriptions map[Subscription]struct{}
}

func NewPublisher(
	track Track,
	stop <-chan struct{},
	log *logrus.Entry,
) (*Publisher, <-chan struct{}) {
	// Create a done channel, so that we can signal the caller when we're done.
	done := make(chan struct{})

	publisher := &Publisher{
		logger:        log,
		track:         track,
		subscriptions: make(map[Subscription]struct{}),
	}

	// Start a goroutine that will read RTP packets from the remote track.
	// We run the publisher until we receive a stop signal or an error occurs.
	go func() {
		defer close(done)
		for {
			// Check if we were signaled to stop.
			select {
			case <-stop:
				return
			default:
				if err := publisher.forwardPacket(); err != nil {
					log.Errorf("failed to read the frame from the track %s", err)
					return
				}
			}
		}
	}()

	return publisher, done
}

func (p *Publisher) AddSubscription(subscription Subscription) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.subscriptions[subscription]; ok {
		return
	}

	p.subscriptions[subscription] = struct{}{}
}

func (p *Publisher) RemoveSubscription(subscription Subscription) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.subscriptions, subscription)
}

func (p *Publisher) GetTrack() Track {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.track
}

func (p *Publisher) ReplaceTrack(track Track) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.track = track
}

// Reads a single packet from the remote track and forwards it to all subscribers.
func (p *Publisher) forwardPacket() error {
	track := p.GetTrack()

	packet, err := track.ReadPacket()
	if err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Write the packet to all subscribers.
	for subscription := range p.subscriptions {
		if err := subscription.WriteRTP(*packet); err != nil {
			p.logger.Warnf("packet dropped on the subscription: %s", err)
		}
	}

	return nil
}
