package publish

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TestUploadRefusesAChatUnlessAsked: Upload is the second gate behind the
// verb's own refusal, so a caller that builds Options by hand cannot send a
// chat's conversation without saying so.
func TestUploadRefusesAChatUnlessAsked(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "abc", "url": "https://tape.example/r/abc"})
	}))
	defer srv.Close()

	tp, err := tape.Read("../card/testdata/chat-e2e.toktape")
	if err != nil {
		t.Fatal(err)
	}
	view := PublicView(tp, WithText)
	c := &Client{BaseURL: srv.URL}
	if _, err := c.Upload(context.Background(), view, IndexOf(view), Options{}); !errors.Is(err, ErrConversation) {
		t.Errorf("Upload(chat) = %v, want ErrConversation", err)
	}
	if calls != 0 {
		t.Fatalf("a refused chat reached the server %d times", calls)
	}
	if _, err := c.Upload(context.Background(), view, IndexOf(view), Options{IncludeConversation: true}); err != nil {
		t.Fatalf("Upload(chat, IncludeConversation): %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}
