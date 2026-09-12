//go:build linux

package procmon

// defaultRoot returns the root of the live procfs tree.
func defaultRoot() (string, error) { return "/", nil }
