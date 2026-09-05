# Crier compiler comparison, 5 September 2026

The stripped TinyGo Linux ARM64 binary is **13,829,248 bytes**. The ordinary Go binary is **30,277,794 bytes**. Both use the same Crier source. Both pass the current 143-test end-to-end suite. The reduction is **54.33%**. The separate pixel comparison has two fixtures outside the golden tolerance, so this is not complete rendering equivalence.

This report does not approve or publish a TinyGo release. The experiment changes no Crier application source or release pipeline.

## Source and toolchain

| Input | Exact value |
|---|---|
| Crier source | `7edaff931ac9367734d806b684389b3ba4caf026` |
| Source feature | `feat(crier): publish release media together` |
| Go used to build Crier | `go1.26.7 darwin/arm64` |
| TinyGo compiler | Released `0.43.0-net.1 darwin/arm64`, LLVM `22.1.4` |
| TinyGo tag commit | `1b2b1d163409b9f151d96dc494cf27b1114d39e5` |
| Local TinyGo checkout at copy time | `3a9fc1feba199b8d5735b1b4cd3830cf49be6b95` |
| Copied net commit | `476a4e3241ee8061a8ae6a311884f6304fda7ea0` |
| Cookiejar overlay | `jar.go` and `punycode.go` from Go 1.26.7 |
| Webrender | `v0.0.14`, commit `5022e53cd46092a2054e80c2f5f990f9bad545d7` |
| Host | Apple M5 Pro, 48 GiB RAM, macOS 26.6.2, build 25G83 |
| Docker client and server | `29.6.1` |
| Linux test image | `golang@sha256:26326682769ca980f8f1d3b1f52be2dd1c1d25270e3de3fe0c97d6bb65df3556` |
| Linux test harness | Go 1.26.6, linux/arm64, root, FFmpeg 7.1.5 |
| Linux symbol removal | GNU Binutils 2.44, native and x86_64 cross `strip` |

The compiler executable itself was built with Go 1.27.0, as its Go build metadata reports. It uses Go 1.26.7 to compile this Crier source. These are separate version facts.

Compiler SHA-256 values:

```text
TinyGo 05e117ab5d5f0e085c4af5bd49e9a797d3041857d53045d2f7c3612dd6d74866
Go     9da68c657a8344623d37fc9dc048d845011736409249bc924dd9af47a61594e6
```

The toolchain was copied before the builds. A directory comparison against the cached release found only the two changed net files and the added cookiejar directory. The net source manifest is in the evidence archive.

This run does not use the later net `0bdf14f`, loader PR #5655, or later process fixes. It does not claim that a new compiler release is published. The application files, module files, announcement files, examples, and tests match the pinned Crier commit.

## Build options and sizes

The builds run in sequence. Each uses two workers, `GOMAXPROCS=2`, and the Go heap limit `GOMEMLIMIT=8GiB`. This heap limit does not cover all LLVM allocations. The largest measured TinyGo maximum resident set was 7,228,719,104 bytes. The Linux test container has a separate hard limit of 10 GiB and two CPUs.

Both Crier builds receive these values:

```text
Version=0.0.0-spike
Commit=7edaff931ac9
Date=2026-09-05T00:00:00Z
```

The [build script](../../scripts/size-comparison-build.sh) uses `CGO_ENABLED=0`, Go `-p 2 -trimpath -ldflags "-s -w ..."`, and TinyGo `-p 2 -opt=z -no-debug -tags noasm -interp-timeout=15m -ldflags "..."`. The existing Crier shims are unchanged. These are uncompressed file sizes, not memory measurements.

| Target | Go bytes | TinyGo bytes | Stripped TinyGo bytes | Stripped reduction |
|---|---:|---:|---:|---:|
| linux/arm64 | 30,277,794 | 29,097,232 | 13,829,248 | 54.33% |
| linux/amd64 | 32,518,306 | 29,119,952 | 16,443,104 | 49.43% |
| darwin/arm64 | 31,205,970 | 25,555,184 | 13,976,832 | 55.21% |

