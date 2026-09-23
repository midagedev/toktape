package recorder

import (
	"context"
	"errors"

	"github.com/midagedev/toktape/internal/tape"
)

// An interactive session (`toktape chat`, 2026-09-24).
//
// A benchmark run knows every round before the first request goes out, and
// Record plans, calibrates and fetches the template for all of them at once.
// A chat session does not: each round is one turn a person types after reading
// the last answer. Session is the same recorder cut at that seam — Open does
// everything that happens once per tape, Send sends one turn, Close reduces
// the turns into one tape with Summary.Mode = tape.ModeChat.
//
// A session is one stream. The history is the session's own: Send appends the
// user's text, sends the whole conversation, and appends the answer, so the
// caller never holds a second copy that could disagree with the tape.

// ErrContextFull is returned by Send when the conversation plus the answer
// cap no longer fits the server's context. The session is still open and
// Close still writes the tape; the turn was not sent. The recorder never
// shortens the history to make it fit — a chat trimmed behind the user's back
// is a different conversation from the one they typed.
var ErrContextFull = errors.New("the conversation no longer fits the server's context")

// Session is one open chat session. It is not safe for concurrent use: one
// turn at a time, which is what a chat is.
type Session struct {
	r *run
}

// Open attaches to the server and does everything that happens once per tape.
// It fails exactly where Record would fail before its first request.
func Open(ctx context.Context, opts Options) (*Session, error) {
	return nil, errors.New("recorder: chat session not implemented")
}

// Send sends one turn — text as the user's message after the history so
// far — and returns its record once the answer has finished. Cancelling ctx
// ends this turn only: the partial answer is kept as a cancelled record and
// the session stays open.
func (s *Session) Send(ctx context.Context, text string) (tape.RequestRecord, error) {
	return tape.RequestRecord{}, errors.New("recorder: chat session not implemented")
}

// History is the conversation so far, oldest first, as it will be sent with
// the next turn. The returned slice is a copy.
func (s *Session) History() []tape.Message {
	return nil
}

// Close stops the session's sampling and reduces every turn into one tape.
// A session with no answered turn is ErrAllStreamsFailed, as a run is.
func (s *Session) Close() (*tape.Tape, error) {
	return nil, errors.New("recorder: chat session not implemented")
}
