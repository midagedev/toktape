package server

import (
	"fmt"

	"github.com/midagedev/toktape/internal/tape"
)

// defaultPrompts is the zero-config prompt set. The north star asks for a
// first run with no flag to learn, so a user who types nothing still gets a
// real measurement, and a concurrent run needs one distinct prompt per stream.
//
// Each one is about forty tokens of plain English and asks for an explanation,
// a short list or a small function, which is the shape that answers in roughly
// 150 to 300 tokens: long enough to clear tape.MinDecodeTokens by a wide
// margin, short enough that a slow rig still finishes a run quickly. They are
// deliberately unlike each other from the first word, so concurrent streams do
// not share a prefix and cannot hit each other's prompt cache, which would
// make the measured prefill meaningless.
var defaultPrompts = []string{
	"Explain what a memory-mapped file is and why a process that mmaps a very large file can show a huge virtual size while its resident set stays small. Keep it to a short paragraph.",
	"Write a small Go function that takes a slice of integers and returns the median, handling both the even and the odd length case. Include a one-line comment on each branch.",
	"List the main differences between a solid state drive and a hard disk drive for random reads, and say which one matters more for a database workload and why.",
	"Describe in a few sentences how a CPU cache line works, and explain why iterating a two-dimensional array row by row is usually faster than column by column.",
	"Summarise what happens, step by step, when a user types a domain name into a browser and presses enter, from name resolution to the first byte of the response.",
	"Write a short Python function that reads a text file line by line and returns a dictionary counting how often each word appears, lowercasing the words first.",
	"Compare optimistic and pessimistic locking in a relational database. Give one situation where each is clearly the better choice and explain the reasoning briefly.",
	"Explain what a page fault is in an operating system, the difference between a minor and a major one, and what a program that takes many major faults is actually doing.",
	"Outline the steps to take when a web service that normally responds in fifty milliseconds starts responding in three seconds, in the order you would actually try them.",
	"Write a small shell pipeline that finds the ten largest files under a directory tree and prints their sizes in a human readable form. Explain each stage in one line.",
	"Describe how a bloom filter works, what kind of error it can make and what kind it cannot, and name one real system that uses one and what it uses it for.",
	"Explain the difference between concurrency and parallelism using a concrete example, and say why a single-core machine can be concurrent but never parallel.",
	"Give a short explanation of how public key cryptography lets two strangers agree on a shared secret over a channel that an eavesdropper is fully able to read.",
	"Write a small JavaScript function that debounces another function by a given number of milliseconds, and explain in two sentences when debouncing is the wrong tool.",
	"Describe what a database index costs on writes, what it saves on reads, and how you would decide whether a particular index on a busy table is worth keeping.",
	"Explain what happens to a TCP connection during a packet loss event, how the sender detects it, and why the transfer rate drops before it slowly climbs back.",
}

// DefaultPrompts returns n distinct chat requests in a deterministic order, so
// two runs on the same rig send the same work. n <= 0 yields nil.
//
// Beyond the built-in set the prompts repeat with a numbered lead sentence,
// which keeps them distinct from their first token and so keeps every stream's
// prefix its own.
func DefaultPrompts(n int) []StreamRequest {
	if n <= 0 {
		return nil
	}
	out := make([]StreamRequest, 0, n)
	for i := 0; i < n; i++ {
		text := defaultPrompts[i%len(defaultPrompts)]
		if i >= len(defaultPrompts) {
			text = fmt.Sprintf("Request %d. %s", i+1, text)
		}
		out = append(out, StreamRequest{
			Messages:  []tape.Message{{Role: "user", Content: text}},
			MaxTokens: 320,
		})
	}
	return out
}
