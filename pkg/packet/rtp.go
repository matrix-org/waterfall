package packet

import (
	"github.com/pion/rtp"
	"golang.org/x/exp/constraints"
)

type RewrittenRTPPacket *rtp.Packet

// A structure that is used to rewrite the RTP packets that are being forwarded.
type PacketRewriter struct {
	// The SSRC of the previously forwarded packet.
	previouslyForwardedSSRC uint32
	// SSRC that we're using for all packets that we're forwarding.
	// This is the SSRC that we're sending to the remote peer. Typically,
	// this is the SSRC of the lowest layer for the simulcast track.
	outgoingSSRC uint32
	// The highest identifiers of the forwarded packet,i.e. the IDs of
	// the **latest** (in terms of timestamp and number) packet. This is not
	// necessarily the IDs of the **last forwarded packet**, due to packets
	// that may arrive out-of-order.
	latestOutgoingIDs ExpandedPacketIdentifiers
	// The identifiers of the **first incoming packet after swithing
	// layers**. We use it to calulcate their relative position in the RTP stream.
	firstIncomingIDs ExpandedPacketIdentifiers
	// The latest identifiers of the **incoming packet** after switching the layers.
	latestIncomingIDs ExpandedPacketIdentifiers
	// The identifiers of the **first outgoing packet** after switching
	// layers. This is our "base" to calculate the proper timestamp when
	// forwarding/rewriting the packet.
	firstOutgoingIDs ExpandedPacketIdentifiers
}

// Creates a new instance of the `PacketRewriter`.
func NewPacketRewriter(outgoingSSRC uint32) *PacketRewriter {
	rewriter := new(PacketRewriter)
	rewriter.outgoingSSRC = outgoingSSRC
	return rewriter
}

// Process new incoming packet.
func (p *PacketRewriter) ProcessIncoming(packet rtp.Packet) (RewrittenRTPPacket, error) {
	// Store current incoming packet identifiers.
	incomingIDs := TruncatedPacketIdentifiers{packet.Timestamp, packet.SequenceNumber}

	// Calculated outgoing IDs of the current packet.
	outgoingIDs := ExpandedPacketIdentifiers{0, 0}

	// Check if we've just switched the layer before this packet, i.e. if
	// it is the first packet after switching layers.
	if p.previouslyForwardedSSRC != packet.SSRC {
		// Calculate the delta between the current packet and the previous one.
		// We assume that the previous packet was the last one of the previous layer.
		// These are OK to expand without any checks, as they are only used as a base
		// for the future values. In other words, we are only tracking the ROC since
		// the switching point, and that is now, so the ROC is 0.
		var delta ExpandedPacketIdentifiers

		// If this is not the first packet overall, then is a gap of 1 seqnum to signify
		// to the decoder that the previous frame was (probably) incomplete. That's why
		// there's a 2 for the seqnum.
		if p.previouslyForwardedSSRC != 0 {
			delta = ExpandedPacketIdentifiers{1, 2}
		} else {
			// We make an exception for the very first packet that we're forwarding
			// so that we start with 0 seqnum and 0 timestamp.
			delta = ExpandedPacketIdentifiers{0, 0}
		}

		// Update incoming timestamps of the first packet after switching layers.
		p.firstIncomingIDs = ExpandedPacketIdentifiers{uint64(packet.Timestamp), uint32(packet.SequenceNumber)}
		p.latestIncomingIDs = p.firstIncomingIDs

		// Calculate the outgoing IDs of the current packet
		// as well as our new "base" for calculation of timestamps.
		outgoingIDs = p.latestOutgoingIDs.Add(delta)
		p.firstOutgoingIDs = outgoingIDs

		// Update the SSRC of the previously forwarded packet.
		p.previouslyForwardedSSRC = packet.SSRC
	} else {
		// Expand the sequence number.
		latestSequenceNumber := uint64(p.latestIncomingIDs.sequenceNumber)
		expandedSequenceNumber := uint32(expandCounter(uint64(incomingIDs.sequenceNumber), 16, &latestSequenceNumber))
		p.latestIncomingIDs.sequenceNumber = uint32(latestSequenceNumber)

		// Expand the timestamp.
		expandedTimestamp := expandCounter(uint64(incomingIDs.timestamp), 32, &p.latestIncomingIDs.timestamp)

		// Expanded identifiers.
		expandedIncomingIDs := ExpandedPacketIdentifiers{expandedTimestamp, expandedSequenceNumber}

		// Now we can safely calculate the delta.
		delta := expandedIncomingIDs.Sub(p.firstIncomingIDs)

		// The outgoing IDs are the delta added to the first outgoing IDs.
		outgoingIDs = p.firstOutgoingIDs.Add(delta)
	}

	// Store the highest outgoing IDs.
	p.latestOutgoingIDs = p.latestOutgoingIDs.Max(outgoingIDs)

	// Rewrite the IDs of the incoming packet and return it.
	packet.Timestamp = uint32(outgoingIDs.timestamp)
	packet.SequenceNumber = uint16(outgoingIDs.sequenceNumber)

	// All packets within a single subscription must have the same SSRC.
	packet.SSRC = p.outgoingSSRC

	return &packet, nil
}

