package rewriter_test

import (
	"testing"

	"github.com/matrix-org/waterfall/pkg/peer/subscription/rewriter"
)

func TestExpandCounter(t *testing.T) {
	assert := func(condition bool) {
		if !condition {
			t.Fatal("assertion failed")
		}
	}

	var latest uint64
	var expanded uint64

	// Zero max.
	latest = 0
	expanded = rewriter.ExpandCounter(0x3, 16, &latest)
	assert(0x3 == expanded)
	assert(0x3 == latest)

	// Roll over.
	latest = 0xffff
	expanded = rewriter.ExpandCounter(0x0001, 16, &latest)
	assert(0x10001 == expanded)
	assert(0x10001 == latest)

	// Roll over larger ROC.
	latest = 0x3ffff
	expanded = rewriter.ExpandCounter(0x0001, 16, &latest)
	assert(0x40001 == expanded)
	assert(0x40001 == latest)

	// Roll under.
	latest = 0x10001
	expanded = rewriter.ExpandCounter(0xffff, 16, &latest)
	assert(0xffff == expanded)
	assert(0x10001 == latest)

	// Roll under larger ROC.
	latest = 0x30001
	expanded = rewriter.ExpandCounter(0xffff, 16, &latest)
	assert(0x2ffff == expanded)
	assert(0x30001 == latest)
}
