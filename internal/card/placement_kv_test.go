package card

import (
	"strings"
	"testing"
)

// TTP-137 (2026-09-19): the KV-cache size the recorder lifted from the
// server's own load log renders in the memory block, and a run whose log
// said nothing prints the "?" every unobserved figure prints — never a
// zero that would read as "no cache". This is a pin, not a FAIL-first gate:
// the recorder side of the figure landed with its own commit and the row
// already had the slot; what this pins is the two shapes of the slot.
func TestKVSizeRendersOrStaysUnknown(t *testing.T) {
	out := Text(Example())
	if !strings.Contains(out, "kv 2.6") {
		t.Errorf("the memory block does not carry the server's own KV size:\n%s", out)
	}
	s := Example()
	s.Placement.VRAMKVBytes = 0
	if out := Text(s); !strings.Contains(out, "kv ?") {
		t.Errorf("an unobserved KV size must print as ?, never as zero:\n%s", out)
	}
}