// Holds values required for the proper calculation of RTP IDs.
// These are the values that are being overwritten in the RTP packets.
type TruncatedPacketIdentifiers struct {
	// RTP timestamp.
	timestamp uint32
	// RTP sequence number.
	sequenceNumber uint16
}

// Add the given delta to the identifiers.
func (p TruncatedPacketIdentifiers) Add(delta TruncatedPacketIdentifiers) TruncatedPacketIdentifiers {
	return TruncatedPacketIdentifiers{
		timestamp:      p.timestamp + delta.timestamp,
		sequenceNumber: p.sequenceNumber + delta.sequenceNumber,
	}
}

// Subtract the given delta from the identifiers.
func (p TruncatedPacketIdentifiers) Sub(delta TruncatedPacketIdentifiers) TruncatedPacketIdentifiers {
	return TruncatedPacketIdentifiers{
		timestamp:      p.timestamp - delta.timestamp,
		sequenceNumber: p.sequenceNumber - delta.sequenceNumber,
	}
}

// Returns the maximum value of both.
func (p TruncatedPacketIdentifiers) Max(other TruncatedPacketIdentifiers) TruncatedPacketIdentifiers {
	return TruncatedPacketIdentifiers{
		timestamp:      max(p.timestamp, other.timestamp),
		sequenceNumber: max(p.sequenceNumber, other.sequenceNumber),
	}
}

// Expanded identifiers after taking into account the rollover.
type ExpandedPacketIdentifiers struct {
	// RTP timestamp.
	timestamp uint64
	// RTP sequence number.
	sequenceNumber uint32
}

// Add the given delta to the identifiers.
func (p ExpandedPacketIdentifiers) Add(delta ExpandedPacketIdentifiers) ExpandedPacketIdentifiers {
	return ExpandedPacketIdentifiers{
		timestamp:      p.timestamp + delta.timestamp,
		sequenceNumber: p.sequenceNumber + delta.sequenceNumber,
	}
}

// Subtract the given delta from the identifiers.
func (p ExpandedPacketIdentifiers) Sub(delta ExpandedPacketIdentifiers) ExpandedPacketIdentifiers {
	return ExpandedPacketIdentifiers{
		timestamp:      p.timestamp - delta.timestamp,
		sequenceNumber: p.sequenceNumber - delta.sequenceNumber,
	}
}

// Returns the maximum value of both.
func (p ExpandedPacketIdentifiers) Max(other ExpandedPacketIdentifiers) ExpandedPacketIdentifiers {
	return ExpandedPacketIdentifiers{
		timestamp:      max(p.timestamp, other.timestamp),
		sequenceNumber: max(p.sequenceNumber, other.sequenceNumber),
	}
}

// Go does not have a built-in generic function to get the maximum value of two values...
// It only has a library `math.Max()` function defined for float64 which is not what we want.
func max[T constraints.Ordered](a, b T) T {
	if a > b {
		return a
	}
	return b
}
