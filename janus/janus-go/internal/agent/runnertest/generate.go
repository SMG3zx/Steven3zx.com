//go:build ignore

// generate assembles the .wat fixtures in testdata into the .wasm files the
// contract suite embeds, so running the suite needs no WebAssembly toolchain.
// Run it with `go generate ./internal/adapters/runnertest` (needs cgo).
package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	wasmtime "github.com/bytecodealliance/wasmtime-go/v49"
)

func main() {
	sources, err := filepath.Glob("testdata/*.wat")
	if err != nil {
		log.Fatal(err)
	}
	for _, source := range sources {
		text, err := os.ReadFile(source)
		if err != nil {
			log.Fatal(err)
		}
		wasm, err := wasmtime.Wat2Wasm(string(text))
		if err != nil {
			log.Fatalf("%s: %v", source, err)
		}
		if err = os.WriteFile(strings.TrimSuffix(source, ".wat")+".wasm", wasm, 0o644); err != nil {
			log.Fatal(err)
		}
	}
}
