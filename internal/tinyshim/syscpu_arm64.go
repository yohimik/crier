//go:build tinygo && arm64

package tinyshim

import _ "unsafe" // for go:linkname

// golang.org/x/sys/cpu reads the arm64 feature registers (ID_AA64ISAR0_EL1
// and friends) through four one-instruction routines its cpu_arm64.s wraps
// for the gc toolchain. TinyGo compiles no .s files, so the declarations stay
// and the definitions never arrive.
//
// These answer as a processor whose feature registers are all zero. The
// package parses that as no optional feature present, which is what every
// consumer falls back from into the portable code it carries; on linux the
// package reads HWCAP from the auxiliary vector first and only consults these
// registers when that says it may. Nothing is lost but hardware fast paths
// TinyGo could not have linked anyway.

//go:linkname sysGetISAR0 golang.org/x/sys/cpu.getisar0
func sysGetISAR0() uint64 { return 0 }

//go:linkname sysGetISAR1 golang.org/x/sys/cpu.getisar1
func sysGetISAR1() uint64 { return 0 }

//go:linkname sysGetPFR0 golang.org/x/sys/cpu.getpfr0
func sysGetPFR0() uint64 { return 0 }

//go:linkname sysGetZFR0 golang.org/x/sys/cpu.getzfr0
func sysGetZFR0() uint64 { return 0 }
