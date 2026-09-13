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
		// Speculative decoding (TTP-30). The four named draft flags leave
		// Other; the draft's own placement flags stay in it verbatim.
		{
			name: "a speculative decoding command line, short spellings",
			argv: []string{
				"llama-server", "-m", "/models/target.gguf",
				"-md", "/models/drafts/DSpark-0.6B-Q8_0.gguf",
				"--draft-max", "3", "--draft-min", "1", "--draft-p-min", "0.75",
			},
			want: tape.ServerFlags{
				DraftModel: "DSpark-0.6B-Q8_0.gguf",
				DraftMax:   "3",
				DraftMin:   "1",
				DraftPMin:  "0.75",
				Other:      []string{"-m /models/target.gguf"},
			},
		},
		{
			name: "the long and alternative spellings parse to the same fields",
			argv: []string{
				"llama-server",
				"--model-draft", "/models/drafts/DSpark-0.6B-Q8_0.gguf",
				"--draft-n", "5", "--draft-n-min", "2",
			},
			want: tape.ServerFlags{
				DraftModel: "DSpark-0.6B-Q8_0.gguf",
				DraftMax:   "5",
				DraftMin:   "2",
			},
		},
		{
			name: "--draft is the block size, the same field as --draft-max",
			argv: []string{"llama-server", "--draft", "8"},
			want: tape.ServerFlags{DraftMax: "8"},
		},
		// TTP-58, 2026-09-14: the argv of the ws DSpark run, verbatim from
		// scratch/wsreal/20260913-205310-deepseek-v4-1-flash-q3-k.tape. Its
		// card printed "n_max ?" beside "44% accepted" because the fork
		// spells the block size --spec-draft-n-max. --spec-type names the
		// drafting scheme and has no field, so it stays in Other, where the
		// FLAGS line already prints it.
		{
			name: "the DSpark fork's spelling is the same block size",
			argv: []string{
				"/home/user/llama.cpp-v41-merged/build/bin/llama-server",
				"-m", "/models/DeepSeek-V4.1-Flash-Q3_K_M-engramQ8-tokembdBF16/DeepSeek-V4.1-Flash-Q3_K_M-00001-of-00009.gguf",
				"--alias", "DeepSeek-V4.1-Flash", "-c", "16384",
				"-ngl", "99", "-t", "32", "-b", "2048", "-ub", "512",
				"--lazy-mode", "auto",
				"-md", "/models/DeepSeek-V4.1-Flash-DSpark/DeepSeek-V4.1-Flash-Fp8-128x742M-MXFP4_MOE.tl37.gguf",
				"--spec-type", "draft-dspark", "--spec-draft-n-max", "3",
				"-otd", "output_norm=CUDA0", "--jinja",
				"--reasoning-budget", "0", "--host", "127.0.0.1", "--port", "8001",
			},
			want: tape.ServerFlags{
				NGL:        "99",
				Batch:      "2048",
				UBatch:     "512",
				Threads:    "32",
				DraftModel: "DeepSeek-V4.1-Flash-Fp8-128x742M-MXFP4_MOE.tl37.gguf",
				DraftMax:   "3",
				Other: []string{
					"-m /models/DeepSeek-V4.1-Flash-Q3_K_M-engramQ8-tokembdBF16/DeepSeek-V4.1-Flash-Q3_K_M-00001-of-00009.gguf",
					"--alias DeepSeek-V4.1-Flash",
					"-c 16384",
					"--lazy-mode auto",
					"--spec-type draft-dspark",
					"-otd output_norm=CUDA0",
					"--jinja",
					"--reasoning-budget 0",
					"--host 127.0.0.1",
					"--port 8001",
				},
			},
		},
		{
			name: "the inline = form carries the same values",
			argv: []string{
				"llama-server",
				"-md=/models/drafts/DSpark-0.6B-Q8_0.gguf",
				"--draft-max=3", "--draft-min=1", "--draft-p-min=0.75",
			},
			want: tape.ServerFlags{
				DraftModel: "DSpark-0.6B-Q8_0.gguf",
				DraftMax:   "3",
				DraftMin:   "1",
				DraftPMin:  "0.75",
			},
		},
		{
			name: "the draft's own placement flags stay verbatim in Other",
			argv: []string{
				"llama-server", "-md", "/models/drafts/d.gguf",
				"-ngld", "99", "-devd", "CUDA1", "-ctkd", "q8_0", "-ctvd", "q8_0",
				"--n-gpu-layers-draft", "40", "--device-draft", "CUDA0",
			},
			want: tape.ServerFlags{
				DraftModel: "d.gguf",
				Other: []string{
					"-ngld 99", "-devd CUDA1", "-ctkd q8_0", "-ctvd q8_0",
					"--n-gpu-layers-draft 40", "--device-draft CUDA0",
				},
			},
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
		got.CPUMoE != "" || got.Threads != "" || got.OverrideTens != nil ||
		got.DraftModel != "" || got.DraftMax != "" || got.DraftMin != "" || got.DraftPMin != "" {
		t.Errorf("ParseFlags invented a value: %+v", got)
	}
}
