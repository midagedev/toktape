package card

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// 2026-09-15, ExLlamaV3: an engine behind a llama-server-protocol shim. The
// engine object is the record — model, placement and flags come from /props,
// not from a GGUF header and an argv — and the card has to read as a card,
// not as a list of question marks.
//
// FAIL-first: this file ran against unchanged card code and failed on
// ExampleExLlamaV3 being undefined, and every FLAGS/ENGINE assertion failed
// once the fixture existed without the renderer changes.

// TestExLlamaV3TextCard: the normal engine run. Per-device active bytes were
// omitted (dynamic expert placement swaps experts between devices), so no RAM
// or verify line may appear — absent, not "?" — and no llama.cpp flag may be
// taught. The MODEL, ENGINE and FLAGS blocks carry real values, no "?".
func TestExLlamaV3TextCard(t *testing.T) {
	got := Text(ExampleExLlamaV3())
	for _, want := range []string{
		"exllamav3 1.5.0",
		"GLM-5.3-Flash-exl3-4.05",
		"EXL3 4.05 bpw · head 6.0",
		"-gs 44,21 -mcs 185 -mct 32 -mtp",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("text card lacks %q", want)
		}
	}
	if strings.Contains(got, "GB/s from RAM") {
		t.Error("text card prints a RAM line the engine did not report a split for")
	}
	if strings.Contains(got, "% of peak") {
		t.Error("text card prints an \"of peak\" clause with no derivable ceiling")
	}
	// 2026-09-15 (lead review): the combined all-bus figure is refused for an
	// engine placement (bandwidth.Combined) — it is mixed across RAM and VRAM
	// and drafted, the two cases TTP-56 and TTP-67 already refuse. FAIL-first:
	// the round's card printed "Decode 39.6 tok/s aggregate · 19.8 tok/s each
	// · ≈ 139 GB/s".
	if strings.Contains(got, "GB/s") {
		t.Error("text card prints a bandwidth figure for an engine placement with no per-device split")
	}
	// 2026-09-15 (lead review): an engine placement is not a mapping of the
	// model file, so the host residency split is not derivable
	// (tape.Residency). FAIL-first: the round's card printed
	// "Host placed 97.9 GiB (92.1 in RAM / 5.8 on disk)".
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "Host placed") && (strings.Contains(line, "on disk") || strings.Contains(line, "in RAM")) {
			t.Errorf("an engine placement's host line claims a RAM/disk split: %q", strings.TrimSpace(line))
		}
	}
	// 2026-09-15 (lead review): engine.draft names the drafter. FAIL-first: the
	// round's card printed "Draft ? · n_max ? · 75% accepted (300/400)".
	if !strings.Contains(got, "mtp · n_max 1") {
		t.Error("the Draft row does not name the engine's drafter (mtp, n_max 1)")
	}
	for _, llama := range []string{"-fa ", "-ctk ", "-ctv ", "-ub "} {
		if strings.Contains(got, llama) {
			t.Errorf("text card teaches the llama.cpp flag %q an engine run never had", llama)
		}
	}
	for _, block := range []string{"MODEL", "ENGINE", "FLAGS"} {
		for _, line := range strings.Split(got, "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, block) {
				continue
			}
			// The block's own label line and its continuation lines alike:
			// a "?" token anywhere in the block means a figure the engine
			// contract supplies was dropped.
			body := strings.TrimSpace(strings.TrimPrefix(trimmed, block))
			if body == "?" || strings.HasPrefix(body, "? ") || strings.HasSuffix(body, " ?") ||
				strings.Contains(body, " ? ") {
				t.Errorf("%s block prints \"?\" (%q) for a figure the engine reported", block, trimmed)
			}
		}
	}
}

// TestExLlamaV3SplitTextCard: the engine that DID report a consistent
// per-device active split. The RAM line is back — built from the engine's own
// figures — with its "of peak" clause.
func TestExLlamaV3SplitTextCard(t *testing.T) {
	got := Text(ExampleExLlamaV3Split())
	if !strings.Contains(got, "GB/s from RAM") {
		t.Error("text card lacks the RAM line for the engine's consistent split")
	}
	if !strings.Contains(got, "% of peak") {
		t.Error("text card lacks the \"of peak\" clause beside the RAM line")
	}
	if !strings.Contains(got, "-gs 44,21 -mcs 185 -mct 32 -mtp") {
		t.Error("text card lacks the engine argv")
	}
}

