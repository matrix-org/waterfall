package rewriter

import "golang.org/x/exp/constraints"

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
