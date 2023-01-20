package rewriter

import (
	"github.com/pion/rtp"
)

type RewrittenRTPPacket *rtp.Packet

// A structure that is used to rewrite the RTP packets that are being forwarded.
type PacketRewriter struct {
	// The highest identifiers of the outgoing packet returned by the processing
	// function. This is the **latest** identifier in terms of seqNum and ts, not
	// the identifier of the **last** forwarded packet.
	latestOutgoing ExpandedPacketIdentifiers
	// State of the rewriter. Currently we only have a forwarding state.
	// We'll also have "switching" state in the future to handle smooth layer switching.
	state forwardingState
}

// Creates a new instance of the `PacketRewriter`.
func NewPacketRewriter() *PacketRewriter {
	rewriter := new(PacketRewriter)
	return rewriter
}

// Process new incoming packet.
func (p *PacketRewriter) ProcessIncoming(packet rtp.Packet) (RewrittenRTPPacket, error) {
	incomingIDs := TruncatedPacketIdentifiers{packet.Timestamp, packet.SequenceNumber}
	outgoingIDs := p.state.process(packet.SSRC, incomingIDs, p.latestOutgoing)

	// Store the highest outgoing IDs.
	p.latestOutgoing = p.latestOutgoing.Max(outgoingIDs)

	// Rewrite the IDs of the incoming packet and return it.
	packet.Timestamp = uint32(outgoingIDs.timestamp)
	packet.SequenceNumber = uint16(outgoingIDs.sequenceNumber)

	return &packet, nil
}

// The state of the forwarding/rewriting process for a single SSRC, i.e. a
// single simulcast layer after a switch. This changes each time the simulcast
// layer is switched and/or the incoming SSRC changes.
type forwardingState struct {
	// The SSRC of the previously forwarded packet.
	ssrc uint32
	// The identifiers of the **first incoming packet after swithing
	// layers**. We use it to calulcate their relative position in the RTP stream.
	firstIncoming ExpandedPacketIdentifiers
	// The highest identifiers of the forwarded packet,i.e. the IDs of
	// the **latest** (in terms of timestamp and number) packet. This is not
	// necessarily the IDs of the **last forwarded packet**, due to packets
	// that may arrive out-of-order.
	latestIncoming ExpandedPacketIdentifiers
	// The identifiers of the **first outgoing packet** after switching
	// layers. This is our "base" to calculate the proper timestamp when
	// forwarding/rewriting the packet.
	firstOutgoing ExpandedPacketIdentifiers
}

// Processes the incoming IDs and returns the rewritten IDs.
func (s *forwardingState) process(
	ssrc uint32,
	incomingIDs TruncatedPacketIdentifiers,
	latestOutgoing ExpandedPacketIdentifiers,
) ExpandedPacketIdentifiers {
	// If the SSRCs don't match, then we've switched layers.
	if s.ssrc != ssrc {
		return s.reset(ssrc, incomingIDs, latestOutgoing)
	}

	// Expand the sequence number.
	latestSequenceNumber := uint64(s.latestIncoming.sequenceNumber)
	expandedSequenceNumber := uint32(expandCounter(uint64(incomingIDs.sequenceNumber), 16, &latestSequenceNumber))
	s.latestIncoming.sequenceNumber = uint32(latestSequenceNumber)

	// Expand the timestamp.
	expandedTimestamp := expandCounter(uint64(incomingIDs.timestamp), 32, &s.latestIncoming.timestamp)

	// Expanded identifiers.
	expandedIncomingIDs := ExpandedPacketIdentifiers{expandedTimestamp, expandedSequenceNumber}

	// Now we can safely calculate the delta.
	delta := expandedIncomingIDs.Sub(s.firstIncoming)

	// The outgoing IDs are the delta added to the first outgoing IDs.
	return s.firstOutgoing.Add(delta)
}

// Resets the state of the rewriter for a new SSRC (switching layers).
// Returns new outgoing identifiers.
func (s *forwardingState) reset(
	newSSRC uint32,
	incoming TruncatedPacketIdentifiers,
	latestOutgoing ExpandedPacketIdentifiers,
) ExpandedPacketIdentifiers {
	// Set the new SSRC.
	s.ssrc = newSSRC

	// Update incoming IDs of the first packet after switching layers.
	// These are OK to expand without any checks, as they are only used as a base
	// for the future values. In other words, we are only tracking the ROC since
	// the switching point, and that is now, so the ROC is 0.

	s.firstIncoming = ExpandedPacketIdentifiers{uint64(incoming.timestamp), uint32(incoming.sequenceNumber)}
	s.latestIncoming = s.firstIncoming

	// Calculate the delta between the current packet and the previous one.
	var delta ExpandedPacketIdentifiers

	// If this is not the first packet overall, then is a gap of 1 seqnum to signify
	// to the decoder that the previous frame was (probably) incomplete. That's why
	// there's a 2 for the seqnum.
	if s.ssrc != 0 {
		delta = ExpandedPacketIdentifiers{1, 2}
	} else {
		// We make an exception for the very first packet that we're forwarding
		// so that we start with 0 seqnum and 0 timestamp.
		delta = ExpandedPacketIdentifiers{0, 0}
	}

	// Calculate the outgoing IDs of the current packet
	// as well as our new "base" for calculation of timestamps.
	outgoingIDs := latestOutgoing.Add(delta)
	s.firstOutgoing = outgoingIDs

	return outgoingIDs
}
