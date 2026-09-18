package recorder

import "testing"

// TestHFCacheRepo pins what a path is allowed to prove. Repo is read
// downstream as an exact identity, so the interesting half of this table is
// the refusals: everything hfCacheRepo cannot prove must come back "" rather
// than as a plausible-looking id (TTP-119).
func TestHFCacheRepo(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{{
		name: "a real cache entry",
		path: "/home/k/.cache/huggingface/hub/models--unsloth--Qwen3-0.6B-GGUF/snapshots/8f3c1e/Qwen3-0.6B-Q8_0.gguf",
		want: "unsloth/Qwen3-0.6B-GGUF",
	}, {
		name: "an exl3 directory, which is a model and not a file",
		path: "/models/hub/models--turboderp--Llama-3.1-8B-exl3/snapshots/a1b2c3",
		want: "turboderp/Llama-3.1-8B-exl3",
	}, {
		// Windows is not the primary target but a --url laptop on it reports
		// the server's path, and the server's path is POSIX. This case is
		// about a local file, and separators must not decide the answer.
		name: "backslashes",
		path: `C:\Users\k\.cache\huggingface\hub\models--bartowski--Qwen_Qwen3-0.6B-GGUF\snapshots\d4\Qwen_Qwen3-0.6B-Q4_K_M.gguf`,
		want: "bartowski/Qwen_Qwen3-0.6B-GGUF",
	}, {
		name: "no snapshots component: a directory that only looks like a cache entry",
		path: "/models/models--unsloth--Qwen3-0.6B-GGUF/Qwen3-0.6B-Q8_0.gguf",
		want: "",
	}, {
		name: "snapshots is not the next component",
		path: "/hub/models--unsloth--Qwen3-0.6B-GGUF/blobs/snapshots/x.gguf",
		want: "",
	}, {
		name: "three halves: nothing here says where the org ends",
		path: "/hub/models--a--b--c/snapshots/d/x.gguf",
		want: "",
	}, {
		name: "one half: no org at all",
		path: "/hub/models--Qwen3/snapshots/d/x.gguf",
		want: "",
	}, {
		name: "an empty half",
		path: "/hub/models--unsloth--/snapshots/d/x.gguf",
		want: "",
	}, {
		name: "an ordinary model directory",
		path: "/models/Qwen3-0.6B-GGUF/Qwen3-0.6B-Q8_0.gguf",
		want: "",
	}, {
		name: "no path at all",
		path: "",
		want: "",
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hfCacheRepo(c.path); got != c.want {
				t.Errorf("hfCacheRepo(%q) = %q, want %q", c.path, got, c.want)
			}
		})
	}
}
