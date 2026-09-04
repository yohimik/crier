//go:build tinygo && arm64

package tinyshim

import (
	hashcrc32 "hash/crc32"
	_ "unsafe" // for go:linkname
)

// The two routines crc32_arm64.s of klauspost/crc32 defines with the CRC32
// instructions, called when golang.org/x/sys/cpu reports them.

//go:linkname crc32CastagnoliUpdate github.com/klauspost/crc32.castagnoliUpdate
func crc32CastagnoliUpdate(crc uint32, p []byte) uint32 {
	return rawUpdate(crc, castagnoli, p)
}

//go:linkname crc32IEEEUpdate github.com/klauspost/crc32.ieeeUpdate
func crc32IEEEUpdate(crc uint32, p []byte) uint32 {
	return rawUpdate(crc, hashcrc32.IEEETable, p)
}
