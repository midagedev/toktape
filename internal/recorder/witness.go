package recorder

import (
	"time"

	"github.com/midagedev/toktape/internal/procmon"
	"github.com/midagedev/toktape/internal/tape"
)

// Edges of a measurement round at which a witness is taken (TTP-36).
const (
	witnessStart = "start"
	witnessEnd   = "end"
)

// witness takes one reading of how busy the box is: the 1-minute load
// average, /proc/pressure/io some avg10, the page-cache size and every live
// llama-* process with its age.
//
// Each reading degrades on its own and silently — the witness is evidence
// attached to the verdict, not a collector whose absence is a caveat for the
// card: an unreadable load stays 0, IO pressure nil (a kernel without PSI),
// the page cache 0, and a process table that could not be scanned leaves
// ProcsRead false. An unreadable boot time only leaves the ages at 0.
func (r *run) witness(t time.Duration, round int, edge string) tape.ContentionWitness {
	fsRoot := r.opts.FSRoot
	w := tape.ContentionWitness{T: t, Round: round, Edge: edge}
	if v, err := procmon.LoadAvg(fsRoot); err == nil {
		w.LoadAvg1 = v
	}
	if v, err := procmon.ReadIOPressure(fsRoot); err == nil {
		w.IOSomeAvg10 = &v
	}
	if v, err := procmon.ReadPageCache(fsRoot); err == nil {
		w.PageCacheBytes = v
	}
	boot, err := procmon.ReadBootTime(fsRoot)
	if err != nil {
		boot = time.Time{}
	}
	if procs, err := procmon.LlamaProcs(fsRoot, r.pid, boot, procmon.ClockTicks); err == nil {
		w.ProcsRead = true
		w.LlamaProcs = procs
	}
	return w
}

// observe records a witness when the server is a process on this host. For a
// remote server this host's pressure says nothing about the measurement, so
// no witness is taken at all (tape.ContentionInfo.Witnesses doc).
//
// It runs on the goroutine that sends the rounds, before and after each
// RunConcurrent call, so r.witnesses needs no lock.
func (r *run) observe(t time.Duration, round int, edge string) {
	if r.pid <= 0 {
		return
	}
	r.witnesses = append(r.witnesses, r.witness(t, round, edge))
}

// sinceOrigin is the time since the run's StartedAt origin, or 0 before that
// origin is set. The first round's start witness is taken just before the
// origin exists — and before, not after, the later rounds' offset is measured,
// so the read of /proc never shifts a record's StartedAt.
func sinceOrigin(origin time.Time) time.Duration {
	if origin.IsZero() {
		return 0
	}
	return time.Since(origin)
}
