package sla

import (
	"testing"

	"github.com/Aone2233/nekomari/pkg/metric"
)

// Compile-time proof that the real store satisfies the interfaces this package reads
// through. Without it, a signature change in pkg/metric would only surface when the RPC
// handler was exercised at runtime.
var (
	_ SeriesReader      = (*metric.Store)(nil)
	_ BatchSeriesReader = (*metric.Store)(nil)
)

func TestStoreSatisfiesReaders(t *testing.T) {
	// The assertions above are the test; this keeps the file from being optimised away
	// as unused in some build modes and gives the failure a name.
}