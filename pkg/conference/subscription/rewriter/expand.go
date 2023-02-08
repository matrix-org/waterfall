package rewriter

// Expands a counter (whether it's a sequence number, timestamp or any other counter)
// into an expanded counter using the latest observed counter value for a calculation.
// It's impossible to reliably extend the counter without knowing the "max" value of
// the previously observed counter (i.e. the latest counter value). This function also
// updates the value of the latest observed counter. Returns expanded counter value
// that can be casted to any smaller type. The `width` defines the width of the
// truncated counter in bits.
func ExpandCounter(truncated, width uint64, latest *uint64) uint64 {
	// Mask that selects the truncated counter actual value.
	var mask uint64 = 1<<width - 1

	// Really big number that is close to the maximum possible value of the truncated counter.
	var reallyBig uint64 = 1 << (width - 1)

	// The latest observed counter value without ROC taken into account.
	var truncatedLatest uint64 = *latest & mask

	// The current value of ROC inside the `latest` counter.
	var latestROC uint64 = *latest >> width

	// Calculated value of ROC that we must use for the expansion of our truncated counter.
	var ROC uint64

	// Calculate the new value of ROC.
	if truncatedLatest > truncated && truncatedLatest-truncated > reallyBig {
		// Truncated counter is much smaller than the latest observed counter value. Likely a rollover.
		ROC = latestROC + 1
	} else if latestROC > 0 && truncated > truncatedLatest && truncated-truncatedLatest > reallyBig {
		// Truncated counter is much bigger than the latest observed counter value. Likely a rollunder.
		ROC = latestROC - 1
	} else {
		// Truncated value is close to the latest observed value. No rollover.
		ROC = latestROC
	}

	// Expand the timestamp based on a new value of ROC.
	var expanded uint64 = ROC<<width | (truncated & mask)

	// Update the latest observed counter value if needed.
	if expanded > *latest {
		*latest = expanded
	}

	return expanded
}
