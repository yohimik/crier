//go:build tinygo && amd64

package tinyshim

import "unsafe"

// The six routines the SSE, AVX2 and AVX-512 files of zeebo/xxh3 define. On
// amd64 the package picks between them by asking klauspost/cpuid at init,
// which under `-tags noasm` reports no vector unit at all, so the scalar path
// is the one a tiny binary takes anyway; these keep the link honest.

//go:linkname xxh3AccumSSE github.com/zeebo/xxh3.accumSSE
func xxh3AccumSSE(acc *[8]uint64, data, key unsafe.Pointer, n uint64) {
	xxh3AccumScalar(acc, data, key, n)
}

//go:linkname xxh3AccumAVX2 github.com/zeebo/xxh3.accumAVX2
func xxh3AccumAVX2(acc *[8]uint64, data, key unsafe.Pointer, n uint64) {
	xxh3AccumScalar(acc, data, key, n)
}

//go:linkname xxh3AccumAVX512 github.com/zeebo/xxh3.accumAVX512
func xxh3AccumAVX512(acc *[8]uint64, data, key unsafe.Pointer, n uint64) {
	xxh3AccumScalar(acc, data, key, n)
}

//go:linkname xxh3AccumBlockSSE github.com/zeebo/xxh3.accumBlockSSE
func xxh3AccumBlockSSE(acc *[8]uint64, data, key unsafe.Pointer) {
	xxh3AccumBlockScalar(acc, data, key)
}

//go:linkname xxh3AccumBlockAVX2 github.com/zeebo/xxh3.accumBlockAVX2
func xxh3AccumBlockAVX2(acc *[8]uint64, data, key unsafe.Pointer) {
	xxh3AccumBlockScalar(acc, data, key)
}

//go:linkname xxh3AccumBlockAVX512 github.com/zeebo/xxh3.accumBlockAVX512
func xxh3AccumBlockAVX512(acc *[8]uint64, data, key unsafe.Pointer) {
	xxh3AccumBlockScalar(acc, data, key)
}
