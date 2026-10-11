package card

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TTP-178 (2026-10-11): a host whose CPU and GPU share one memory pool — a DGX
// Spark's GB10, Apple Silicon — read as "offloaded", "all in host RAM" or
// "VRAM ?" on the card, and a Spark's pool was one bug away from being counted
// twice. The card now says unified, from the host's part names alone.
//
// FAIL-first: this file ran against the unchanged card and failed on every
// assertion below (the GB10 card printed "GB10 · … · 122 GB" with no unified
// word, and "VRAM GPU0 0.0/? GiB").

// block returns the lines of one labelled block of the text card, gutter
// stripped. A block runs until the next line with a non-blank gutter.
func block(card, label string) []string {
	var out []string
	keep := false
	for _, line := range strings.Split(card, "\n") {
		body := strings.TrimSuffix(strings.TrimPrefix(line, "│ "), " │")
		switch {
		case strings.HasPrefix(body, label+" "):
			keep = true
		case keep && !strings.HasPrefix(body, strings.Repeat(" ", blockLabelW)):
			keep = false
		}
		if keep {
			out = append(out, strings.TrimSpace(strings.TrimPrefix(body, label)))
		}
	}
	return out
}

var capacityRE = regexp.MustCompile(`(\d+(?:\.\d+)?) ?(?:GB|GiB|G)\b`)

// capacities are the memory-size figures RIG and MEMORY print as a size a
// machine HAS (a card's VRAM, the RAM, the pool), not as a size something
// used or was placed: RIG's whole body, and MEMORY's Unified and per-GPU
// lines. Each is rounded to whole GiB and de-duplicated, because the same
// pool printed twice in two units is one figure.
func capacities(card string) map[int]bool {
	got := map[int]bool{}
	add := func(s string) {
		for _, m := range capacityRE.FindAllStringSubmatch(s, -1) {
			v, _ := strconv.ParseFloat(m[1], 64)
			got[int(math.Round(v))] = true
		}
	}
	for _, l := range block(card, "RIG") {
		add(l)
	}
	for _, l := range block(card, "MEMORY") {
		if strings.HasPrefix(l, "Unified") || strings.HasPrefix(l, "GPU") || strings.HasPrefix(l, "VRAM") {
			add(l)
		}
	}
	return got
}

// hostileGB10 is a GB10 tape whose GPU reports the pool as its own VRAM — the
// double count the class of bug is about.
func hostileGB10() *tape.RunSummary {
	s := ExampleGB10()
	s.Host.GPUs[0].VRAMBytes = s.Host.RAMBytes
	s.GPUsAtEnd[0].UsedBytes = 50 << 30
	return s
}

// TestUnifiedMemoryIsCountedOnce is the recurrence guard: whatever a tape
// says, the capacities RIG and MEMORY print, taken once each, never sum above
// the one pool, and none of the words that were wrong on a unified host return.
func TestUnifiedMemoryIsCountedOnce(t *testing.T) {
	for name, s := range map[string]*tape.RunSummary{
		"example":              ExampleGB10(),
		"gpu reports the pool": hostileGB10(),
	} {
		t.Run(name, func(t *testing.T) {
			card := Text(s)
			var sum int
			for v := range capacities(card) {
				sum += v
			}
			pool := int(math.Ceil(float64(s.Host.RAMBytes) / gib))
			if sum == 0 || sum > pool {
				t.Errorf("RIG and MEMORY print %d GiB of distinct capacity for a %d GiB pool\n%s", sum, pool, card)
			}
			for _, bad := range []string{"offloaded", "0 GB", "0.0 GiB", "[N/A]", "all in host RAM", "VRAM ?",
				// A GB10 reports no memory.used, so a hold of 0 is unread and
				// not a contradiction of the placement estimate: the first
				// draft of this card warned "GPU0 … held ?" on every Spark.
				"held ?", "slower",
				// One pool, no host leg: "≈ 19 GB/s from RAM" named a bus that is not there.
				"from RAM", "ram "} {
				if strings.Contains(card, bad) {
					t.Errorf("card contains %q\n%s", bad, card)
				}
			}
			// The gate above de-duplicates, so a GPU group that printed the
			// pool as its own size beside the RAM segment would pass it. The
			// GPU group of a unified part is its name and nothing else.
			if rig := strings.Join(block(card, "RIG"), " "); !strings.HasPrefix(rig, "GB10 · ") {
				t.Errorf("RIG GPU group carries a figure: %q", rig)
			}
			if !strings.Contains(strings.Join(block(card, "RIG"), " "), "unified") {
				t.Errorf("RIG does not say unified: %v", block(card, "RIG"))
			}
			mem := strings.Join(block(card, "MEMORY"), "\n")
			for _, want := range []string{"Unified 121.7 GiB (one pool, CPU and GPU)", "(unified memory)"} {
				if !strings.Contains(mem, want) {
					t.Errorf("MEMORY lacks %q:\n%s", want, mem)
				}
			}
		})
	}
}

// TestUnifiedFromAnOldTape: nothing in the tape says unified — a Spark
// recorded by a binary from before this shipped has exactly the fields
// ExampleGB10 sets — so a tape decoded from today's JSON reads as unified.
func TestUnifiedFromAnOldTape(t *testing.T) {
	b, err := json.Marshal(ExampleGB10())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"unified", "memory_shape"} {
		if strings.Contains(string(b), key) {
			t.Fatalf("the schema grew a %q field; this gate is for tapes that have none", key)
		}
	}
	var old tape.RunSummary
	if err := json.Unmarshal(b, &old); err != nil {
		t.Fatal(err)
	}
	if card := Text(&old); !strings.Contains(card, "unified") || strings.Contains(card, "offloaded") {
		t.Errorf("a decoded old GB10 tape does not read as unified:\n%s", card)
	}
}

// TestUnifiedOnAMac: the chat-e2e goldens are Apple M1 Pro runs that printed
// "? · Apple M1 Pro · 32 GB" and "VRAM ?" for a model that ran on Metal.
func TestUnifiedOnAMac(t *testing.T) {
	s := ExampleGB10()
	s.Host = tape.HostInfo{OS: "darwin", CPU: "Apple M1 Pro", RAMBytes: 34359738368}
	s.GPUsAtEnd = nil
	card := Text(s)
	if rig := strings.Join(block(card, "RIG"), " "); rig != "Apple M1 Pro · 32 GB unified" {
		t.Errorf("RIG = %q", rig)
	}
	if strings.Contains(card, "VRAM") || strings.Contains(card, "?  ·") {
		t.Errorf("a Mac card still prints a VRAM unknown:\n%s", card)
	}
}

// TestDiscreteRigUnchanged: a discrete host prints no unified word anywhere.
func TestDiscreteRigUnchanged(t *testing.T) {
	if card := Text(Example()); strings.Contains(card, "nified") {
		t.Errorf("a two-3090 card says unified:\n%s", card)
	}
}
