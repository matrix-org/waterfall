package common

import (
	"errors"
)

var ErrSinkSealed = errors.New("The channel is sealed")

// SinkWithSender is a helper struct that allows to send messages to a message sink.
// The SinkWithSender abstracts the message sink which has a certain sender, so that
// the sender does not have to be specified every time a message is sent.
// At the same it guarantees that the caller can't alter the `sender`, which means that
// the sender can't impersonate another sender (and we guarantee this on a compile-time).
type SinkWithSender[SenderType comparable, MessageType any] struct {
	// The sender of the messages. This is useful for multiple-producer-single-consumer scenarios.
	sender SenderType
	// The message sink to which the messages are sent.
	messageSink chan<- Message[SenderType, MessageType]
	// A channel that is used to indicate that our channel is considered sealed. It's akin
	// to a close indication without really closing the channel. We don't want to close
	// the channel here since we know that the sink is shared between multiple producers,
	// so we only disallow sending to the sink at this point.
	sealed chan struct{}
}

// Creates a new MessageSink. The function is generic allowing us to use it for multiple use cases.
func NewSink[S comparable, M any](sender S, messageSink chan<- Message[S, M]) *SinkWithSender[S, M] {
	return &SinkWithSender[S, M]{
		sender:      sender,
		messageSink: messageSink,
		sealed:      make(chan struct{}),
	}
}

// Sends a message to the message sink. Blocks if the sink is full!
func (s *SinkWithSender[S, M]) Send(message M) error {
	messageWithSender := Message[S, M]{
		Sender:  s.sender,
		Content: message,
	}

	select {
	case <-s.sealed:
		return ErrSinkSealed
	case s.messageSink <- messageWithSender:
		return nil
	}
}

// Seals the channel, which means that no messages could be sent via this channel.
// Any attempt to send a message would result in an error. This is similar to closing the
// channel except that we don't close the underlying channel (since there might be other
// senders that may want to use it).
func (s *SinkWithSender[S, M]) Seal() {
	select {
	case <-s.sealed:
		return
	default:
		close(s.sealed)
	}
}

// Messages that are sent from the peer to the conference in order to communicate with other peers.
// Since each peer is isolated from others, it can't influence the state of other peers directly.
type Message[SenderType comparable, MessageType any] struct {
	// The sender of the message.
	Sender SenderType
	// The content of the message.
	Content MessageType
}
