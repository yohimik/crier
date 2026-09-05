package main

import (
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	for _, path := range os.Args[1:] {
		f, err := elf.Open(path)
		if err != nil {
			panic(err)
		}
		var rows []map[string]any
		for _, s := range f.Sections {
			r := map[string]any{"name": s.Name, "bytes": s.Size, "type": s.Type.String(), "allocated": s.Flags&elf.SHF_ALLOC != 0}
			if s.Type != elf.SHT_NOBITS {
				data, err := s.Data()
				if err != nil {
					panic(err)
				}
				h := sha256.Sum256(data)
				r["sha256"] = hex.EncodeToString(h[:])
			}
			rows = append(rows, r)
		}
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"file": path, "sections": rows}); err != nil {
			panic(err)
		}
		if err := f.Close(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
