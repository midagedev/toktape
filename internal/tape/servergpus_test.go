package tape

import (
	"reflect"
	"testing"
)

// TTP-209 (2026-10-12). A bloomery clip ran with its server pinned to the
// RTX A6000; the card's RIG still named both cards. The server's own VRAM
// (GPUSample.ProcBytes) says which card worked. The fixture is that clip's
// real tape: GPU 0 (RTX 3090) holds no server bytes, GPU 1 (A6000) does.

func clipFixture(t *testing.T) *Tape {
	t.Helper()
	tp, err := Read("testdata/clip-pinned-a6000.tape")
	if err != nil {
		t.Fatal(err)
	}
	return tp
}

func TestUsedIndexesNamesTheCardsTheServerHeld(t *testing.T) {
	used, known := usedIndexes(clipFixture(t).Summary.GPUsAtEnd)
	if !known || !reflect.DeepEqual(used, []int{1}) {
		t.Errorf("clip fixture: usedIndexes = %v, %v; want [1], true", used, known)
	}

	// No ProcBytes anywhere (old tape, remote server, no compute-apps read).
	none := []GPUSample{{Index: 0, UsedBytes: 5}, {Index: 1, UsedBytes: 9}}
	if used, known := usedIndexes(none); used != nil || known {
		t.Errorf("no ProcBytes: usedIndexes = %v, %v; want nil, false", used, known)
	}

	// Every card used, reported out of order: index order, all of them.
	all := []GPUSample{{Index: 1, ProcBytes: 3}, {Index: 0, ProcBytes: 4}}
	if used, known := usedIndexes(all); !known || !reflect.DeepEqual(used, []int{0, 1}) {
		t.Errorf("all used: usedIndexes = %v, %v; want [0 1], true", used, known)
	}
}

func TestServerRigCountsTheIdleCards(t *testing.T) {
	tp := clipFixture(t)
	gpus, idle := ServerRig(&tp.Summary)
	if len(gpus) != 1 || gpus[0].Index != 1 || idle != 1 {
		t.Errorf("clip: ServerRig = %v, idle %d; want the A6000 alone, 1 idle", gpus, idle)
	}
	// Unknown: the host list, untouched.
	s := RunSummary{Host: HostInfo{GPUs: []GPUInfo{{Index: 0}, {Index: 1}}}}
	if gpus, idle := ServerRig(&s); len(gpus) != 2 || idle != 0 {
		t.Errorf("unknown: ServerRig = %v, idle %d; want both, 0", gpus, idle)
	}
	// Used index not on the host: do not guess, keep the host list.
	s.GPUsAtEnd = []GPUSample{{Index: 7, ProcBytes: 1}}
	if gpus, idle := ServerRig(&s); len(gpus) != 2 || idle != 0 {
		t.Errorf("off-host index: ServerRig = %v, idle %d; want both, 0", gpus, idle)
	}
}
