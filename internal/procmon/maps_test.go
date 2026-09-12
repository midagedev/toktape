package procmon_test

import (
	"testing"

	"github.com/midagedev/toktape/internal/procmon"
)

func TestParseMapsLine(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    procmon.Mapping
		wantErr bool
	}{
		{
			name: "file mapping",
			in:   "7f0000000000-7f0500000000 r--p 00000000 103:02 6821001                   /models/gguf/m-00001-of-00009.gguf",
			want: procmon.Mapping{Start: 0x7f0000000000, End: 0x7f0500000000, Perms: "r--p", Offset: 0, Dev: "103:02", Inode: 6821001, Path: "/models/gguf/m-00001-of-00009.gguf"},
		},
		{
			// A path with spaces is why the pathname is taken as the remainder
			// of the line rather than by joining a Fields split.
			name: "path with consecutive spaces",
			in:   "7ef200000000-7ef200100000 r--s 00000000 103:02 6820001                   /models/old  models/scratch.bin",
			want: procmon.Mapping{Start: 0x7ef200000000, End: 0x7ef200100000, Perms: "r--s", Dev: "103:02", Inode: 6820001, Path: "/models/old  models/scratch.bin"},
		},
		{
			name: "anonymous with trailing space",
			in:   "7ee000000000-7ee100000000 rw-p 00000000 00:00 0 ",
			want: procmon.Mapping{Start: 0x7ee000000000, End: 0x7ee100000000, Perms: "rw-p", Dev: "00:00", Inode: 0, Path: ""},
		},
		{
			name: "anonymous with no trailing whitespace",
			in:   "7ee000000000-7ee100000000 rw-p 00000000 00:00 0",
			want: procmon.Mapping{Start: 0x7ee000000000, End: 0x7ee100000000, Perms: "rw-p", Dev: "00:00", Inode: 0, Path: ""},
		},
		{
			name: "pseudo path",
			in:   "7ffd0a2f9000-7ffd0b2f9000 rw-p 00000000 00:00 0                          [stack]",
			want: procmon.Mapping{Start: 0x7ffd0a2f9000, End: 0x7ffd0b2f9000, Perms: "rw-p", Dev: "00:00", Path: "[stack]"},
		},
		{
			name: "deleted suffix stripped",
			in:   "7ef300000000-7ef300400000 r--p 00000000 103:02 6820777                   /models/gguf/x.imatrix (deleted)",
			want: procmon.Mapping{Start: 0x7ef300000000, End: 0x7ef300400000, Perms: "r--p", Dev: "103:02", Inode: 6820777, Path: "/models/gguf/x.imatrix", Deleted: true},
		},
		{
			name: "non-zero offset",
			in:   "7f0500000000-7f0a80000000 r--p 500000000 103:02 6821001                   /models/gguf/m-00001-of-00009.gguf",
			want: procmon.Mapping{Start: 0x7f0500000000, End: 0x7f0a80000000, Perms: "r--p", Offset: 0x500000000, Dev: "103:02", Inode: 6821001, Path: "/models/gguf/m-00001-of-00009.gguf"},
		},
		{name: "no address range", in: "garbage r--p 0 00:00 0", wantErr: true},
		{name: "too few columns", in: "7f00-7f01 r--p 00000000", wantErr: true},
		{name: "end below start", in: "7f0100000000-7f0000000000 r--p 00000000 00:00 0", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := procmon.ParseMaps([]byte(tt.in + "\n"))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseMaps(%q) = %+v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseMaps(%q): %v", tt.in, err)
			}
			if len(got) != 1 {
				t.Fatalf("ParseMaps(%q) returned %d mappings", tt.in, len(got))
			}
			if got[0] != tt.want {
				t.Errorf("ParseMaps(%q) =\n got %+v\nwant %+v", tt.in, got[0], tt.want)
			}
		})
	}
}

