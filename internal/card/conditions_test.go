package card

import (
	"reflect"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// witnessAt is one edge reading with only the TTP-57 figures filled: the rest
// of a witness says how busy the box was, which is a different verdict.
func witnessAt(round int, edge string, capKHz int64, tempC float64, sensor string) tape.ContentionWitness {
	return tape.ContentionWitness{Round: round, Edge: edge, CPUMaxKHz: capKHz, TempC: tempC, TempSensor: sensor}
}

// summaryWith is a run summary carrying ws as its witnesses.
func summaryWith(ws ...tape.ContentionWitness) *tape.RunSummary {
	s := &tape.RunSummary{}
	s.Contention.Witnesses = ws
	return s
}

// TestConditionsLine (TTP-57, 2026-09-14): the card warns when the machine at
// the end of the run was not the machine at the start, and stays silent
// otherwise — including when it has nothing to compare.
func TestConditionsLine(t *testing.T) {
	const k10 = "k10temp Tctl"
	tests := []struct {
		name string
		ws   []tape.ContentionWitness
		want string
	}{
		{
			name: "no witnesses",
			ws:   nil,
			want: "",
		},
		{
			name: "identical edges",
			ws: []tape.ContentionWitness{
				witnessAt(1, "start", 3600000, 41, k10),
				witnessAt(1, "end", 3600000, 41, k10),
			},
			want: "",
		},
		{
			name: "the ticket's run: the watchdog dropped the cap and the box got hot",
			ws: []tape.ContentionWitness{
				witnessAt(1, "start", 3600000, 41, k10),
				witnessAt(1, "end", 3600000, 44, k10),
				witnessAt(2, "start", 3600000, 47, k10),
				witnessAt(2, "end", 2700000, 50, k10),
			},
			want: "conditions changed: CPU cap 3.6 → 2.7 GHz · k10temp Tctl 41 → 50 °C",
		},
		{
			name: "cap drop alone",
			ws: []tape.ContentionWitness{
				witnessAt(0, "start", 3600000, 0, ""),
				witnessAt(0, "end", 2700000, 0, ""),
			},
			want: "conditions changed: CPU cap 3.6 → 2.7 GHz",
		},
		{
			// Re-pinned 2026-09-21 (TTP-140): this used to want the clause.
			// A machine that decoded for a minute and ended 9 °C warmer at a
			// cap that never moved is the null case — both runs recorded on
			// 2026-09-19 that did real work carried exactly this line, and a
			// caveat that fires on every working run is not read. The
			// deliberate behaviour change is argued in tempClause's doc.
			name: "a temperature rise at a steady cap is the machine working",
			ws: []tape.ContentionWitness{
				witnessAt(0, "start", 3600000, 41, k10),
				witnessAt(0, "end", 3600000, 50, k10),
			},
			want: "",
		},
		{
			// The reading this line could not make before (TTP-140): cooler
			// at the end than at the start means it was hotter when the run
			// began than the run ever made it, i.e. it was still shedding
			// heat from something else. "Was already warm when it started" is
			// the distinction the caveat's name claims to make, and this is
			// the only way two edges can see it.
			name: "a temperature fall is a machine that started hot",
			ws: []tape.ContentionWitness{
				witnessAt(0, "start", 3600000, 62, k10),
				witnessAt(0, "end", 3600000, 50, k10),
			},
			want: "conditions changed: k10temp Tctl 62 → 50 °C",
		},
		{
			// A rise still speaks when there is a cap movement for it to
			// explain — which is what the temperature was always for
			// (conditions.go's opening).
			name: "a temperature rise beside a cap that moved",
			ws: []tape.ContentionWitness{
				witnessAt(0, "start", 3600000, 41, k10),
				witnessAt(0, "end", 2700000, 50, k10),
			},
			want: "conditions changed: CPU cap 3.6 → 2.7 GHz · k10temp Tctl 41 → 50 °C",
		},
		{
			name: "a cap that went up is a change too",
			ws: []tape.ContentionWitness{
				witnessAt(0, "start", 2700000, 0, ""),
				witnessAt(0, "end", 3600000, 0, ""),
			},
			want: "conditions changed: CPU cap 2.7 → 3.6 GHz",
		},
		{
			name: "cap moved less than 1 %",
			ws: []tape.ContentionWitness{
				witnessAt(0, "start", 3600000, 0, ""),
				witnessAt(0, "end", 3590000, 0, ""),
			},
			want: "",
		},
		{
			name: "a 1.1 % move gets the decimal it needs",
			ws: []tape.ContentionWitness{
				witnessAt(0, "start", 3600000, 0, ""),
				witnessAt(0, "end", 3560000, 0, ""),
			},
			want: "conditions changed: CPU cap 3.60 → 3.56 GHz",
		},
		{
			name: "temperature moved less than 5 °C",
			ws: []tape.ContentionWitness{
				witnessAt(0, "start", 3600000, 41, k10),
				witnessAt(0, "end", 3600000, 44.9, k10),
			},
			want: "",
		},
		{
			name: "a different sensor at each edge is not a difference",
			ws: []tape.ContentionWitness{
				witnessAt(0, "start", 3600000, 41, k10),
				witnessAt(0, "end", 3600000, 50, "nvme Composite"),
			},
			want: "",
		},
		{
			name: "no sensor at either edge",
			ws: []tape.ContentionWitness{
				witnessAt(0, "start", 3600000, 0, ""),
				witnessAt(0, "end", 3600000, 0, ""),
			},
			want: "",
		},
		{
			name: "a cap unread at one edge is not a drop to zero",
			ws: []tape.ContentionWitness{
				witnessAt(0, "start", 3600000, 0, ""),
				witnessAt(0, "end", 0, 0, ""),
			},
			want: "",
		},
		{
			name: "only a start witness",
			ws: []tape.ContentionWitness{
				witnessAt(0, "start", 3600000, 41, k10),
			},
			want: "",
		},
		{
			name: "only an end witness",
			ws: []tape.ContentionWitness{
				witnessAt(0, "end", 2700000, 50, k10),
			},
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := conditionsLine(summaryWith(tc.ws...)); got != tc.want {
				t.Errorf("conditionsLine =\n %q\nwant %q", got, tc.want)
			}
		})
	}
	if got := conditionsLine(nil); got != "" {
		t.Errorf("conditionsLine(nil) = %q, want \"\"", got)
	}
}

