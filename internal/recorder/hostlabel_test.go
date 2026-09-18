package recorder

import (
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// The hostname a tape carries (TTP-93).
//
// A tape is made to be posted and the hostname is the one field in it that
// names a place rather than a measurement. rig-log's rule is that nothing
// public carries one; before --host-label the only way to honour it was to
// gunzip a tape, rewrite two fields and re-render, and on 2026-09-17 that
// manual step was skipped and a real name reached a working tree.

// TestHostLabelReplacesTheNameAndSaysItIsALabel: what the operator gave wins
// over what /proc said, and the tape records which it was — a label is not an
// observation, and two runs both labelled "workstation" are not evidence they
// ran on one machine.
//
// FAIL-first: with apply returning before it writes HostnameSource, the
// observed and labelled cases both report "" and four subtests fail.
func TestHostLabelReplacesTheNameAndSaysItIsALabel(t *testing.T) {
	for _, tc := range []struct {
		name       string
		label      HostLabel
		read       string
		wantName   string
		wantSource string
	}{{
		name:       "no flag keeps what the machine said",
		label:      HostLabel{},
		read:       "ws",
		wantName:   "ws",
		wantSource: tape.HostnameObserved,
	}, {
		name:       "a label replaces it",
		label:      HostLabel{Set: true, Text: "workstation"},
		read:       "ws",
		wantName:   "workstation",
		wantSource: tape.HostnameLabelled,
	}, {
		// --host-label "" is a deliberate "store no name", which is why Set is
		// separate from Text. The card leaves the machine out of its ENGINE
		// line for an empty hostname rather than printing "?", so this needs
		// no card change.
		name:       "an empty label stores no name at all",
		label:      HostLabel{Set: true, Text: ""},
		read:       "ws",
		wantName:   "",
		wantSource: tape.HostnameLabelled,
	}, {
		// /proc unreadable. Nothing was observed, so nothing is claimed —
		// "" is both "not recorded" and "this tape predates the field".
		name:       "an unreadable host claims nothing",
		label:      HostLabel{},
		read:       "",
		wantName:   "",
		wantSource: "",
	}, {
		name:       "a label stands in even when nothing could be read",
		label:      HostLabel{Set: true, Text: "workstation"},
		read:       "",
		wantName:   "workstation",
		wantSource: tape.HostnameLabelled,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			h := tape.HostInfo{Hostname: tc.read}
			tc.label.apply(&h)
			if h.Hostname != tc.wantName || h.HostnameSource != tc.wantSource {
				t.Errorf("apply() = (%q, %q), want (%q, %q)",
					h.Hostname, h.HostnameSource, tc.wantName, tc.wantSource)
			}
		})
	}
}
