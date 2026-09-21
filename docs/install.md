# Install and platforms

[← README](../README.md)

## Install

**Homebrew** (macOS and Linux):

```sh
brew install midagedev/tap/toktape
```

**Shell script** — downloads the release for your OS and CPU, verifies it
against `checksums.txt`, installs one binary to `~/.local/bin`
(`TOKTAPE_INSTALL`, `TOKTAPE_VERSION`, `TOKTAPE_BASE_URL` to aim it; `--dry-run` to watch):

```sh
curl -fsSL https://raw.githubusercontent.com/midagedev/toktape/main/scripts/install.sh | sh
```

**Windows:** the release page carries zips; unpack `toktape.exe` anywhere on
`PATH`. **Go:** `go install github.com/midagedev/toktape/cmd/toktape@latest`.
**From source** (Go 1.26, no cgo):

```sh
git clone https://github.com/midagedev/toktape.git
cd toktape
go build -o toktape ./cmd/toktape
```

Check with `toktape version`; the binary resolves nothing at runtime, so
`scp` to the server's host is also an install. Linux is the primary target —
the memory, fault and flag rows are read from `/proc`. macOS and Windows
build and attach to a remote server with `--url`; those rows print `?` there,
and WSL2 runs the Linux binary for the full view. GPU rows come from
`nvidia-smi`.

## Supported

| | |
| --- | --- |
| Servers | llama-server (upstream llama.cpp), ik_llama.cpp, any server that answers `/props` with an `engine` object |
| Linux | primary target, x86_64 and arm64, full `/proc` view |
| macOS | builds and runs; attach with `--url`; no `/proc` view, so memory and fault rows are `?` |
| Windows | own binary; discovery and the `nvidia-smi` GPU view work as they do elsewhere, so `--url` is only for a server discovery does not probe; no `/proc` view, so memory and fault rows are `?`; WSL2 runs the Linux binary for the full view |
| GPU | NVIDIA through `nvidia-smi` |

Any other OpenAI-compatible server — vLLM, SGLang, TabbyAPI, LM Studio —
records in a generic mode (`--engine-kind openai`, or auto-detected): no
timings come back, so the recorder's own clock is the record, the card says
`client-timed` beside the rate, and you compare it only with other
client-timed runs.

Ollama (port 11434) and LM Studio (1234) are found without `--url`, and
`--model <id>` picks one when the server lists several. These servers count
tokens only in a closing `usage` message, which a stream cut by the clock
never sends — so toktape first sends two short requests to measure decode
and prefill, then sizes the prompts and the answer cap so every stream ends
on its own inside the clock. vLLM's `max_model_len` is read as the context,
and its token count is requested on every chunk. Measured on Ollama 0.34 and
vLLM 0.29: the default command finishes in about twenty seconds with a rate
on the card.
