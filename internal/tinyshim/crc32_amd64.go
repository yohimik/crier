//go:build tinygo && amd64

package tinyshim

import (
	hashcrc32 "hash/crc32"
	_ "unsafe" // for go:linkname
)

// The routines crc32_amd64.s of klauspost/crc32 defines with SSE 4.2 and
// PCLMULQDQ, called when golang.org/x/sys/cpu reports them.

//go:linkname crc32CastagnoliSSE42 github.com/klauspost/crc32.castagnoliSSE42
func crc32CastagnoliSSE42(crc uint32, p []byte) uint32 {
	return rawUpdate(crc, castagnoli, p)
}

// castagnoliSSE42Triple advances three crcs over three buffers at once,
// `rounds` rounds of 24 bytes each, which is how the assembly keeps three
// CRC32 instructions in flight. Three sequential updates over the same
// prefixes are the same arithmetic.
//
//go:linkname crc32CastagnoliSSE42Triple github.com/klauspost/crc32.castagnoliSSE42Triple
func crc32CastagnoliSSE42Triple(crcA, crcB, crcC uint32, a, b, c []byte, rounds uint32) (uint32, uint32, uint32) {
	n := int(rounds) * 24
	return rawUpdate(crcA, castagnoli, a[:n]),
		rawUpdate(crcB, castagnoli, b[:n]),
		rawUpdate(crcC, castagnoli, c[:n])
}

//go:linkname crc32CastagnoliCLMULAvx512 github.com/klauspost/crc32.castagnoliCLMULAvx512
func crc32CastagnoliCLMULAvx512(crc uint32, p []byte) uint32 {
	return rawUpdate(crc, castagnoli, p)
}

//go:linkname crc32IEEECLMUL github.com/klauspost/crc32.ieeeCLMUL
func crc32IEEECLMUL(crc uint32, p []byte) uint32 {
	return rawUpdate(crc, hashcrc32.IEEETable, p)
}

//go:linkname crc32IEEECLMULAvx512 github.com/klauspost/crc32.ieeeCLMULAvx512
func crc32IEEECLMULAvx512(crc uint32, p []byte) uint32 {
	return rawUpdate(crc, hashcrc32.IEEETable, p)
}
