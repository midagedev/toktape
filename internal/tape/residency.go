package tape

// HostResidency is where the CPU-placed weights actually are (TTP-63,
// user 2026-09-14: "cpu 388g 찍혀있는데 이게 ram이랑 nvme랑 구분이 안되나").
//
// DeviceCPU in a placement is llama.cpp's backend assignment: the tensors the
// server maps into host memory and runs on the CPU. It says nothing about
// whether they are in RAM. On a box whose RAM is smaller than what was placed
// there, the rest is read from the model file on every touch — that is what
// the major-fault sparkline shows — and the tape already carries the figure
// that separates the two: the process's file-backed resident set, which is
// the model mapping's pages that are in RAM at that instant (the loader
// unmaps the fragments it uploaded to a GPU, so nothing else is in it).
//
//	resident = min(RSSFile, placed)
//	paged    = placed − resident
//
// Both are derived here, never recorded, so every surface — the pane, the
// modal, the text card, the PNG — prints the same split from the same sample,
// and it moves during a run as the sample does.
//
// Ok is false when the split cannot be read: no CPU placement, or no /proc
// sample (a remote server). A remote server's CPU placement is then just
// "placed"; printing 0 paged for it would be a default nobody observed.
type HostResidency struct {
	Placed   int64 // bytes placed on DeviceCPU
	Resident int64 // of those, in RAM at the sample
	Paged    int64 // of those, read from the file on touch
	Ok       bool  // false: Resident and Paged were not derivable
}

// Residency derives the split from a placement and one memory sample. The
// sample's RSSFileBytes is the reading; a sample with none (RSSBytes == 0)
// is no reading, whatever RSSFileBytes says.
func Residency(p PlacementSummary, m MemSample) HostResidency {
	var placed int64
	for _, d := range p.Devices {
		if d.Device == DeviceCPU {
			placed += d.Bytes
		}
	}
	r := HostResidency{Placed: placed}
	if placed <= 0 || m.RSSBytes <= 0 {
		return r
	}
	r.Resident = min(m.RSSFileBytes, placed)
	r.Paged = placed - r.Resident
	r.Ok = true
	return r
}