The original TinyGo ARM64 output saves only 3.90%. It retains 15,267,837 bytes in `.symtab` and `.strtab`. Removing symbols from a copy saves 15,267,984 file bytes, including changes to headers and alignment. Every allocated ELF section retains its size and content hash. The macOS copy was processed with the host `/usr/bin/strip`; neither macOS TinyGo size is a usable-release result.

| Linux ARM64 section | Go bytes | TinyGo bytes |
|---|---:|---:|
| `.text` | 9,943,556 | 4,024,892 |
| `.rodata` | 9,340,665 | 8,134,144 |
| `.gopclntab` | 8,199,594 | absent |
| `.symtab` | absent | 4,446,456 |
| `.strtab` | absent | 10,821,381 |

These sections do not constitute a complete file-size sum. The Go runtime's `.gopclntab` remains in its stripped executable. The experiment does not remove runtime data.

## End-to-end results

Each run uses `CRIER_E2E_BINARY` to test the measured binary. It does not rebuild the binary under test. The self-update tests build separate Go update targets. The current suite has 143 tests; `TestMain` is not a test case.

| Target and binary | Pass | Fail | Skip | Exit | Seconds |
|---|---:|---:|---:|---:|---:|
| linux/arm64 Go | 143 | 0 | 0 | 0 | 124.200 |
| linux/arm64 TinyGo | 143 | 0 | 0 | 0 | 84.658 |
| linux/arm64 stripped TinyGo | 143 | 0 | 0 | 0 | 83.338 |
| darwin/arm64 Go | 142 | 0 | 1 | 0 | 87.803 |
| darwin/arm64 TinyGo | 7 | 136 | 0 | 1 | 13.285 |

These durations are run records, not a performance benchmark. Later runs use warm build caches. The Linux runs cover release-media, announcement, platform uploads, TLS verification, listener serving and shutdown, self-update, and rollback. Linux AMD64 starts and renders all comparison fixtures through Docker's architecture support, but its full suite was not run.

The macOS Go skip is the trusted-CA half of `TestPublishOverTLSVerifiesThePlatform`. The test first checks untrusted-certificate rejection, then skips because the platform verifier does not read `SSL_CERT_FILE`. It does not change the keychain.

The current macOS TinyGo binary exits 134 on `--version`. A debugger stops at address zero with this call sequence:

```text
syscall.rawsyscalln
syscall.syscall6
golang.org/x/sys/cpu.darwinSysctlEnabled
runtime.initAll
runtime.runMain
```

This is a fresh observation on `7edaff9`, not the old spike result. The seven passing cases exercise scripts without running a working Crier CLI. No rebuilt compiler was tested to resolve this failure.

## TLS and pixels

The [TLS script](../../scripts/size-comparison-tls.sh) uses the existing `sufake` source from `Dockerfile.tinygo`. Go, TinyGo, and stripped TinyGo each pass trusted-name and trusted-IP checks, reject an untrusted CA, reject plaintext at the TLS port, complete an update to a Go 1.1.0 binary, and roll back with the server stopped. The server logs completed handshakes and the first ClientHello bytes. This matrix does not test a TinyGo update target.

The [render script](../../scripts/size-comparison-render.sh) uses seed 12345, the seven example projects, the Instagram game variant, and both announcement layouts. Each announcement has two pages. All inputs come from the pinned source.

| Linux ARM64 fixture | Changed pixels | Maximum channel difference | Golden tolerance |
|---|---:|---:|---|
| Nine other PNGs | 0 | 0 | pass |
| event-invite | 6 | 1 | pass |
| video-game-release | 7 | 38 | fail |
| video-game-story | 4 | 38 | fail |

All three images with differences contain 2,073,600 pixels each. The repeated renders are identical within each compiler. All twelve stripped TinyGo PNGs match the original TinyGo PNGs. All twelve Linux AMD64 Go and TinyGo PNGs match exactly.

The [small gradient fixture](../../tools/size-comparison/gradient.html) removes text and fonts from the displayed content. It reproduces the same seven changed pixels as the landscape game card. This narrows the difference to the gradient path. It does not isolate a defect in webrender from Crier's raster code or compiler arithmetic. The actual per-pixel coordinates and differences are in the JSON evidence.

No claim of general template equivalence follows from these tests. Crier's current executor uses plain HTML escaping, which differs from `html/template` contextual escaping in URL and CSS contexts.

