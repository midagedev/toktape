// Command toktape is the black-box tape for local LLM serving.
//
// Run with no arguments it discovers a llama-server, records one run into a
// .tape file and prints the shareable card. Every other verb is a thin
// operation over a tape file that already exists.
//
// The command tree is built around Run, which takes its streams and its
// arguments and returns the process exit code, so every verb is testable
// without spawning a process. Dependencies are deliberately stdlib-only: the
// flag package is enough for five verbs and it keeps the release a single
// static binary with nothing to resolve.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(Run(ctx, os.Stdout, os.Stderr, os.Args[1:]))
}
