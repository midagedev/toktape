package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/ledger"
	"github.com/midagedev/toktape/internal/tape"
	"github.com/midagedev/toktape/internal/tui"
)

// chatFixturePath is a real `toktape chat` tape (internal/card/testdata): two
// turns of one stream, the second carrying the first's history.
const chatFixturePath = "../../internal/card/testdata/chat-e2e.toktape"

// copyChatTape writes the fixture into dir, with mutate applied, and returns
// its path.
func copyChatTape(t *testing.T, dir, name string, mutate func(*tape.Tape)) string {
	t.Helper()
	tp, err := tape.Read(chatFixturePath)
	if err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(tp)
	}
	path := filepath.Join(dir, name+tape.Ext)
	if err := tape.Write(path, tp); err != nil {
		t.Fatal(err)
	}
	return path
}

// C3: a saved chat stays out of the experiment log, and saving it warns of
// nothing.
func TestSaveRunKeepsChatOutOfTheLedger(t *testing.T) {
	dir := t.TempDir()
	tp, err := tape.Read(chatFixturePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := saveRun(dir, tp, false); err != nil {
		t.Fatalf("saveRun: %v", err)
	}
	if rows, _ := ledger.Read(dir); len(rows) != 0 {
		t.Errorf("the ledger holds %d rows after a chat, want 0", len(rows))
	}
	code, out, _ := exec(t, "log", "--out", dir)
	if code != exitOK {
		t.Fatalf("log: exit %d", code)
	}
	if strings.Contains(out, tp.Summary.ID) {
		t.Errorf("toktape log lists the chat:\n%s", out)
	}
}

// C3: ls lists every tape on disk, a chat among them, and says which it is.
func TestLsLabelsAChat(t *testing.T) {
	dir := t.TempDir()
	copyChatTape(t, dir, "20260924-085340-qwen3-1-7b-q4-k-m", nil)
	writeTape(t, dir, card.Example())
	code, out, errOut := exec(t, "ls", "--out", dir)
	if code != exitOK {
		t.Fatalf("ls: exit %d: %s", code, errOut)
	}
	var chatRow, benchRow string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "20260924-085340"):
			chatRow = line
		case strings.HasPrefix(line, card.Example().ID):
			benchRow = line
		}
	}
	if !strings.Contains(chatRow, "chat") {
		t.Errorf("the chat's row does not say chat: %q", chatRow)
	}
	if strings.Contains(benchRow, "chat") {
		t.Errorf("a benchmark row says chat: %q", benchRow)
	}
}

// C4: a chat tape holds the person's own words, so publish refuses it — dry
// run included — unless --include-conversation says the words may go.
func TestPublishRefusesAChat(t *testing.T) {
	publishHome(t, "first_publish_warning_seen = true\n")
	path := copyChatTape(t, t.TempDir(), "chat", nil)
	srv, calls := acceptingServer(t, "")

	for _, extra := range [][]string{nil, {"--dry-run"}, {"--yes"}, {"--with-text"}} {
		args := append([]string{"publish", path, "--url", srv.URL}, extra...)
		code, stdout, stderr := exec(t, args...)
		if code != exitUsage {
			t.Errorf("%v: exit %d, want %d", extra, code, exitUsage)
		}
		if stdout != "" {
			t.Errorf("%v: stdout = %q, want nothing", extra, stdout)
		}
		if !strings.Contains(stderr, "conversation") || !strings.Contains(stderr, "--include-conversation") {
			t.Errorf("%v: the refusal does not say why and what allows it:\n%s", extra, stderr)
		}
	}
	if *calls != 0 {
		t.Fatalf("a refused chat reached the server %d times", *calls)
	}

	if code, _, stderr := exec(t, "publish", path, "--url", srv.URL, "--include-conversation", "--no-text"); code != exitUsage || !strings.Contains(stderr, "opposite") {
		t.Errorf("--include-conversation --no-text: exit %d, stderr %q", code, stderr)
	}
	if *calls != 0 {
		t.Fatalf("a contradictory publish reached the server %d times", *calls)
	}

	code, stdout, stderr := exec(t, "publish", path, "--url", srv.URL, "--include-conversation")
	if code != exitOK {
		t.Fatalf("--include-conversation: exit %d: %s", code, stderr)
	}
	if *calls != 1 || !strings.Contains(stdout, "/r/abc123") {
		t.Errorf("--include-conversation did not publish: calls %d, stdout %q", *calls, stdout)
	}
}

