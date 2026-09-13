package tape

// Where a run's weights live, as one word (user, 2026-09-14: "통합메모리
// 계열인지 순수 vram인지 오프로딩인지 좀 더 명확하게 갈리도록 … 이거에 따라서
// 성격이 많이 다르니까").
//
// The three are different machines wearing the same card. A model entirely in
// VRAM decodes at the card's bandwidth and its rate barely moves with context;
// a model whose experts sit in host RAM is bounded by a bus an order of
// magnitude slower and can fall off a cliff the moment the page cache cannot
// hold it. A reader comparing two cards has to know which of those they are
// looking at before any of the figures mean anything, and until now they had
// to work it out from a bar.
//
// Unified memory — Apple Silicon, Jetson, an APU — is a fourth kind and is
// deliberately NOT derived here. Nothing toktape can read today observes it:
// the host collector is /proc-only, so it never runs on a Mac, and "the GPU
// reports as much memory as the host has" would call a 24 GB card in a 24 GB
// box unified, which is a guess dressed as a reading. It arrives with the
// collector that can see it (TTP-15).
type MemoryShape string

// The values of MemoryShape.
const (
	// ShapeUnknown: no placement was derived, or it has no bytes.
	ShapeUnknown MemoryShape = ""
	// ShapeVRAM: every byte the model has is on the GPUs.
	ShapeVRAM MemoryShape = "vram"
	// ShapeOffload: the model is split between VRAM and host RAM.
	ShapeOffload MemoryShape = "offload"
	// ShapeHost: no GPU holds any of it.
	ShapeHost MemoryShape = "host"
)

// MemoryLayout is the shape and the split it was decided from.
type MemoryLayout struct {
	Shape MemoryShape
	// VRAMBytes and HostBytes are the placed bytes on each side. Their sum is
	// the placement's own total, so a share is a share of what was placed and
	// not of the file.
	VRAMBytes, HostBytes int64
	// VRAMShare is VRAMBytes over the two, 0..1. Meaningless when Shape is
	// ShapeUnknown.
	VRAMShare float64
}

// Layout derives where a run's weights live. Bytes on DeviceCPU are host RAM;
// everything else the placement names is a device of its own.
func Layout(p PlacementSummary) MemoryLayout {
	var out MemoryLayout
	for _, d := range p.Devices {
		if d.Bytes <= 0 {
			continue
		}
		if d.Device == DeviceCPU {
			out.HostBytes += d.Bytes
			continue
		}
		out.VRAMBytes += d.Bytes
	}
	total := out.VRAMBytes + out.HostBytes
	if total <= 0 {
		return MemoryLayout{}
	}
	out.VRAMShare = float64(out.VRAMBytes) / float64(total)
	switch {
	case out.HostBytes == 0:
		out.Shape = ShapeVRAM
	case out.VRAMBytes == 0:
		out.Shape = ShapeHost
	default:
		out.Shape = ShapeOffload
	}
	return out
}
