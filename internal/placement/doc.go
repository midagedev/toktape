// Package placement answers "which bytes of this model sit on which device".
//
// It reads a GGUF header (never the tensor data), buckets every tensor into a
// tape.TensorClass, and replays llama.cpp's own placement rules over the
// server's flags to produce a tape.PlacementSummary.
//
// Three rules shape the whole package:
//
//  1. "Never loaded" comes from the tensor headers, never from
//     total-minus-RSS (docs/research/00-handover-brief.md lesson 3: that
//     subtraction overstated it by ~50 GB on the demo machine).
//  2. Unknown is "" / 0. When a flag was not observed the placement is not
//     guessed; Source becomes "unknown" and the card prints "?".
//  3. Every rule that decides a byte's device is transcribed from llama.cpp
//     master, with the file and symbol cited at the rule. Where the toktape
//     design note and upstream disagree, upstream wins and the deviation is
//     named in a comment.
//
// Everything here is a pure function, so the placement logic is testable
// without a multi-gigabyte model on disk. The one file that reads a model
// from disk lives in internal/placement/gguf, imported only by the recorder —
// that direction keeps the GGUF parser (and its HTTP client) out of every
// render path, because a renderer reads a tape and nothing else.
package placement
