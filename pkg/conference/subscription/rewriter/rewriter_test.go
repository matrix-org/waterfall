package rewriter_test

import (
	"testing"

	"github.com/matrix-org/waterfall/pkg/conference/subscription/rewriter"
	"github.com/pion/rtp"
)

func TestRewriter(t *testing.T) {
	cases := []struct {
		seqNum         uint16
		ts             uint32
		ssrc           uint32
		expectedSeqNum uint16
		expectedTs     uint32
	}{
		{40000, 1000000, 1111, 0, 0},           // first packet
		{50000, 1200000, 1111, 10000, 200000},  // normal increase (we're gap agnostic)
		{65000, 1500000, 1111, 25000, 500000},  // normal increase (we're gap agnostic)
		{10, 2000000, 1111, 25546, 1000000},    // wrap around (roll over)
		{20, 2000000, 1111, 25556, 1000000},    // wrap around (after a roll over)
		{50000, 2000000, 1111, 10000, 1000000}, // roll under (after a roll over)
		{30, 2000000, 1111, 25566, 1000000},    // back to normal (after a roll over)
		{10000, 20000, 2222, 25568, 1000001},   // layer switch (SSRC change)
		{10001, 20001, 2222, 25569, 1000002},   // normal packet after a layer switch
		{60001, 20002, 2222, 10033, 1000003},   // global counter wrap around (roll over)
		{60002, 20003, 2222, 10034, 1000004},   // global counter wrap around (after a roll over)
		{0, 20004, 2222, 15568, 1000005},       // normal packet
		{15000, 20005, 3333, 15570, 1000006},   // layer switch (SSRC change)
		{0, 20005, 3333, 570, 1000006},         // woohoo, double wrap around, let's go!!!
		{5, 20005, 3333, 575, 1000006},         // normal packet
		{5005, 20005, 3333, 5575, 1000006},     // normal packet
		{5006, 2000500, 3333, 5576, 2980501},   // normal packet
		{5006, 4294967295, 3333, 5576, 980000}, // global ts wrap around (roll over)
		{5006, 0, 3333, 5576, 980001},          // and local ts wrap around as well (roll over)
		{64965, 1, 3333, 65535, 980002},        // and local ts wrap around as well (after a roll over)
		{1000, 2000, 4444, 1, 980003},          // rollover while SSRC changing
	}

	rewriter := rewriter.NewPacketRewriter()
	packet := new(rtp.Packet)

	for _, c := range cases {
		packet.SequenceNumber = c.seqNum
		packet.Timestamp = c.ts
		packet.SSRC = c.ssrc

		rewritten := rewriter.ProcessIncoming(*packet)

		if rewritten.SequenceNumber != c.expectedSeqNum {
			t.Fatalf("expected seqNum %d, got %d", c.expectedSeqNum, rewritten.SequenceNumber)
		}

		if rewritten.Timestamp != c.expectedTs {
			t.Fatalf("expected ts %d, got %d", c.expectedTs, rewritten.Timestamp)
		}
	}
}
