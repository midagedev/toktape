package main

import (
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/recorder"
)

// TestLoadingLineBusy: ik_llama.cpp holds /props while another completion
// runs, and the line must say that instead of "loading" (TTP-33, 2026-09-13).
func TestLoadingLineBusy(t *testing.T) {
	if got, want := loadingLine(recorder.ReasonBusy, 3*time.Second), "⠸ server is busy with another request … 3s"; got != want {
		t.Errorf("loadingLine(ReasonBusy) = %q, want %q", got, want)
	}
}
