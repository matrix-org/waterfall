package subscription

import (
	"fmt"

	"github.com/pion/rtp"
	"golang.org/x/exp/constraints"
)

type RewrittenRTPPacket *rtp.Packet

// A structure that is used to rewrite the RTP packets that are being forwarded.
type PacketRewriter struct {
	// The SSRC of the previously forwarded packet.
	previouslyForwardedSSRC uint32
	// Currently selected SSRC of the layer (this one changes over time
	// if we e.g. switch the layer that we want to forward).
	selectedSSRC uint32
	// SSRC that we're using for all packets that we're forwarding.
	// This is the SSRC that we're sending to the remote peer. Typically,
	// this is the SSRC of the lowest layer for the simulcast track.
	forwardingSSRC uint32
	// The highest identifiers of the forwarded packet,i.e. the IDs of
	// the **latest** (in terms of timestamp and number) packet. This is not
	// necessarily the IDs of the **last forwarded packet**, due to packets
	// that may arrive out-of-order.
	maxOutgoingIDs PacketIdentifiers
	// The identifiers of the **first incoming packet** after swithing
	// layers. We use it to calulcate their relative position in the RTP stream.
	firstIncomingIDs PacketIdentifiers
	// The identifiers of the **first outgoing packet** after switching
	// layers. This is our "base" to calculate the proper timestamp when
	// forwarding/rewriting the packet.
	firstOutgoingIDs PacketIdentifiers
}

// Creates a new instance of the `PacketRewriter`.
func NewPacketRewriter() PacketRewriter {
	return *new(PacketRewriter)
}

// Process new incoming packet.
func (p *PacketRewriter) ProcessIncoming(packet *rtp.Packet) (RewrittenRTPPacket, error) {
	// This is the first packet, so we select the SSRC of it and assume that it's our
	// starting point. We currently implicitly expect that the first packet is the lowest
	// layer when simulcast is enabled.
	firstPacket := (p.selectedSSRC == 0 && p.forwardingSSRC == 0)
	if firstPacket {
		p.selectedSSRC = packet.SSRC
		p.forwardingSSRC = packet.SSRC
	}

	// We received a packet with the SSRC different from the **currently selected** layer.
	// This is a mistake, we drop such a package.
	if packet.SSRC != p.selectedSSRC {
		return nil, fmt.Errorf("Bug: packet's SSRC is different from the selected one")
	}

	// Store current incoming packet identifiers.
	incomingIDs := PacketIdentifiers{
		timestamp:      packet.Timestamp,
		sequenceNumber: packet.SequenceNumber,
	}

	// Calculated outgoing IDs of the current packet.
	outgoingIDs := PacketIdentifiers{0, 0}

	// Check if we've just switched the layer before this packet, i.e. if
	// it is the first packet after switching layers.
	if p.previouslyForwardedSSRC != p.selectedSSRC {
		// There is a gap of 1 seqnum to signify to the decoder that the previous frame
		// was (probably) incomplete. That's why there's a 2 for the seqnum.
		delta := PacketIdentifiers{1, 2}

		// We make an exception for the very first packet that we're forwarding
		// so that we start with 0 seqnum and 0 timestamp for the first packet.
		if firstPacket {
			delta = PacketIdentifiers{0, 0}
		}

		// Calculate the outgoing IDs of the current packet
		// as well as our new "base" for calculation of timestamps.
		outgoingIDs = p.maxOutgoingIDs.Add(delta)
		p.firstIncomingIDs = incomingIDs
		p.firstOutgoingIDs = outgoingIDs

		// Update the SSRC of the previously forwarded packet.
		p.previouslyForwardedSSRC = p.selectedSSRC
	} else {
		delta := incomingIDs.Sub(p.firstIncomingIDs)
		outgoingIDs = p.firstOutgoingIDs.Add(delta)
	}

	// Store the highest outgoing IDs.
	p.maxOutgoingIDs = p.maxOutgoingIDs.Max(outgoingIDs)

	// Rewrite the IDs of the incoming packet and return it.
	packet.Timestamp = outgoingIDs.timestamp
	packet.SequenceNumber = outgoingIDs.sequenceNumber

	// All packets within a single subscription must have the same SSRC.
	packet.SSRC = p.forwardingSSRC

	return packet, nil
}

// Switches the layer that we're forwarding, i.e. change the new SSRC that we're expecting from
// the incoming packets. If the SSRC won't match, we'll drop the packet.
func (p *PacketRewriter) SwitchLayer(ssrc uint32) {
	p.selectedSSRC = ssrc
}

// Holds values required for the proper calculation of RTP IDs.
// These are the values that are being overwritten in the RTP packets.
type PacketIdentifiers struct {
	// RTP timestamp.
	timestamp uint32
	// RTP sequence number.
	sequenceNumber uint16
}

// Add the given delta to the identifiers.
func (p PacketIdentifiers) Add(delta PacketIdentifiers) PacketIdentifiers {
	return PacketIdentifiers{
		timestamp:      p.timestamp + delta.timestamp,
		sequenceNumber: p.sequenceNumber + delta.sequenceNumber,
	}
}

// Subtract the given delta from the identifiers.
func (p PacketIdentifiers) Sub(delta PacketIdentifiers) PacketIdentifiers {
	return PacketIdentifiers{
		timestamp:      p.timestamp - delta.timestamp,
		sequenceNumber: p.sequenceNumber - delta.sequenceNumber,
	}
}

// Returns the maximum value of both.
func (p PacketIdentifiers) Max(other PacketIdentifiers) PacketIdentifiers {
	return PacketIdentifiers{
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
