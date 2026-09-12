package procmon

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/midagedev/toktape/internal/tape"
)

// Sampler reads one process's counters repeatedly for the length of a run.
//
// It exists because the two readings happen at different rates. The full
// memory picture is taken once per tape.DefaultSampleInterval, but the major
// fault counter is read on arrival of every generated token — that delta is
// the sparkline, and it is the most watched number in the clip. So the hot
// path touches only /proc/<pid>/stat, a single short line, through a file
// handle opened once: procfs regenerates the contents on each read at offset
// 0, so one ReadAt per token costs one syscall and no path walk.
//
// A Sampler is not safe for concurrent use.
type Sampler struct {
	fsRoot string
	pid    int
	stat   *os.File
	buf    []byte

	have             bool
	lastMaj, lastMin uint64
}

// NewSampler opens a sampler for pid on the live /proc. Off Linux it returns
// an error wrapping ErrUnsupported.
func NewSampler(pid int) (*Sampler, error) {
	root, err := defaultRoot()
	if err != nil {
		return nil, fmt.Errorf("procmon: sampler for pid %d: %w", pid, err)
	}
	return NewSamplerAt(root, pid)
}

// NewSamplerAt opens a sampler for pid under fsRoot. Tests use it against a
// fixture tree; the recorder uses NewSampler.
func NewSamplerAt(fsRoot string, pid int) (*Sampler, error) {
	p := pidPath(fsRoot, pid, "stat")
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("procmon: pid %d: %w", pid, ErrNotFound)
		}
		return nil, fmt.Errorf("procmon: open %s: %w", p, err)
	}
	return &Sampler{fsRoot: fsRoot, pid: pid, stat: f, buf: make([]byte, 2048)}, nil
}

// PID is the process this sampler watches.
func (s *Sampler) PID() int { return s.pid }

// Close releases the stat handle.
func (s *Sampler) Close() error {
	if s == nil || s.stat == nil {
		return nil
	}
	err := s.stat.Close()
	s.stat = nil
	return err
}

// readStat re-reads /proc/<pid>/stat from offset 0.
func (s *Sampler) readStat() (Stat, error) {
	if s.stat == nil {
		return Stat{}, fmt.Errorf("procmon: sampler for pid %d is closed", s.pid)
	}
	for {
		n, err := s.stat.ReadAt(s.buf, 0)
		if err != nil && !errors.Is(err, io.EOF) {
			return Stat{}, fmt.Errorf("procmon: read stat for pid %d: %w", s.pid, err)
		}
		if n == len(s.buf) {
			// The line filled the buffer, so it may have been cut short.
			s.buf = make([]byte, len(s.buf)*2)
			continue
		}
		return ParseStat(s.buf[:n])
	}
}

// Faults returns the cumulative major and minor fault counters. This is the
// per-token hot path: one read of one file.
func (s *Sampler) Faults() (maj, minor uint64, err error) {
	st, err := s.readStat()
	if err != nil {
		return 0, 0, err
	}
	s.have, s.lastMaj, s.lastMin = true, st.MajFaults, st.MinFaults
	return st.MajFaults, st.MinFaults, nil
}

// FaultDelta returns the faults taken since the previous Faults, FaultDelta,
// MajDelta or Sample call, and latches the new values. The first call after
// the sampler is opened has nothing to subtract from and returns 0, 0 — so the
// recorder should call it once when the request is sent, and then once per
// token, which makes the first token's delta the prompt phase.
func (s *Sampler) FaultDelta() (majDelta, minDelta uint64, err error) {
	prevMaj, prevMin, had := s.lastMaj, s.lastMin, s.have
	maj, minor, err := s.Faults()
	if err != nil {
		return 0, 0, err
	}
	if !had {
		return 0, 0, nil
	}
	return sub(maj, prevMaj), sub(minor, prevMin), nil
}

// MajDelta returns only the major-fault delta: the sparkline's sample.
func (s *Sampler) MajDelta() (uint64, error) {
	maj, _, err := s.FaultDelta()
	return maj, err
}

// Sample reads the full memory picture — status and stat — for one
// tape.RunSample. It latches the fault counters like Faults does, so a
// periodic Sample and the per-token MajDelta share one timeline with no
// double counting.
func (s *Sampler) Sample() (tape.MemSample, error) {
	statusData, err := readPIDFile(s.fsRoot, s.pid, "status")
	if err != nil {
		return tape.MemSample{}, err
	}
	st, err := ParseStatus(statusData)
	if err != nil {
		return tape.MemSample{}, err
	}
	stat, err := s.readStat()
	if err != nil {
		return tape.MemSample{}, err
	}
	s.have, s.lastMaj, s.lastMin = true, stat.MajFaults, stat.MinFaults
	return memSample(st, stat), nil
}

// sub is a - b, clamped at 0: the counters only ever rise, but a PID that was
// reused would otherwise produce a nonsense delta.
func sub(a, b uint64) uint64 {
	if a < b {
		return 0
	}
	return a - b
}