func TestMappingSize(t *testing.T) {
	ms, err := procmon.ParseMaps([]byte("7f0000000000-7f0a80000000 r--p 00000000 103:02 1 /m.gguf\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ms[0].Size(), int64(42*gB); got != want {
		t.Errorf("Size = %d, want %d", got, want)
	}
}

func TestSameModelFile(t *testing.T) {
	const split = "/models/gguf/DeepSeek-V3-0324-UD-Q4_K_XL-00001-of-00009.gguf"
	tests := []struct {
		mapped string
		want   bool
	}{
		{split, true},
		{"/models/gguf/DeepSeek-V3-0324-UD-Q4_K_XL-00009-of-00009.gguf", true},
		{"/models/gguf/DeepSeek-V3-0324-UD-Q4_K_XL-00005-of-00009.gguf", true},
		// Same prefix but a different shard count: another quantisation run.
		{"/models/gguf/DeepSeek-V3-0324-UD-Q4_K_XL-00001-of-00012.gguf", false},
		// Another model that happens to live next to it.
		{"/models/gguf/Qwen3-30B-A3B-Q6_K.gguf", false},
		{"/models/gguf/DeepSeek-V3-0324-UD-Q2_K_XL-00002-of-00009.gguf", false},
		// Same name, another directory: a different copy of the weights.
		{"/mnt/scratch/DeepSeek-V3-0324-UD-Q4_K_XL-00002-of-00009.gguf", false},
		{"/models/gguf/DeepSeek-V3-0324-UD-Q4_K_XL.imatrix", false},
		{"[heap]", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := procmon.SameModelFile(split, tt.mapped); got != tt.want {
			t.Errorf("SameModelFile(%q, %q) = %v, want %v", split, tt.mapped, got, tt.want)
		}
	}

	// An unsplit model matches only itself, so the sibling .gguf next to it is
	// never folded in.
	const single = "/models/gguf/Qwen3-30B-A3B-Q6_K.gguf"
	if !procmon.SameModelFile(single, single) {
		t.Error("an unsplit model must match its own path")
	}
	if procmon.SameModelFile(single, "/models/gguf/Qwen3-30B-A3B-Q4_K_M.gguf") {
		t.Error("an unsplit model must not match a sibling .gguf")
	}
}

func TestMappedFileBytes(t *testing.T) {
	got, err := procmon.MappedFileBytes(fsRoot, 1234, modelPath)
	if err != nil {
		t.Fatal(err)
	}
	// Shards 1-8 are 42 GiB each and shard 9 is 30 GiB. Shard 1 is split
	// across two VMAs (20 GiB + 22 GiB); the ranges are disjoint, so summing
	// them must give 42 GiB, not 84.
	const want = 8*42*gB + 30*gB
	if got != want {
		t.Errorf("MappedFileBytes = %d, want %d", got, want)
	}
	// The libraries, the heap, the stack, the deleted imatrix, the scratch
	// file in a directory whose name has a space, and the unrelated Qwen GGUF
	// in the same directory are all in the fixture and none of them counts.
	if got >= 400*gB {
		t.Errorf("MappedFileBytes = %d: something other than the model was counted", got)
	}
}

func TestMappedFileBytesUnknownModel(t *testing.T) {
	got, err := procmon.MappedFileBytes(fsRoot, 1234, "/models/gguf/NotMapped-Q4_K_M.gguf")
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Errorf("MappedFileBytes for an unmapped model = %d, want 0", got)
	}
}

func TestParseMapsFixtureCounts(t *testing.T) {
	ms, err := procmon.ParseMaps(readFixture(t, "proc/1234/maps"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 25 {
		t.Fatalf("parsed %d mappings, want 25", len(ms))
	}
	var deleted, anon, withSpace int
	for _, m := range ms {
		if m.Deleted {
			deleted++
		}
		if m.Path == "" {
			anon++
		}
		if m.Path == "/models/gguf/old models/scratch.bin" {
			withSpace++
		}
	}
	if deleted != 1 || anon != 1 || withSpace != 1 {
		t.Errorf("deleted=%d anon=%d withSpace=%d, want 1/1/1", deleted, anon, withSpace)
	}
}
