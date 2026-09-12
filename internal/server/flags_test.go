package server

import (
	"reflect"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

func TestParseFlags(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		want tape.ServerFlags
	}{
		{
			name: "nothing passed leaves everything unknown",
			argv: []string{"/usr/local/bin/llama-server"},
			want: tape.ServerFlags{},
		},
		{
			name: "a typical offload command line",
			argv: []string{
				"llama-server", "-m", "/models/gpt-oss-20b.gguf",
				"-ngl", "99", "-fa", "on", "-b", "4096", "-ub", "1024",
				"-ctk", "q8_0", "-ctv", "q8_0", "-t", "16", "-c", "32768",
				"--no-mmap", "--port", "8080",
			},
			want: tape.ServerFlags{
				NGL: "99", FlashAttn: "on", Batch: "4096", UBatch: "1024",
				CacheTypeK: "q8_0", CacheTypeV: "q8_0", Threads: "16",
				LoadMode: "direct",
				Other:    []string{"-m /models/gpt-oss-20b.gguf", "-c 32768", "--port 8080"},
			},
		},
		{
			name: "long names and inline values",
			argv: []string{
				"llama-server", "--n-gpu-layers=48", "--flash-attn=auto",
				"--batch-size=2048", "--ubatch-size=512",
				"--cache-type-k=f16", "--cache-type-v=f16",
				"--threads=8", "--load-mode=direct",
			},
			want: tape.ServerFlags{
				NGL: "48", FlashAttn: "auto", Batch: "2048", UBatch: "512",
				CacheTypeK: "f16", CacheTypeV: "f16", Threads: "8", LoadMode: "direct",
			},
		},
		{
			name: "override-tensor: repeatable, and a comma list of pattern=device",
			argv: []string{
				"llama-server",
				"-ot", `blk\.(1[0-9])\.ffn_.*_exps=CPU,blk\.2.*=CUDA1`,
				"--override-tensor", `blk\.3[0-9]\.ffn_.*=CUDA0`,
			},
			want: tape.ServerFlags{
				OverrideTens: []string{
					`blk\.(1[0-9])\.ffn_.*_exps=CPU`,
					`blk\.2.*=CUDA1`,
					`blk\.3[0-9]\.ffn_.*=CUDA0`,
				},
			},
		},
		{
			name: "a comma inside a regex repetition is not a list separator",
			argv: []string{"llama-server", "-ot", `blk\.(1|2){1,2}\.ffn_.*=CPU,blk\.9.*=CUDA0`},
			want: tape.ServerFlags{
				OverrideTens: []string{
					`blk\.(1|2){1,2}\.ffn_.*=CPU`,
					`blk\.9.*=CUDA0`,
				},
			},
		},
		{
			name: "ik_llama cpu-moe flags",
			argv: []string{"llama-server", "--n-cpu-moe", "12"},
			want: tape.ServerFlags{CPUMoE: "12"},
		},
		{
			name: "short cpu-moe flags",
			argv: []string{"llama-server", "-ncmoe", "8", "-ngl", "99"},
			want: tape.ServerFlags{CPUMoE: "8", NGL: "99"},
		},
		{
			name: "the valueless cpu-moe flag means every expert layer",
			argv: []string{"llama-server", "-cmoe"},
			want: tape.ServerFlags{CPUMoE: "all"},
		},
		{
			name: "flash-attn as a bare boolean on an older build",
			argv: []string{"llama-server", "-fa", "-ngl", "99"},
			want: tape.ServerFlags{FlashAttn: "on", NGL: "99"},
		},
		{
			name: "flash-attn written as 0/1",
			argv: []string{"llama-server", "--flash-attn", "0"},
			want: tape.ServerFlags{FlashAttn: "off"},
		},
		{
			name: "explicit no-flash-attn",
			argv: []string{"llama-server", "--no-flash-attn"},
			want: tape.ServerFlags{FlashAttn: "off"},
		},
		{
			name: "mmap on",
			argv: []string{"llama-server", "--mmap"},
			want: tape.ServerFlags{LoadMode: "mmap"},
		},
		{
			name: "a negative value is a value, not the next flag",
			argv: []string{"llama-server", "-ngl", "-1"},
			want: tape.ServerFlags{NGL: "-1"},
		},
		{
			name: "unknown boolean flags are kept verbatim",
			argv: []string{"llama-server", "--verbose", "--metrics", "--jinja"},
			want: tape.ServerFlags{Other: []string{"--verbose", "--metrics", "--jinja"}},
		},
		{
			name: "no argv at all",
			argv: nil,
			want: tape.ServerFlags{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseFlags(tc.argv)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseFlags(%q)\n got %+v\nwant %+v", tc.argv, got, tc.want)
			}
		})
	}
}

func TestSplitOverrideTensor(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`a=CPU`, []string{`a=CPU`}},
		{`a=CPU,b=CUDA0`, []string{`a=CPU`, `b=CUDA0`}},
		{`blk\.(1|2){1,2}=CPU`, []string{`blk\.(1|2){1,2}=CPU`}},
		{`blk\.{2,3}x=CPU,y=CUDA1`, []string{`blk\.{2,3}x=CPU`, `y=CUDA1`}},
		{`no-equals-sign`, []string{`no-equals-sign`}},
	}
	for _, tc := range cases {
		if got := SplitOverrideTensor(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("SplitOverrideTensor(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestParseFlagsNeverInventsADefault: the card prints "?" for a flag that was
// not on the command line, so the parser must not fill one in.
func TestParseFlagsNeverInventsADefault(t *testing.T) {
	got := ParseFlags([]string{"llama-server", "-m", "/models/x.gguf"})
	if got.NGL != "" || got.FlashAttn != "" || got.Batch != "" || got.UBatch != "" ||
		got.CacheTypeK != "" || got.CacheTypeV != "" || got.LoadMode != "" ||
		got.CPUMoE != "" || got.Threads != "" || got.OverrideTens != nil {
		t.Errorf("ParseFlags invented a value: %+v", got)
	}
}
