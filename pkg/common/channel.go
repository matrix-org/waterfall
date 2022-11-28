package common

import "sync/atomic"

// Creates a new channel, returns two counterparts of it where one can only send and another can only receive.
// Unlike traditional Go channels, these allow the receiver to mark the channel as closed which would then fail
// to send any messages to the channel over `Send“.
func NewChannel[M any]() (Sender[M], Receiver[M]) {
	channel := make(chan M, 128)
	closed := &atomic.Bool{}
	sender := Sender[M]{channel, closed}
	receiver := Receiver[M]{channel, closed}
	return sender, receiver
}

type Sender[M any] struct {
	channel        chan<- M
	receiverClosed *atomic.Bool
}

func (s *Sender[M]) Send(message M) *M {
	if !s.receiverClosed.Load() {
		s.channel <- message
		return nil
	} else {
		return &message
	}
}

type Receiver[M any] struct {
	Channel        <-chan M
	receiverClosed *atomic.Bool
}

func (r *Receiver[M]) Close() {
	r.receiverClosed.Store(true)
}
