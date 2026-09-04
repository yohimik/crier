//go:build tinygo

package tinyshim

import (
	hashcrc32 "hash/crc32"
	_ "unsafe" // for go:linkname
)

// The tables klauspost/crc32's assembly stands in for. The standard library's
// Castagnoli table is the same polynomial (0x82f63b78) klauspost's generic
// path builds, and IEEETable is the one every CRC-32 agrees on.
var castagnoli = hashcrc32.MakeTable(hashcrc32.Castagnoli)

// rawUpdate is the contract klauspost's assembly routines share: the caller
// hands over the crc already inverted and inverts the result itself
// (`^castagnoliUpdate(^crc, p)`), so the routine works on the raw register
// value with no inversion of its own. hash/crc32.Update takes and returns the
// conventional, inverted form, so it is wrapped in two complements: the pair
// cancels the caller's, and what comes out is the same crc the generic path
// would have produced.
func rawUpdate(crc uint32, tab *hashcrc32.Table, p []byte) uint32 {
	return ^hashcrc32.Update(^crc, tab, p)
}
