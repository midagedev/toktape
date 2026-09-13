package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// The prompts file of `record --prompts FILE` (TTP-31, 2026-09-13).
//
// One card from one prompt is misleading when the same draft model is accepted
// 13 % of the time on prose and 87 % on SQL, so a run can send several prompts,
// one round after another, into one tape. The file is JSON Lines because that
// is what every eval set already ships as, and one line is one round:
//
//	{"name":"sql-1","prompt":"Write a query that ..."}
//	{"name":"chat","messages":[{"role":"user","content":"..."}],"max_tokens":512}
//
// "name" is optional and labels the round on the card; a line without one is
// labelled by its number. Exactly one of "prompt" and "messages" is required.
// "max_tokens", when present, caps that round and wins over --n-predict.

// promptLine is one line of the file as written.
type promptLine struct {
	Name      string         `json:"name"`
	Prompt    *string        `json:"prompt"`
	Messages  []tape.Message `json:"messages"`
	MaxTokens *int           `json:"max_tokens"`
}

// readPromptsFile opens path and parses it with parsePromptsJSONL.
func readPromptsFile(path string) ([]recorder.Round, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening the prompts file: %w", err)
	}
	defer f.Close()
	return parsePromptsJSONL(f)
}

// parsePromptsJSONL reads a JSON Lines prompts file into recorder rounds, one
// round per non-blank line, in file order. Every error names the 1-based line
// it is about, because the file is typically hundreds of lines long and a
// message without a line number sends the user bisecting it.
//
// Unknown keys are an error rather than ignored: "promt" silently dropped would
// send a round with no prompt at all, and the user would be measuring the
// default prompt without knowing it.
func parsePromptsJSONL(r io.Reader) ([]recorder.Round, error) {
	br := bufio.NewReader(r)
	var rounds []recorder.Round
	line := 0
	for {
		raw, readErr := br.ReadBytes('\n')
		if len(raw) > 0 {
			line++
			if line == 1 {
				raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf")) // a UTF-8 BOM from a Windows editor
			}
			if body := bytes.TrimSpace(raw); len(body) > 0 {
				rd, err := parsePromptLine(body)
				if err != nil {
					return nil, fmt.Errorf("line %d: %w", line, err)
				}
				rounds = append(rounds, rd)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("line %d: reading: %w", line+1, readErr)
		}
	}
	if len(rounds) == 0 {
		return nil, fmt.Errorf("line %d: end of file with no prompt line in it", line)
	}
	return rounds, nil
}

// parsePromptLine turns one non-blank line into a round.
func parsePromptLine(body []byte) (recorder.Round, error) {
	var pl promptLine
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&pl); err != nil {
		return recorder.Round{}, fmt.Errorf("not a prompt object: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return recorder.Round{}, errors.New("more than one JSON value on the line")
	}

	var msgs []tape.Message
	switch {
	case pl.Prompt != nil && pl.Messages != nil:
		return recorder.Round{}, errors.New(`has both "prompt" and "messages"; give one`)
	case pl.Prompt != nil:
		if strings.TrimSpace(*pl.Prompt) == "" {
			return recorder.Round{}, errors.New(`"prompt" is empty`)
		}
		msgs = []tape.Message{{Role: "user", Content: *pl.Prompt}}
	case pl.Messages != nil:
		if len(pl.Messages) == 0 {
			return recorder.Round{}, errors.New(`"messages" is empty`)
		}
		for i, m := range pl.Messages {
			if strings.TrimSpace(m.Role) == "" {
				return recorder.Round{}, fmt.Errorf(`message %d has no "role"`, i+1)
			}
		}
		msgs = append([]tape.Message(nil), pl.Messages...)
	default:
		return recorder.Round{}, errors.New(`needs "prompt" or "messages"`)
	}

	req := server.StreamRequest{Messages: msgs}
	if pl.MaxTokens != nil {
		if *pl.MaxTokens <= 0 {
			return recorder.Round{}, fmt.Errorf(`"max_tokens" is %d; it must be a positive count`, *pl.MaxTokens)
		}
		req.MaxTokens = *pl.MaxTokens
	}
	return recorder.Round{Name: pl.Name, Prompts: []server.StreamRequest{req}}, nil
}
