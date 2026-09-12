// Command toktape is the black-box tape for local LLM serving.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("toktape dev")
		return
	}
	fmt.Fprintln(os.Stderr, "toktape: not implemented yet (see docs/toktape-spec.ko.md)")
	os.Exit(2)
}