// TestLlamaCPPFlagsPredicate: the one exported predicate every flag-printing
// site branches on. A llama server of every known kind keeps its flags; an
// engine does not, because its argv is not llama.cpp's.
func TestLlamaCPPFlagsPredicate(t *testing.T) {
	for _, s := range []*tape.RunSummary{
		Example(),
		ExampleConcurrent(),
		ExampleSpeculative(),
		ExampleRounds(),
	} {
		if !LlamaCPPFlags(s.Server) {
			t.Errorf("LlamaCPPFlags(%s) = false, want true", s.Server.Kind)
		}
	}
	if LlamaCPPFlags(ExampleExLlamaV3().Server) {
		t.Error("LlamaCPPFlags(exllamav3) = true, want false")
	}
	// Unknown kind with no argv at all: still llama-shaped, still "?" rather
	// than an engine's argv.
	if !LlamaCPPFlags(unknownsSummary().Server) {
		t.Error("LlamaCPPFlags(unknown) = false, want true")
	}
	// The rule is about any engine that named itself, not one product
	// (TTP-105, 2026-09-17). FAIL-first: "mistral.rs" answered true on the
	// unedited predicate.
	for k, want := range map[tape.ServerKind]bool{
		tape.ServerLlamaCPP:  true,
		tape.ServerIKLlama:   true,
		tape.ServerUnknown:   true,
		"":                   true,
		tape.ServerExLlamaV3: false,
		"mistral.rs":         false,
	} {
		if got := LlamaCPPFlags(tape.ServerInfo{Kind: k}); got != want {
			t.Errorf("LlamaCPPFlags(%q) = %v, want %v", k, got, want)
		}
	}
}

// TestExplainEnginePlacement: the --explain listing an engine run leads with.
// It names the source (/props, not a GGUF and an argv) and carries the three
// validation verdicts the recorder recorded — the normal absent split, the
// kept one, and the states only the figures' aftermath can show.
func TestExplainEnginePlacement(t *testing.T) {
	if got := ExplainEnginePlacement(Example()); got != "" {
		t.Errorf("ExplainEnginePlacement(llama.cpp run) = %q, want \"\"", got)
	}
	got := ExplainEnginePlacement(ExampleExLlamaV3())
	for _, want := range []string{
		"exllamav3 1.5.0 reported the model, the placement and the flags through /props",
		"no GGUF opened, no argv parsed",
		"split sums    ok",
		"active bytes  absent    no device figure",
		"gpu indices   ok        GPU0, GPU1 among the 2 probed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("explain lacks %q", want)
		}
	}

	split := ExplainEnginePlacement(ExampleExLlamaV3Split())
	if !strings.Contains(split, "active bytes  kept      3 of 3 devices, 7.0 GB/token") {
		t.Errorf("split explain lacks the kept verdict:\n%s", split)
	}

	// The zeroed state: every active figure 0 and the recorder's warning in
	// the tape. Only the warning separates it from an engine that sent none.
	zeroed := ExampleExLlamaV3()
	for i := range zeroed.Placement.Devices {
		zeroed.Placement.Devices[i].ActiveBytesPerToken = 0
	}
	zeroed.Model.ActiveBytesPerToken = 0
	zeroed.Warnings = append(zeroed.Warnings,
		"per-device active bytes (3 of 3 devices, 7.0 GB) disagree with the model's 7.0 GB; all active figures zeroed")
	if g := ExplainEnginePlacement(zeroed); !strings.Contains(g, "active bytes  zeroed") {
		t.Errorf("zeroed explain lacks the zeroed verdict:\n%s", g)
	}

	// The failed validations, derived from the figures alone.
	mismatch := ExampleExLlamaV3()
	mismatch.Placement.Devices[0].Bytes += 1 << 20
	if g := ExplainEnginePlacement(mismatch); !strings.Contains(g, "split sums    MISMATCH") {
		t.Errorf("mismatch explain lacks the MISMATCH verdict:\n%s", g)
	}
	offHost := ExampleExLlamaV3()
	offHost.Placement.Devices = append(offHost.Placement.Devices,
		tape.DevicePlacement{Device: "GPU7", Bytes: 1, Classes: map[tape.TensorClass]int64{tape.ClassOther: 1}})
	if g := ExplainEnginePlacement(offHost); !strings.Contains(g, "gpu indices   unmatched") {
		t.Errorf("off-host explain lacks the unmatched verdict:\n%s", g)
	}
}