// C4: the flag names what it shares, so it is refused on a tape that holds no
// conversation rather than silently ignored.
func TestPublishIncludeConversationOnABenchmark(t *testing.T) {
	publishHome(t, "")
	code, _, stderr := exec(t, "publish", publishTape(t), "--include-conversation", "--dry-run")
	if code != exitUsage || !strings.Contains(stderr, "not a chat") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

// C5: a chat and a benchmark run are not the same measurement, in either
// order.
func TestCompareRefusesChatAgainstBenchmark(t *testing.T) {
	dir := t.TempDir()
	chat := copyChatTape(t, dir, "chat", nil)
	bench := writeTape(t, dir, card.Example())
	for _, pair := range [][2]string{{chat, bench}, {bench, chat}} {
		code, out, stderr := exec(t, "compare", pair[0], pair[1])
		if code != exitUsage {
			t.Errorf("compare %s %s: exit %d, want %d", filepath.Base(pair[0]), filepath.Base(pair[1]), code, exitUsage)
		}
		if out != "" {
			t.Errorf("a refused compare printed a table:\n%s", out)
		}
		if !strings.Contains(stderr, "not comparable") {
			t.Errorf("stderr = %q", stderr)
		}
	}
}

// C5: two chats compare, and the report says when they were different
// conversations.
func TestCompareTwoChats(t *testing.T) {
	dir := t.TempDir()
	a := copyChatTape(t, dir, "a", nil)
	same := copyChatTape(t, dir, "same", nil)
	other := copyChatTape(t, dir, "other", func(tp *tape.Tape) {
		for i := range tp.Requests {
			tp.Requests[i].Prompt.Messages[0].Content = "Tell me a long story."
		}
	})

	code, out, stderr := exec(t, "compare", a, other)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(out, "different conversations") {
		t.Errorf("two different chats compare without saying so:\n%s", out)
	}
	code, out, _ = exec(t, "compare", a, same)
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(out, "different conversations") {
		t.Errorf("two chats that opened alike are called different:\n%s", out)
	}
}

// C6: play steps through a chat — two turns of one stream, the second
// carrying the first's history and starting after the person typed — and
// every frame renders.
func TestPlayAChat(t *testing.T) {
	tp, err := tape.Read(chatFixturePath)
	if err != nil {
		t.Fatal(err)
	}
	end := tapeDuration(tp)
	if end < 2*time.Second {
		t.Fatalf("tapeDuration = %v, want past the second turn's start", end)
	}
	for _, wall := range []time.Duration{0, end / 4, end / 2, end, end + time.Second} {
		m, clip := playModel(tp, wall, 1)
		if v := tui.View(m, clip, tui.MinWidth, tui.MinHeight); strings.TrimSpace(v) == "" {
			t.Errorf("an empty frame at %v", wall)
		}
	}
	m, clip := playModel(tp, end, 1)
	if v := tui.View(m, clip, tui.MinWidth, tui.MinHeight); !strings.Contains(v, "Korean") && !strings.Contains(v, "안녕") {
		t.Errorf("the last frame shows neither the second turn's prompt nor its answer:\n%s", v)
	}
}

// C6: render writes a clip of a chat.
func TestRenderAChat(t *testing.T) {
	dir := t.TempDir()
	path := copyChatTape(t, dir, "chat", nil)
	cast := filepath.Join(dir, "chat.cast")
	args := append([]string{"render", path, "--cast", cast}, shortClip...)
	code, out, errOut := exec(t, args...)
	if code != exitOK {
		t.Fatalf("render: exit %d, stderr %q", code, errOut)
	}
	fi, err := os.Stat(cast)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("no cast written (%v): %s", err, out)
	}
}
