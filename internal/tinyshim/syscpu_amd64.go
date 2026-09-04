//go:build tinygo && amd64

package tinyshim

import _ "unsafe" // for go:linkname

// golang.org/x/sys/cpu reads the processor's feature bits through two
// instructions its cpu_gc_x86.s wraps for the gc toolchain and its
// cpu_gccgo_x86.c wraps for gccgo. Neither file is compiled by TinyGo, so the
// declarations stay and the definitions never arrive.
//
// These answer as a processor with no feature bits at all. That is what the
// package's own doinit takes for "nothing to detect": a maximum leaf of zero
// ends the detection before a single flag is set, and every consumer —
// klauspost/crc32 among crier's dependencies — falls back to the portable
// code it carries for exactly that case. A tiny binary loses nothing but the
// hardware fast paths TinyGo could not have linked anyway.

//go:linkname sysCPUID golang.org/x/sys/cpu.cpuid
func sysCPUID(eaxArg, ecxArg uint32) (eax, ebx, ecx, edx uint32) {
	return 0, 0, 0, 0
}

//go:linkname sysXGETBV golang.org/x/sys/cpu.xgetbv
func sysXGETBV() (eax, edx uint32) {
	return 0, 0
}
