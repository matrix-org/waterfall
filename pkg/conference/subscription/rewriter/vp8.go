package rewriter

import (
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

// Determines if a given packet contains a VP8 keyframe.
func IsVP8Keyframe(packet rtp.Packet) bool {
	// First try to parse the packet as a VP8 packet.
	vp8Packet := codecs.VP8Packet{}

	payload, err := vp8Packet.Unmarshal(packet.Payload)
	if err != nil {
		return false
	}

	// At this point we know that we're dealing with a VP8 packet
	// and we have a so-called "VP8 Payload Descriptor" that Pion
	// parses into the `vp8Packet` structure. This is not to be
	// confused with the "VP8 Payload Header", one bit of which
	// we're parsing here. The P bit is set to 0 for the key frames.
	Pbit := (payload[0] & 0x01)

	// We also must check that the S bit from the VP8 Payload Descriptor
	// is set to 1 as S denotes a start of a new VP8 partition. Typically
	// key frames have it set to 1.
	return vp8Packet.S == 1 && Pbit == 0
}
