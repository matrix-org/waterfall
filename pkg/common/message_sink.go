package common

import (
	"errors"
	"sync/atomic"
)

// MessageSink is a helper struct that allows to send messages to a message sink.
// The MessageSink abstracts the message sink which has a certain sender, so that
// the sender does not have to be specified every time a message is sent.
// At the same it guarantees that the caller can't alter the `sender`, which means that
// the sender can't impersonate another sender (and we guarantee this on a compile-time).
type MessageSink[SenderType comparable, MessageType any] struct {
	// The sender of the messages. This is useful for multiple-producer-single-consumer scenarios.
	sender SenderType
	// The message sink to which the messages are sent.
	messageSink chan<- Message[SenderType, MessageType]
	// Atomic variable that indicates whether the message sink is sealed.
	// Basically it means that **the current sender** (but not other senders)
	// won't be able to send any more messages to the message sink. The difference
	// between this and the channel being closed is that the closed channel is not
	// available for writing for all senders.
	sealed atomic.Bool
}

// Creates a new MessageSink. The function is generic allowing us to use it for multiple use cases.
func NewMessageSink[S comparable, M any](sender S, messageSink chan<- Message[S, M]) *MessageSink[S, M] {
	return &MessageSink[S, M]{
		sender:      sender,
		messageSink: messageSink,
	}
}

// Sends a message to the message sink.
func (s *MessageSink[S, M]) Send(message M) error {
	if s.sealed.Load() {
		return errors.New("The channel is sealed, you can't send any messages over it")
	}

	s.messageSink <- Message[S, M]{
		Sender:  s.sender,
		Content: message,
	}

	return nil
}

// Seals the channel, which means that no messages could be sent via this channel.
// Any attempt to send a message would result in an error. This is similar to closing the
// channel except that we don't close the underlying channel (since there might be other
// senders that may want to use it).
func (s *MessageSink[S, M]) Seal() {
	s.sealed.Store(true)
}

// Messages that are sent from the peer to the conference in order to communicate with other peers.
// Since each peer is isolated from others, it can't influence the state of other peers directly.
type Message[SenderType comparable, MessageType any] struct {
	// The sender of the message.
	Sender SenderType
	// The content of the message.
	Content MessageType
}
