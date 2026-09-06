# The TinyGo comparison

Crier uses ordinary Go for its release builds. The TinyGo comparison is an experiment. It does not publish a release or change the release pipeline.

The current comparison uses Crier commit `7edaff931ac9367734d806b684389b3ba4caf026`, which includes the release-media feature. Both compilers use that same source. See the [comparison report](./tinygo-comparison-2026-09-05.md) for the toolchain files, commands, test results, and limits.

## Size results

These are executable file sizes in bytes. They are not archive sizes or memory use.

| Target | Go | TinyGo with `-no-debug` | TinyGo after symbol removal |
|---|---:|---:|---:|
| linux/arm64 | 30,277,794 | 29,097,232 | 13,829,248 |
| linux/amd64 | 32,518,306 | 29,119,952 | 16,443,104 |
| darwin/arm64 | 31,205,970 | 25,555,184 | 13,976,832 |

Go uses `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w ..."`. TinyGo uses `tinygo build -opt=z -no-debug -tags noasm -interp-timeout=15m -ldflags "..."`. Both receive the same version, commit, and date values.

The earlier comparison did not remove TinyGo's symbol tables. `-no-debug` removes debugging data but keeps the ELF symbol table and symbol names. In the current Linux ARM64 binary, `.symtab` and `.strtab` contain 15,267,837 bytes. GNU `strip` reduces a copy to 13,829,248 bytes. All allocated sections keep the same sizes and content hashes. This copy is 54.33% smaller than the Go binary.

The macOS sizes are build results only. The TinyGo binary fails during CPU initialization before it can print its version. Its file size does not show that it is usable.

## Rendering and tests

The Linux ARM64 comparison runs the current black-box suite against the measured Go, TinyGo, and stripped TinyGo binaries. The [report](./tinygo-comparison-2026-09-05.md) records each result separately.

Pixel equality is a separate check. Nine of twelve Linux ARM64 PNG fixtures match exactly. One fixture differs at six pixels by one channel level. Two video-game fixtures differ at seven and four pixels, with a maximum channel difference of 38. The latter two exceed Crier's golden-test channel limit. Repeated renders are stable within each compiler. Removing symbols does not change any of the twelve TinyGo images.

A small HTML fixture with only the video-game background reproduces the seven-pixel difference. This identifies the gradient path. It does not yet identify whether the cause is in webrender, Crier's raster code, or compiler floating-point behavior. The comparison does not claim general rendering or template equivalence.

The separate TLS matrix tests a trusted CA, an untrusted CA, plaintext at the TLS port, a complete self-update, and rollback with the server stopped. The update target is a Go binary. These checks do not test a TinyGo-to-TinyGo update.

## The toolchain under test

The compiler is the released fork `0.43.0-net.1`, with Go `1.26.7` and LLVM `22.1.4`. Its source tree is copied before the experiment. The copy receives:

- The local `net` tree at `476a4e3241ee8061a8ae6a311884f6304fda7ea0`.
- Go 1.26.7's `net/http/cookiejar/jar.go` and `punycode.go`.

Net commit `895d524608d7f51773da7c99ef8257cde1c2e196` implements the `ListenConfig` delegation. Commit `476a4e3` adds shutdown before socket close. The published compiler archive does not contain these two changes. Crier's install manifest at the measured source still pins `0.43.0-net.1`.

This experiment does not include later net, loader, or process changes. It does not establish that those changes are published.

Crier's existing [TinyGo shims](../../internal/tinyshim/) supply portable definitions for checksum assembly and CPU feature probes. `-tags noasm` selects the portable paths offered by other dependencies. The experiment changes none of these files.

## What contributes to size

A small program that uses Crier's render API is 25,755,810 bytes with Go and 12,776,704 bytes with stripped TinyGo. This is the render stack, including fonts, raster code, and runtime support. It is not the exclusive size of the webrender module.

Webrender embeds 42 hyphenation dictionary files with 5,915,110 bytes of content. Symbol hashes identify all 42 files in the TinyGo executable. Three German dictionaries account for 3,280,833 bytes. They have different content hashes and must not be treated as duplicate files.

Upstream already tracks [CSS representation work](https://github.com/benoitkugler/webrender/issues/2) and [raster output](https://github.com/benoitkugler/webrender/issues/6). The comparison does not prove a missing webrender fix, so it creates no upstream PR.

## The older spike

[`Dockerfile.tinygo`](../../Dockerfile.tinygo) and [the macOS spike script](../../scripts/tinygo-spike-darwin.sh) remain available. They install the manifest's pinned compiler and write logs under `coverage/tinygo-spike/`. Some comments and logs describe earlier source versions and failures.

The historical 142-test pass predates `7edaff9`. The old Linux ARM64 size pair also used different Crier source for the two compilers. Neither result is evidence for the current source.

Crier 1.0.0 used standard template execution and hit TinyGo reflection stubs. The later executor removed that dependency. Its plain HTML escaping differs from `html/template` contextual escaping for URLs and CSS. A passing sample render does not establish equivalent template semantics.

Use the dated comparison report for current measurements. Keep old spike results labeled with their original source and toolchain.
