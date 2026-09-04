//go:build tinygo

// Package tinyshim supplies, under TinyGo only, the handful of symbols crier's
// dependencies declare in Go assembly.
//
// Nothing a release ships is built with TinyGo — see docs/operations/tinygo.md
// for why a fork-built crier links but cannot render — so today this package
// is what lets the TinyGo spike (Dockerfile.tinygo) link crier at all and ask
// it its questions. It is written as the release would need it, so the day
// the toolchain's reflect catches up, the tiny build is a Dockerfile stage
// away rather than a porting job.
//
// TinyGo compiles no .s files. Most of the libraries minio-go (the S3 stage)
// leans on for its checksums keep a pure-Go path behind a build tag — cpuid
// and crc64nvme behind `noasm`, klauspost/compress behind `purego`, which
// TinyGo sets on its own — and the tiny build passes `-tags noasm` for those.
// Two do not: klauspost/crc32 and zeebo/xxh3 select their assembly by file
// name, so under TinyGo the declarations stay and the definitions never
// arrive, and the link fails on them.
//
// This package is the definitions. Each function here is linknamed onto the
// symbol the library declared and implemented with the portable code the same
// library already ships for the architectures its assembly does not cover:
// xxh3's scalar accumulators, reached through a second linkname, and for
// crc32 the standard library's own table-driven hash/crc32, which under
// TinyGo's `purego` tag is the same slicing algorithm klauspost's generic
// file carries. So a tiny binary hashes exactly what a gc binary hashes; it
// merely takes the road the library would take on a machine without the
// instructions.
//
// The third library is golang.org/x/sys/cpu, which both of the above consult
// to choose a road. TinyGo sets the `gc` build tag, so the package's gc files
// are compiled and their feature probes — CPUID and XGETBV on amd64, the
// ID_AA64* system registers on arm64 — are declared with no assembly behind
// them. The shims answer as a processor with no optional features, which is
// the answer that sends every consumer down the portable road above.
//
// Every file carries the tinygo build tag, and cmd/crier imports the package
// under the same tag, so the gc build never sees a line of this: go vet,
// golangci-lint and the release binaries are untouched. Under gc the
// bodiless declarations below would be errors; under TinyGo they are the
// ordinary way to name a symbol another package defines.
//
// The libraries only call these on a CPU whose features they detect through
// golang.org/x/sys/cpu or klauspost/cpuid. Whether TinyGo reports those
// features is the library's business; the shims are correct either way,
// which is why they compute rather than panic.
package tinyshim

import _ "unsafe" // for go:linkname
