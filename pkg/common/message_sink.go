package common

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
}

// Creates a new MessageSink. The function is generic allowing us to use it for multiple use cases.
func NewMessageSink[S comparable, M any](sender S, messageSink chan<- Message[S, M]) *MessageSink[S, M] {
	return &MessageSink[S, M]{
		sender:      sender,
		messageSink: messageSink,
	}
}

// Sends a message to the message sink.
func (s *MessageSink[S, M]) Send(message M) {
	s.messageSink <- Message[S, M]{
		Sender:  s.sender,
		Content: message,
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
