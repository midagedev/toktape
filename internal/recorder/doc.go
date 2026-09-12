// Package recorder drives one run: it attaches to a live server, sends N
// concurrent streams, watches the host while they generate, and reduces
// everything into a *tape.Tape.
//
// It is the only place the five collector packages meet. internal/server
// talks to the engine, internal/procmon reads /proc, internal/gpu reads the
// devices, internal/placement reads the GGUF header, and this package owns
// the order they run in, the clock they share and the degradation rules.
//
// Three rules shape it:
//
//  1. Everything degrades. A missing PID, a missing GPU, a model file that
//     is not on this host — each one appends a sentence to
//     tape.RunSummary.Warnings and the run continues. The only fatal
//     conditions are a server that cannot be reached (ErrUnreachable) and a
//     run in which every stream failed (ErrAllStreamsFailed).
//  2. Unknown is "" / 0, never a guessed default (repo rule). A collector
//     that failed leaves its fields zero; it does not substitute a plausible
//     value.
//  3. The major-fault counter is read on the arrival of every token, on the
//     stream's own goroutine, because that delta is the sparkline. The
//     readings of all streams go through one procmon.Sampler behind one
//     mutex — procmon.Sampler is explicitly not safe for concurrent use.
//
// Clock note: Options.Clock supplies only the run's wall-clock stamps
// (StartedAt, FinishedAt and therefore the run ID), so a test can pin the
// ID. Every elapsed duration — token arrivals, sample offsets — comes from
// the monotonic clock inside internal/server and from time.Now here, because
// a frozen clock must not turn a rate into a division by zero.
package recorder
