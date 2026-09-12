//go:build !linux

package procmon

// defaultRoot has no live procfs to return off Linux. The parsers in this
// package still work against a fixture tree or a tree copied from a Linux
// host, so only the entry points that read the live filesystem fail here.
func defaultRoot() (string, error) { return "", ErrUnsupported }
