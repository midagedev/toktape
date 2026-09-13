package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/midagedev/toktape/internal/tape"
)

// Control sequences the clip needs. A player replays bytes into a terminal, so
// the frames have to say where they start and the recording has to leave the
// cursor the way it found it.
const (
	// home moves to row 1 column 1 without clearing, which is how every frame
	// after the first repaints: the frame is a full redraw of a fixed-size
	// screen, so there is nothing left over to erase.
	home = "\x1b[H"
	// clear wipes the screen and the scrollback once, at the start.
	clear = "\x1b[2J\x1b[3J"
	// hideCursor / showCursor keep the terminal's own cursor out of the
	// picture; the breathing block in the left pane is the cursor here.
	hideCursor = "\x1b[?25l"
	showCursor = "\x1b[?25h"
)

// castHeader is the first line of an asciicast v2 file.
type castHeader struct {
	Version   int               `json:"version"`
	Width     int               `json:"width"`
	Height    int               `json:"height"`
	Timestamp int64             `json:"timestamp,omitempty"`
	Env       map[string]string `json:"env"`
}

// Asciicast renders tp as an asciicast v2 recording: a JSON header line
// followed by one ["<time>", "o", "<frame>"] event per frame.
//
// Every event carries a whole screen rather than the difference from the last
// one. A diff would be a third of the bytes, but it would also make the file
// unseekable — a viewer who drags the scrubber into the middle of a diffed
// recording sees whatever the terminal happened to hold. The frames are
// identical text with identical escape sequences, so gzip gets those bytes
// back anyway.
//
// Lines are joined with CRLF because a player feeds these bytes to a terminal,
// where a bare LF moves down a row and leaves the column where it was.
func Asciicast(tp *tape.Tape, opts Options) ([]byte, error) {
	o, sched, err := prepare(tp, opts)
	if err != nil {
		return nil, err
	}

	ts := o.Timestamp
	if ts.IsZero() && tp != nil {
		ts = tp.Summary.StartedAt
	}
	header := castHeader{
		Version: 2,
		Width:   o.Width,
		Height:  o.Height,
		Env:     map[string]string{"TERM": "xterm-256color"},
	}
	if !ts.IsZero() {
		header.Timestamp = ts.Unix()
	}
	head, err := json.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("render: asciicast header: %w", err)
	}

	var buf bytes.Buffer
	buf.Write(head)
	buf.WriteByte('\n')

	for i := 0; i < sched.Count; i++ {
		f := sched.Frame(i)
		var data strings.Builder
		if i == 0 {
			data.WriteString(hideCursor)
			data.WriteString(clear)
		}
		data.WriteString(home)
		data.WriteString(strings.ReplaceAll(FrameText(tp, o, f), "\n", "\r\n"))
		if i == sched.Count-1 {
			data.WriteString(showCursor)
		}
		line, err := castEvent(f.Clip.Seconds(), data.String())
		if err != nil {
			return nil, err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// castEvent formats one output event. The time is written with six decimals
// rather than through json.Marshal, which would print 11 for eleven seconds
// and 0.03333333333333333 for a 30 fps step.
func castEvent(at float64, data string) ([]byte, error) {
	payload, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("render: asciicast event at %.3fs: %w", at, err)
	}
	out := make([]byte, 0, len(payload)+24)
	out = append(out, fmt.Sprintf("[%.6f, \"o\", ", at)...)
	out = append(out, payload...)
	out = append(out, ']')
	return out, nil
}
