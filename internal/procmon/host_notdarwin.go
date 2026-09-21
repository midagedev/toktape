//go:build !darwin

package procmon

import (
	"fmt"

	"github.com/midagedev/toktape/internal/tape"
)

// readLiveHost is the empty seat on every platform but darwin. HostInfo
// reaches for it only when there is no procfs and the caller asked about
// the live machine; here that question still has no answer, so HostInfo
// falls back to the /proc error it would have reported anyway and the
// behaviour off darwin is exactly what it was. A platform that gains a live
// reader adds host_<goos>.go and narrows the constraint above, never an
// edit to HostInfo.
func readLiveHost() (tape.HostInfo, error) {
	return tape.HostInfo{}, fmt.Errorf("procmon: live host info: %w", ErrUnsupported)
}
