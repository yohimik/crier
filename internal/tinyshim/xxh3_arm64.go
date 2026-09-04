//go:build tinygo && arm64

package tinyshim

import "unsafe"

// The two routines accum_vector_neon_arm64.s of zeebo/xxh3 defines. On arm64
// the package holds hasNEON as a constant true, so these are the only
// accumulators it ever calls there.

//go:linkname xxh3AccumNEON github.com/zeebo/xxh3.accumNEON
func xxh3AccumNEON(acc *[8]uint64, data, key unsafe.Pointer, n uint64) {
	xxh3AccumScalar(acc, data, key, n)
}

//go:linkname xxh3AccumBlockNEON github.com/zeebo/xxh3.accumBlockNEON
func xxh3AccumBlockNEON(acc *[8]uint64, data, key unsafe.Pointer) {
	xxh3AccumBlockScalar(acc, data, key)
}