## Renderer contribution and upstream work

The [small render program](../../tools/size-comparison/renderer/main.go) uses Crier's hermetic fonts, render API, raster backend, and PNG encoder. Its 320 by 90 output matches between Go and TinyGo after the progress-log prefix is separated from the PNG stream.

| Small render program | Bytes |
|---|---:|
| Go, stripped | 25,755,810 |
| TinyGo, `-no-debug` | 26,445,496 |
| TinyGo, symbols removed | 12,776,704 |

The corresponding stripped programs are 85.1% and 92.4% of the full Crier sizes. These are complete standalone programs with shared runtime and dependency code. Subtraction does not give an exclusive package contribution.

Webrender's embedded hyphenation directory contains 42 `.dic` files with 5,915,110 bytes, plus a 368-byte readme. Each dictionary's SHA-256 prefix matches an embedded symbol in the TinyGo Crier binary. The three German files contain 1,093,611 bytes each and have different hashes. This is retained language data, not proof of redundant content. Removing languages would change behavior; compression was not implemented or benchmarked.

The upstream review checked all four existing PRs and all five issues. The relevant existing work is [CSS representation #2](https://github.com/benoitkugler/webrender/issues/2), [raster output #6](https://github.com/benoitkugler/webrender/issues/6), and the already completed [text exposure #7](https://github.com/benoitkugler/webrender/issues/7). The current open PR is [dependency update #9](https://github.com/benoitkugler/webrender/pull/9). No independent missing webrender fix was proved. No PR was created or updated.

## Reproduction and evidence

Use an isolated worktree at the recorded Crier commit. The [prepare script](../../scripts/size-comparison-prepare.sh) expects the released compiler cache and a clean net checkout at the recorded commit. `SIZE_COMPARISON_CACHE` and `SIZE_COMPARISON_NET` can select those directories. It refuses to overwrite an existing toolchain snapshot.

```sh
sh scripts/size-comparison-prepare.sh
sh scripts/size-comparison-build.sh linux/arm64
sh scripts/size-comparison-build.sh darwin/arm64
sh scripts/size-comparison-build.sh linux/amd64
sh scripts/size-comparison-probes.sh
```

Mount the worktree at `/src` in the recorded ARM64 Go image. Install FFmpeg and the native and x86_64 Binutils packages. Use two CPUs, a 10 GiB memory and swap limit, `GOMAXPROCS=2`, `GOMEMLIMIT=6GiB`, `GOFLAGS=-p=2`, and `GOTOOLCHAIN=local`. Mount the cached Go modules read-only at `/go/pkg/mod`. Inside that container, create the stripped copies and run the tests:

```sh
cd /src
strip -o coverage/size-comparison/bin/tinygo-stripped-linux-arm64 coverage/size-comparison/bin/tinygo-linux-arm64
x86_64-linux-gnu-strip -o coverage/size-comparison/bin/tinygo-stripped-linux-amd64 coverage/size-comparison/bin/tinygo-linux-amd64
sh scripts/size-comparison-test.sh
sh scripts/size-comparison-tls.sh
sh scripts/size-comparison-render.sh linux-arm64
sh scripts/size-comparison-render.sh linux-amd64
```

Run the [native test script](../../scripts/size-comparison-darwin-test.sh) on macOS. The [pixel checker](../../tools/size-comparison/pixels/main.go) compares two output directories and returns nonzero on any pixel difference. The [section checker](../../tools/size-comparison/sections/main.go) records ELF section hashes. The [report script](../../scripts/size-comparison-report.py) collects sizes, hashes, test counts, and dictionary symbols.

The committed [results](./tinygo-comparison-2026-09-05/results.json) contain executable SHA-256 values. The [evidence archive](./tinygo-comparison-2026-09-05/evidence.tar.gz) contains full test logs, build records, TLS logs, source manifests, section records, upstream inventory, and pixel measurements.

The original local artifact directory is `/Users/yohimik/Projects/crier-size-comparison/coverage/size-comparison/`. It also retains the measured binaries, PNGs, and toolchain copy. Crier main and release work remained under the separate release owner's control. This experiment made no release or push.
