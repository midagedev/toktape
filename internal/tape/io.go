package tape

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Ext is the run file extension. Files are gzip'd JSON.
const Ext = ".tape"

// Write serialises t to path atomically (temp file + rename).
func Write(path string, t *Tape) error {
	if t.Schema == 0 {
		t.Schema = SchemaVersion
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tape-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	gz := gzip.NewWriter(tmp)
	enc := json.NewEncoder(gz)
	if err := enc.Encode(t); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// Read loads a tape. Plain (non-gzip) JSON is accepted too, so a tape
// someone hand-edited still loads.
func Read(path string) (*Tape, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Decode(f)
}

// Decode reads a tape from r (gzip or plain JSON).
func Decode(r io.Reader) (*Tape, error) {
	br := &peekReader{r: r}
	head, err := br.peek(2)
	if err != nil {
		return nil, err
	}
	var src io.Reader = br
	if len(head) == 2 && head[0] == 0x1f && head[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		src = gz
	}
	var t Tape
	if err := json.NewDecoder(src).Decode(&t); err != nil {
		return nil, fmt.Errorf("tape: decode: %w", err)
	}
	if t.Schema > SchemaVersion {
		return nil, fmt.Errorf("tape: schema %d is newer than this build supports (%d)", t.Schema, SchemaVersion)
	}
	if t.Schema == 0 {
		return nil, errors.New("tape: missing schema field")
	}
	return &t, nil
}

// SlugFromModel makes the model part of a run ID: lower-case, [a-z0-9-],
// at most 24 runes, without the .gguf suffix.
func SlugFromModel(fileName string) string {
	s := strings.TrimSuffix(strings.ToLower(filepath.Base(fileName)), ".gguf")
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if len(out) > 24 {
		out = strings.TrimRight(out[:24], "-")
	}
	if out == "" {
		out = "model"
	}
	return out
}

// peekReader lets Decode sniff the gzip magic without losing bytes.
type peekReader struct {
	r   io.Reader
	buf []byte
}

func (p *peekReader) peek(n int) ([]byte, error) {
	b := make([]byte, n)
	got, err := io.ReadFull(p.r, b)
	p.buf = b[:got]
	if err == io.ErrUnexpectedEOF || err == io.EOF {
		return p.buf, nil
	}
	return p.buf, err
}

func (p *peekReader) Read(b []byte) (int, error) {
	if len(p.buf) > 0 {
		n := copy(b, p.buf)
		p.buf = p.buf[n:]
		return n, nil
	}
	return p.r.Read(b)
}
