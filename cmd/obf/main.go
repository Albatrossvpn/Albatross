// Command obf prints the argument encoded for -ldflags -X, so a secret
// (the domestic mirror's address) is not plain in the shipped binary; the
// app decodes it with internal/obf.Reveal. Used by the build scripts.
//
//	go run ./cmd/obf "https://mirror.example"
package main

import (
	"fmt"
	"os"

	"github.com/Albatrossvpn/vp/internal/obf"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "-d" {
		fmt.Print(obf.Reveal(os.Args[2]))
		return
	}
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: obf <string> | obf -d <encoded>")
		os.Exit(2)
	}
	fmt.Print(obf.Hide(os.Args[1]))
}
