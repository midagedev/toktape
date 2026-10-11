//go:build !unix

package main

import "os"

// foregroundGroup is false where there is no process-group model: nothing
// here can be asked through a terminal that is known to answer.
func foregroundGroup(*os.File) bool { return false }
