package recorder

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// Speculative n_max sweeps (TTP-35, 2026-09-13).
//
// A speculative-decoding server takes the draft block size per request:
// "speculative.n_max" in the body overrides --draft-max for that request
// (measured on ik_llama.cpp 7b79b229; mainline llama-server reads the same
// key). Which block size is fastest depends on the prompt — a draft accepted
// 87 % of the time on SQL can afford long guesses, one accepted 13 % on prose
// cannot — so `record --spec-n-max 3,5` runs the whole prompt set once per
// value into one tape, and the card prints one line per value.
//
// A sweep is an expansion, not a run loop of its own: every value becomes a
// copy of the prompt rounds with the key set on each prompt, and the
// multi-round path records them exactly as it records a prompts file.

// specNMaxParam is the request-body key a sweep sets on every prompt.
const specNMaxParam = "speculative.n_max"

// ParseSpecNMax parses the --spec-n-max list: comma-separated positive
// integers such as "3,5", in the order they are to run. Every error names the
// element it is about, and a value listed twice is an error rather than a
// second identical pass, because it would double the run for no new figure.
func ParseSpecNMax(list string) ([]int, error) {
	if strings.TrimSpace(list) == "" {
		return nil, errors.New("empty list; give positive integers such as 3,5")
	}
	parts := strings.Split(list, ",")
	out := make([]int, 0, len(parts))
	seen := make(map[int]int, len(parts))
	for i, p := range parts {
		p = strings.TrimSpace(p)
		v, err := strconv.Atoi(p)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("element %d %q is not a positive integer", i+1, p)
		}
		if first, dup := seen[v]; dup {
			return nil, fmt.Errorf("element %d: n_max %d is listed twice (element %d)", i+1, v, first)
		}
		seen[v] = i + 1
		out = append(out, v)
	}
	return out, nil
}

// expandSweep returns, for each value of nmax in order, a copy of every round
// with speculative.n_max set on each of its prompts: all the rounds for the
// first value, then all of them for the next. The prompts are cloned, Params
// included, so the caller's rounds are never written. No values returns rounds
// unchanged.
func expandSweep(rounds []Round, nmax []int) []Round {
	if len(nmax) == 0 {
		return rounds
	}
	out := make([]Round, 0, len(rounds)*len(nmax))
	for _, v := range nmax {
		for _, rd := range rounds {
			prompts := make([]server.StreamRequest, len(rd.Prompts))
			for i, p := range rd.Prompts {
				c := cloneRequest(p)
				if c.Params == nil {
					c.Params = make(map[string]any, 1)
				}
				c.Params[specNMaxParam] = v
				prompts[i] = c
			}
			out = append(out, Round{Name: rd.Name, Prompts: prompts})
		}
	}
	return out
}

// sweepBase is the prompt set a sweep repeats, with every prompt spelled out
// so there is a request to set the key on.
//
// A run without a prompts file is one round of exactly the requests a plain
// run would send — buildRequests, so the default prompts and --n-predict come
// out the same. It matters for the cap: the rounds path lets a request's own
// max_tokens win, and the default prompts carry 320, so handing it the bare
// defaults would silently move a sweep's cap off --n-predict. A prompts-file
// round with no prompt keeps the default set it gets today.
func sweepBase(o Options) []Round {
	if len(o.Rounds) == 0 {
		return []Round{{Prompts: buildRequests(o, 0)}}
	}
	out := make([]Round, len(o.Rounds))
	for k, rd := range o.Rounds {
		out[k] = rd
		if len(rd.Prompts) == 0 {
			out[k].Prompts = server.DefaultPrompts(o.Concurrency)
		}
	}
	return out
}

// planSweep turns Options.SpecNMax into rounds. It runs after collectProcess,
// because whether a sweep is worth running depends on the server's argv: a
// server with no draft model ignores the key, so every value would measure
// the same thing. When the argv was read and names no draft, only the first
// value runs and a warning says so. When it was not read — a remote server —
// every value runs, and the acceptance figures show whether a draft did.
func (r *run) planSweep() {
	nmax := r.opts.SpecNMax
	if len(nmax) == 0 {
		return
	}
	if len(nmax) > 1 && len(r.args) > 0 && r.flags.DraftModel == "" {
		r.warn("no draft model: --spec-n-max %s ran only n_max %d", joinInts(nmax), nmax[0])
		nmax = nmax[:1]
	}
	r.opts.Rounds = expandSweep(sweepBase(r.opts), nmax)
}

// applySweep fills the run-level sweep fields of a reduced multi-round
// summary: the values that ran, from the records, and one group per value,
// from PerRound. A run that sent no speculative.n_max leaves both nil, so its
// tape is byte-identical to one recorded before sweeps existed.
//
// The values come from the records rather than from PerRound because a sweep
// cut to one value over one round has no PerRound at all, and it still ran
// that value.
func applySweep(s *tape.RunSummary, recs []tape.RequestRecord) {
	s.SpecNMax = sweepValues(recs)
	s.BySpecNMax = sweepGroups(s.PerRound)
}

// sweepValues is every distinct speculative.n_max the records were sent with,
// in the order they first appear; nil when none carried one.
func sweepValues(recs []tape.RequestRecord) []int {
	var out []int
	for _, rec := range recs {
		v := specNMaxOf(rec.Prompt.Params)
		if v > 0 && !containsInt(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// sweepGroups is one SpecNMaxGroup per value the rounds carry, in the order
// the values first appear, each reduced by roundSpread exactly as the run's
// own Spread is. Rounds without a value are not a group; no value at all is
// nil.
func sweepGroups(per []tape.RoundSummary) []tape.SpecNMaxGroup {
	var order []int
	by := map[int][]tape.RoundSummary{}
	for _, p := range per {
		if p.SpecNMax <= 0 {
			continue
		}
		if _, ok := by[p.SpecNMax]; !ok {
			order = append(order, p.SpecNMax)
		}
		by[p.SpecNMax] = append(by[p.SpecNMax], p)
	}
	if len(order) == 0 {
		return nil
	}
	out := make([]tape.SpecNMaxGroup, 0, len(order))
	for _, v := range order {
		out = append(out, tape.SpecNMaxGroup{
			NMax:   v,
			Rounds: len(by[v]),
			Spread: *roundSpread(by[v]),
		})
	}
	return out
}

// roundSpecNMax is the speculative.n_max a round was sent with: the first of
// its records that carries one, because a stream that failed before the
// server answered may have no parameters recorded. 0 when none does.
func roundSpecNMax(recs []tape.RequestRecord) int {
	for _, rec := range recs {
		if v := specNMaxOf(rec.Prompt.Params); v > 0 {
			return v
		}
	}
	return 0
}

// specNMaxOf reads speculative.n_max out of a recorded parameter map. In
// process the value is the int expandSweep set; read back from a tape it is a
// JSON number, which encoding/json decodes as float64. Anything that is not a
// positive whole number is 0, never a guess.
func specNMaxOf(params map[string]any) int {
	switch v := params[specNMaxParam].(type) {
	case int:
		if v > 0 {
			return v
		}
	case int64:
		if v > 0 && v <= math.MaxInt32 {
			return int(v)
		}
	case float64:
		if v > 0 && v <= math.MaxInt32 && v == math.Trunc(v) {
			return int(v)
		}
	case json.Number:
		if n, err := v.Int64(); err == nil && n > 0 && n <= math.MaxInt32 {
			return int(n)
		}
	}
	return 0
}

func joinInts(vs []int) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ",")
}

func containsInt(vs []int, v int) bool {
	for _, x := range vs {
		if x == v {
			return true
		}
	}
	return false
}
