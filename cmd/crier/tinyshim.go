//go:build tinygo

package main

// The TinyGo build links the assembly stand-ins in; see internal/tinyshim.
// The gc build never compiles this file.
import _ "github.com/yohimik/crier/internal/tinyshim"
