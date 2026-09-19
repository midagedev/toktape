// tapefigures re-runs the reducer over a recorded tape and prints what it
// gets beside what the tape stored.
//
// It exists because TTP-138 changed what "the aggregate" means, and the only
// honest way to check an arithmetic change is to run it over a real run and
// compare with the same figure computed independently. That check found the
// window figure agreeing with a hand computation to four significant figures
// while the whole-wall figure stayed byte-identical — which is the whole
// claim of that change, and neither half of it is visible from a test
// fixture.
//
// It is also what the published corpus needs: `toktape reindex` can give a
// run recorded before the window existed its window, because the per-token
// timestamps survive publication, and this is how to see what a run would
// gain before rewriting anything.
//
// Usage: go run ./tools/tapefigures <tape>...
package main

import (
	"fmt"
	"os"

	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: tapefigures <tape>...")
		os.Exit(2)
	}
	status := 0
	for _, path := range os.Args[1:] {
		if err := one(path); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			status = 1
		}
	}
	os.Exit(status)
}

func one(path string) error {
	tp, err := tape.Read(path)
	if err != nil {
		return err
	}
	s := &tp.Summary
	// The reducer, run again over the requests this tape carries. Where the
	// stored figure and this one differ, the tape was written by a binary
	// older than the arithmetic — which is the case this tool is for.
	a := server.Aggregate(tp.Requests)

	fmt.Printf("%s\n", s.ID)
	fmt.Printf("  model            %s · %s\n", s.Model.Name, s.Model.Quant)
	fmt.Printf("  streams          %d\n", a.Streams)
	fmt.Printf("  whole wall       %.1f tok/s over %.2f s · %.1f each · %d x %.1f = %.1f\n",
		a.AggregatePredictedPerSecond, a.WallMs/1000,
		a.PerStreamPredictedPerSecond, a.Streams,
		a.PerStreamPredictedPerSecond, a.PerStreamPredictedPerSecond*float64(a.Streams))
	if a.ConcurrentWindowMs > 0 {
		fmt.Printf("  all %d decoding   %.1f tok/s over %.2f s · %d tokens\n",
			a.Streams, a.ConcurrentPredictedPerSecond,
			a.ConcurrentWindowMs/1000, a.ConcurrentPredictedN)
	} else {
		fmt.Printf("  all %d decoding   ? (no window: fewer than two answered streams, or none overlap)\n", a.Streams)
	}
	fmt.Printf("  stored aggregate %.1f tok/s", s.Aggregate.AggregatePredictedPerSecond)
	if s.Aggregate.ConcurrentPredictedPerSecond > 0 {
		fmt.Printf(" · stored window %.1f tok/s", s.Aggregate.ConcurrentPredictedPerSecond)
	} else {
		fmt.Printf(" · stored window ? (recorded before the field)")
	}
	fmt.Println()
	fmt.Printf("  endings          %d capped of %d observed",
		s.Limit.CappedStreams, s.Limit.EndingsObserved)
	if s.Limit.EndingsObserved == 0 {
		fmt.Printf("  (this tape cannot say)")
	}
	fmt.Println()
	if p := s.Probe; p != nil {
		fmt.Printf("  probe            %.0f tok/s + %.0f ms fixed, from %d points\n",
			p.PrefillPerSecond, p.FixedMs, len(p.Prefill))
	} else {
		fmt.Printf("  probe            ? (this run did not probe)\n")
	}
	return nil
}