// TestConditionsLinesFitTheHostBlock: the full line is wider than the HOST
// block, so the printable form wraps on the clause separator instead of being
// truncated mid-figure.
func TestConditionsLinesFitTheHostBlock(t *testing.T) {
	const k10 = "k10temp Tctl"
	s := summaryWith(
		witnessAt(1, "start", 3600000, 41, k10),
		witnessAt(2, "end", 2700000, 50, k10),
	)
	full := conditionsLine(s)
	avail := innerWidth - blockLabelW
	if Width(full) <= avail {
		t.Fatalf("the example line is %d columns and fits in %d: the wrap is untested", Width(full), avail)
	}

	lines := conditionsLines(s)
	want := []string{"conditions changed: CPU cap 3.6 → 2.7 GHz", "k10temp Tctl 41 → 50 °C"}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("conditionsLines =\n %q\nwant %q", lines, want)
	}
	for _, l := range lines {
		if Width(l) > avail {
			t.Errorf("line %q is %d columns, want at most %d", l, Width(l), avail)
		}
	}
	// Nothing is lost in the wrap: every figure of the one-line form is still
	// printed.
	for _, part := range []string{"3.6 → 2.7 GHz", "41 → 50 °C"} {
		if !strings.Contains(strings.Join(lines, " "), part) {
			t.Errorf("the wrapped lines %q dropped %q", lines, part)
		}
	}
	if conditionsLines(summaryWith()) != nil {
		t.Error("conditionsLines with no witnesses: want nil")
	}
}

// TestConditionsShortDropsTheSentence (TTP-57, 2026-09-14). The PNG's
// environment line has one line and no room for "conditions changed:", so the
// short form is the clauses alone, and empty when nothing changed — a caller
// that joins its parts drops it the way it drops any unobserved element.
func TestConditionsShort(t *testing.T) {
	quiet := &tape.RunSummary{Contention: tape.ContentionInfo{Witnesses: []tape.ContentionWitness{
		{Edge: "start", CPUMaxKHz: 3600000, TempC: 41, TempSensor: "k10temp Tctl"},
		{Edge: "end", CPUMaxKHz: 3600000, TempC: 42, TempSensor: "k10temp Tctl"},
	}}}
	if got := ConditionsShort(quiet); got != "" {
		t.Errorf("a machine that did not change says %q, want nothing", got)
	}
	changed := &tape.RunSummary{Contention: tape.ContentionInfo{Witnesses: []tape.ContentionWitness{
		{Edge: "start", CPUMaxKHz: 3600000, TempC: 41, TempSensor: "k10temp Tctl"},
		{Edge: "end", CPUMaxKHz: 2700000, TempC: 50, TempSensor: "k10temp Tctl"},
	}}}
	// One clause, not both: see ConditionsShort's comment for the measurement.
	const want = "cap 3.6 → 2.7 GHz"
	if got := ConditionsShort(changed); got != want {
		t.Errorf("ConditionsShort = %q, want %q", got, want)
	}
	if got := conditionsLine(changed); got != conditionsPrefix+"CPU cap 3.6 → 2.7 GHz · k10temp Tctl 41 → 50 °C" {
		t.Errorf("the long form lost a clause or its sentence: %q", got)
	}
	// Re-pinned 2026-09-21 (TTP-140). This used to hand ConditionsShort a
	// warm-up and want the clause back; a rise at a steady cap now says
	// nothing, so the case that exercises "the short form can carry a
	// temperature-only clause" has to be one that still is one — a fall.
	cooling := &tape.RunSummary{Contention: tape.ContentionInfo{Witnesses: []tape.ContentionWitness{
		{Edge: "start", CPUMaxKHz: 3600000, TempC: 62, TempSensor: "k10temp Tctl"},
		{Edge: "end", CPUMaxKHz: 3600000, TempC: 50, TempSensor: "k10temp Tctl"},
	}}}
	if got := ConditionsShort(cooling); got != "k10temp Tctl 62 → 50 °C" {
		t.Errorf("a temperature-only change says %q", got)
	}
	// And the warm-up itself reaches neither form: one predicate, both
	// renderings, so the PNG's environment line and the text card cannot
	// disagree about whether this run's machine changed.
	warmUp := &tape.RunSummary{Contention: tape.ContentionInfo{Witnesses: []tape.ContentionWitness{
		{Edge: "start", CPUMaxKHz: 3600000, TempC: 41, TempSensor: "k10temp Tctl"},
		{Edge: "end", CPUMaxKHz: 3600000, TempC: 55, TempSensor: "k10temp Tctl"},
	}}}
	if got := ConditionsShort(warmUp); got != "" {
		t.Errorf("an ordinary warm-up still says %q", got)
	}
	if got := conditionsLine(warmUp); got != "" {
		t.Errorf("an ordinary warm-up still says %q on the text card", got)
	}
}
