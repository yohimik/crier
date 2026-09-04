//go:build !tinygo

package version

// toolchain prefixes what runtime.Version reports. Under gc that is already
// "go1.26.7" and needs no prefix; under TinyGo it is the TinyGo version alone,
// which toolchain_tinygo.go labels.
const toolchain = ""
