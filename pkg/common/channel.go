package common

import "sync/atomic"

// In Go, unbounded channel means something different than what it means in Rust.
// I.e. unlike Rust, "unbounded" in Go means that the channel has **no buffer**,
// meaning that each attempt to send will block the channel until the receiver
// reads it. Majority of primitives here in `waterfall` are designed under assumption
// that sending is not blocking.
const UnboundedChannelSize = 512

// Creates a new channel, returns two counterparts of it where one can only send and another can only receive.
// Unlike traditional Go channels, these allow the receiver to mark the channel as closed which would then fail
// to send any messages to the channel over `Send“.
func NewChannel[M any]() (Sender[M], Receiver[M]) {
	channel := make(chan M, UnboundedChannelSize)
	closed := &atomic.Bool{}
	sender := Sender[M]{channel, closed}
	receiver := Receiver[M]{channel, closed}
	return sender, receiver
}

// Sender counterpart of the channel.
type Sender[M any] struct {
	// The channel itself.
	channel chan<- M
	// Atomic variable that indicates whether the channel is closed.
	receiverClosed *atomic.Bool
}

// Tries to send a message if the channel is not closed.
// Returns the message back if the channel is closed.
func (s *Sender[M]) Send(message M) *M {
	if !s.receiverClosed.Load() {
		s.channel <- message
		return nil
	} else {
		return &message
	}
}

// The receiver counterpart of the channel.
type Receiver[M any] struct {
	// The channel itself. It's public, so that we can combine it in `select` statements.
	Channel <-chan M
	// Atomic variable that indicates whether the channel is closed.
	receiverClosed *atomic.Bool
}

// Marks the channel as closed, which means that no messages could be sent via this channel.
// Any attempt to send a message would result in an error. This is similar to closing the
// channel except that we don't close the underlying channel (since in Go receivers can't
// close the channel).
//
// This function reads (in a non-blocking way) all pending messages until blocking. Otherwise,
// they will stay forver in a channel and get lost.
func (r *Receiver[M]) Close() []M {
	r.receiverClosed.Store(true)

	messages := make([]M, 0)
	for {
		msg, ok := <-r.Channel
		if !ok {
			break
		}
		messages = append(messages, msg)
	}

	return messages
}
