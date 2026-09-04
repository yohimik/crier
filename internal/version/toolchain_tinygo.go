//go:build tinygo

package version

// toolchain labels the version TinyGo's runtime.Version reports, which is the
// TinyGo version ("0.43.0-net.1") rather than a Go version, so the version
// line says which compiler built the binary rather than printing a number
// that reads like a Go version and is not one.
const toolchain = "tinygo "
