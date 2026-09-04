//go:build tinygo

package tinyshim

import "unsafe"

// xxh3's own portable accumulators, which its accum_generic.go compiles on
// every architecture and its amd64 and arm64 files call when the vector units
// are absent. Pulled in by name here so the vector entry points below can
// hand their work to them: the signatures are the vector routines' own, and
// the arithmetic is the reference implementation the vector code was written
// against.

//go:linkname xxh3AccumScalar github.com/zeebo/xxh3.accumScalar
func xxh3AccumScalar(acc *[8]uint64, data, key unsafe.Pointer, n uint64)

//go:linkname xxh3AccumBlockScalar github.com/zeebo/xxh3.accumBlockScalar
func xxh3AccumBlockScalar(acc *[8]uint64, data, key unsafe.Pointer)
