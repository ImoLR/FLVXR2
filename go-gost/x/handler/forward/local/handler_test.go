package local

import "testing"

func TestReportForwardDeltaAfterClose(t *testing.T) {
	var inReported, outReported uint64
	var lastIn, lastOut uint64
	in := func(n uint64) { inReported += n }
	out := func(n uint64) { outReported += n }

	// Two samples, then the final byte counters after a connection closes.
	reportForwardDelta(1000, &lastIn, in)
	reportForwardDelta(2000, &lastOut, out)
	reportForwardDelta(3500, &lastIn, in)
	reportForwardDelta(4000, &lastOut, out)
	reportForwardDelta(3700, &lastIn, in)
	reportForwardDelta(4300, &lastOut, out)
	if inReported != 3700 || outReported != 4300 {
		t.Fatalf("close duplicated sampled bytes: in=%d out=%d", inReported, outReported)
	}
	// A close in the same sample interval as the final tick adds no spike.
	reportForwardDelta(3700, &lastIn, in)
	reportForwardDelta(4300, &lastOut, out)
	if inReported != 3700 || outReported != 4300 {
		t.Fatalf("repeated final sample changed traffic: in=%d out=%d", inReported, outReported)
	}
}
