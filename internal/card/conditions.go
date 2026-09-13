package card

import (
	"math"
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/tape"
)

// The line in this file answers a question a card could not answer before
// (TTP-57, 2026-09-14): the four-stream run reported 22.6 tok/s where the same
// box at the same settings had reported 25.1, and nothing on the card said
// that a thermal watchdog had dropped the clock cap from 3.6 to 2.7 GHz
// partway through. A run whose machine changed under it was never one
// measurement, and the card has to say so rather than let the reader argue
// about the settings.
//
// The two edges compared are the first "start" witness and the last "end"
// witness of the run — the conditions the run began under and the ones it
// finished under. What happened between them is the recorder's evidence
// (Contention.Witnesses), not the card's headline.

// conditionsPrefix opens the line. It is a statement about the machine, not a
// contention verdict: a box can be perfectly quiet and still throttle.
const conditionsPrefix = "conditions changed: "

// capMovedFraction is how far the clock cap has to move to be worth printing.
// 1 % is below anything a governor or a watchdog does deliberately (the
// cheapest real step is a 100 MHz bin on a 3-4 GHz part, ~3 %) and above the
// rounding of a cap reported in kHz.
const capMovedFraction = 0.01

// tempMovedC is how far the temperature has to move to be worth printing.
// A few degrees is the normal breathing of a part under load; 5 °C is the
// point at which the reader is looking at a different thermal situation, and
// it is also comfortably above the 1 °C granularity the card prints.
const tempMovedC = 5

// conditionsLine is the one-line warning that the machine was not the same at
// the end of the run as at the start, or "" when it was (and "" whenever there
// is nothing to compare: no witnesses, only one edge, or a reading missing at
// either edge — an absent figure is not a change).
//
//	conditions changed: CPU cap 3.6 → 2.7 GHz · k10temp Tctl 41 → 50 °C
func conditionsLine(s *tape.RunSummary) string {
	clauses := conditionsClauses(s)
	if len(clauses) == 0 {
		return ""
	}
	return conditionsPrefix + strings.Join(clauses, " · ")
}

// conditionsLines is conditionsLine wrapped to the width of a labelled block,
// for the caller that prints it inside one. The whole line is 67 columns and
// the HOST block has 59, so the clauses are wrapped rather than truncated:
// a warning cut off mid-figure is worse than no warning.
func conditionsLines(s *tape.RunSummary) []string {
	clauses := conditionsClauses(s)
	if len(clauses) == 0 {
		return nil
	}
	parts := make([]string, len(clauses))
	copy(parts, clauses)
	parts[0] = conditionsPrefix + parts[0]
	return wrapJoin(parts, " · ", innerWidth-blockLabelW)
}

// conditionsClauses is what changed, one clause each, in the order the card
// prints them: the clock cap first because it is the figure that moves the
// rate, the temperature second because it is usually the reason the cap moved.
func conditionsClauses(s *tape.RunSummary) []string {
	if s == nil {
		return nil
	}
	start, end := conditionEdges(s.Contention.Witnesses)
	if start == nil || end == nil {
		return nil
	}
	var clauses []string
	if c := capClause(start.CPUMaxKHz, end.CPUMaxKHz); c != "" {
		clauses = append(clauses, c)
	}
	if c := tempClause(start, end); c != "" {
		clauses = append(clauses, c)
	}
	return clauses
}

// conditionEdges picks the first witness taken at the start of a round and the
// last taken at the end of one, over every round of the run. In a single-round
// run they are that round's two readings; in a --prompts run they span the
// whole run, which is the span the card's figures cover.
func conditionEdges(ws []tape.ContentionWitness) (start, end *tape.ContentionWitness) {
	for i := range ws {
		if ws[i].Edge == "start" {
			start = &ws[i]
			break
		}
	}
	for i := len(ws) - 1; i >= 0; i-- {
		if ws[i].Edge == "end" {
			end = &ws[i]
			break
		}
	}
	return start, end
}

// capClause is the clock-cap half, or "" when either edge went unread or the
// cap moved by less than capMovedFraction.
func capClause(startKHz, endKHz int64) string {
	if startKHz <= 0 || endKHz <= 0 {
		return ""
	}
	if math.Abs(float64(endKHz-startKHz))/float64(startKHz) < capMovedFraction {
		return ""
	}
	from, to := formatGHzPair(startKHz, endKHz)
	return "CPU cap " + from + " → " + to + " GHz"
}

// formatGHzPair renders the two caps in GHz with just enough decimals to tell
// them apart. One decimal is the card's usual precision, but a cap that moved
// 1.1 % (3.60 → 3.56 GHz) would print as "3.6 → 3.6 GHz" — a line that says
// something changed and then shows two identical numbers reads as a bug in the
// card rather than a fact about the run.
func formatGHzPair(startKHz, endKHz int64) (from, to string) {
	for prec := 1; prec <= 3; prec++ {
		from = strconv.FormatFloat(float64(startKHz)/1e6, 'f', prec, 64)
		to = strconv.FormatFloat(float64(endKHz)/1e6, 'f', prec, 64)
		if from != to {
			return from, to
		}
	}
	return from, to
}

// tempClause is the temperature half.
//
// It is printed only when both edges name the same sensor: two temperatures
// read from different chips — the CPU package at one edge, an NVMe controller
// at the other — do not subtract into anything, and a difference invented that
// way would be exactly the kind of number this repo refuses to print. An empty
// sensor is no reading at all (tape.ContentionWitness), so TempC is then not a
// figure either.
func tempClause(start, end *tape.ContentionWitness) string {
	if start.TempSensor == "" || start.TempSensor != end.TempSensor {
		return ""
	}
	if math.Abs(end.TempC-start.TempC) < tempMovedC {
		return ""
	}
	return start.TempSensor + " " +
		strconv.FormatFloat(start.TempC, 'f', 0, 64) + " → " +
		strconv.FormatFloat(end.TempC, 'f', 0, 64) + " °C"
}

// ConditionsShort is the one-clause form for a caller with a single line and
// no room for a sentence: the most significant clause alone, with "CPU cap"
// shortened to "cap". Empty when nothing changed, so a caller that joins it
// with its other parts drops it the way it drops every other unobserved
// element.
//
// It exists for the PNG's environment line, which sits beside "throttled no"
// and "contended no" — the three claims about the machine, in the order the
// reader needs them: was it capped, was it busy, did either change. One clause
// and not both, measured: on a two-GPU rig the pair pushes that line past the
// content box and the GPU state falls off the end. The clause kept is the
// clock cap, because the cap is what moved the rate and the temperature is
// only why it moved (conditionsClauses orders them that way). The full
// sentence, with every clause, is on the text card, which wraps.
func ConditionsShort(s *tape.RunSummary) string {
	clauses := conditionsClauses(s)
	if len(clauses) == 0 {
		return ""
	}
	return strings.TrimPrefix(clauses[0], "CPU ")
}
