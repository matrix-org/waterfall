package publisher

import (
	"errors"
	"io"
	"sync"
	"time"

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

	observer *statusObserver
}

// Starts a new publisher, returns a publisher along with the channel that informs the caller
// about the status update of the publisher (i.e. stalled, or active). Once the channel is closed,
// the publisher can be considered stopped.
func NewPublisher(
	track Track,
	stop <-chan struct{},
	considerStalledAfter time.Duration,
	log *logrus.Entry,
) (*Publisher, <-chan Status) {
	// Start an observer that expects us to inform it every time we receive a packet.
	// When no packets are received for N seconds, the observer will report the stalled status.
	observer := newStatusObserver(considerStalledAfter)

	publisher := &Publisher{
		logger:        log,
		track:         track,
		subscriptions: make(map[Subscription]struct{}),
		observer:      observer,
	}

	// Start a goroutine that will read RTP packets from the remote track.
	// We run the publisher until we receive a stop signal or an error occurs.
	go func() {
		defer observer.stop()
		reportFrameReceived := func() { observer.packetArrived() }

		for {
			// Check if we were signaled to stop.
			select {
			case <-stop:
				return
			default:
				if err := publisher.forwardPacket(reportFrameReceived); err != nil {
					logStoppedFn := log.Infof
					if err != io.EOF {
						logStoppedFn = log.Errorf
					}

					logStoppedFn("publisher stopped: %v", err)
					return
				}
			}
		}
	}()

	return publisher, observer.statusCh
}

func (p *Publisher) AddSubscription(subscription Subscription) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.subscriptions[subscription]; !ok {
		p.subscriptions[subscription] = struct{}{}
	}
}

func (p *Publisher) RemoveSubscriptions() []Subscription {
	p.mu.Lock()
	defer p.mu.Unlock()

	subs := make([]Subscription, 0, len(p.subscriptions))
	for s := range p.subscriptions {
		subs = append(subs, s)
	}

	p.subscriptions = make(map[Subscription]struct{})
	return subs
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

func (p *Publisher) IsStalled() bool {
	return p.observer.stalled.Load()
}

// Reads a single packet from the remote track and forwards it to all subscribers.
// The function stops when the remote track is closed or an error occurs when reading.
// Each time new packet is received, the provided callback is called.
func (p *Publisher) forwardPacket(reportFrameReceived func()) error {
	track := p.GetTrack()

	packet, err := track.ReadPacket()
	if err != nil {
		return err
	}

	// Inform the observer that we received a packet.
	reportFrameReceived()

	p.mu.Lock()
	defer p.mu.Unlock()

	// Write the packet to all subscribers.
	for subscription := range p.subscriptions {
		if err := subscription.WriteRTP(*packet); err != nil {
			p.logger.Warnf("failed to forward packet to: %v", err)
		}
	}

	return nil
}
