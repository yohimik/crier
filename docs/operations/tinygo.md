# The TinyGo spike

Read this page to understand why every one of crier's six release binaries is built with the Go compiler, and what a TinyGo fork can and cannot do with crier today. It describes a spike: an experiment kept in the repository because its answer is a version number away from changing, not a gate any job runs, and not something a release ships.

TinyGo produces smaller binaries than gc for the same source. A CLI distributed as six platform binaries has an obvious interest in that, so the question was asked properly, with the whole answer written down. It is [dispat's spike](https://dispat.dev/internals/tinygo/), brought over and asked of crier, and the answer is different: dispat ships two fork-built binaries beside its six, and crier cannot, yet.

## What the spike is

[`Dockerfile.tinygo`](../../Dockerfile.tinygo) at the repository root, run by the `tinygo-spike` script in [`dispat.yaml`](../../dispat.yaml):

```sh
dispat run tinygo-spike
```

It is a chain of build targets, one question each, and no target aborts. A spike exists for its log, and a build that dies on its first failure reports one fact where the matrix needs all of them, so every step records its own exit status and the export stage collects the logs whatever they say. They land in `coverage/tinygo-spike/`.

| Stage | Asks |
|-------|------|
| `tinygo-spike-build` | Does upstream TinyGo build crier for four unix targets, and how large is the result beside its gc twin? |
| `tinygo-spike-run` | Does the binary run, report its platform, render a card, and reach the network? |
| `tinygo-spike-net` | The four network layers one at a time, both toolchains, plus what `-X` does to a version variable and what actually arrives on a TLS socket. |
| `tinygo-spike-fork` | Does the [fork](#the-fork) install and report its version, and is `net/http/cookiejar` in its tree afterwards? |
| `tinygo-spike-selfupdate` | Does crier update itself over real TLS when built by the fork, and does the fork build render? |
| `tinygo-spike-e2e` | Does the whole black-box suite pass against a fork-built crier? |
| `tinygo-spike-test` | Does `tinygo test` run each package's unit tests, one package at a time under a bound? |

Buildx reaches only linux, so the darwin binaries the spike builds are never executed there. The other half runs natively on a Mac:

```sh
sh scripts/tinygo-spike-darwin.sh
```

It mirrors the same stages for `darwin/arm64` and, when Rosetta answers, `darwin/amd64`, writing `coverage/tinygo-spike/darwin-*.log`. It extracts the container's probe programs from the Dockerfile at run time rather than carrying copies, so the two halves cannot drift apart.

## The verdict for upstream TinyGo

**Upstream TinyGo does not build crier at all.** The build stops at the first import it cannot find: minio-go, the S3 stage, imports `net/http/cookiejar` through `golang.org/x/net/publicsuffix`, and TinyGo takes its own `net/http` tree whole rather than merging it with Go's, so the package "is not in std". `build.log` records it for all four targets, and the sizes table has nothing to measure.

Behind that import sits the reason dispat's spike already found: upstream's `crypto/tls` is a stub. It returns no errors, completes a handshake that never happened and writes plaintext to port 443, which the `tls-reality` rows of `net.log` record from the far end of the wire as `first-bytes=504c41 clienthello=false`. Every network path crier has — fourteen platform APIs, S3, self-update's listing and download — is https. So nothing is built with upstream, and the file is kept because both answers are a version number away from changing.

## The fork

[`github.com/yohimik/tinygo`](https://github.com/yohimik/tinygo) closes the TLS gap: a real `crypto/tls` with a real certificate verifier, over a netdev that speaks to the host's sockets on linux and darwin, real process spawning and real signal delivery. dispat's page describes what each of its releases answered. crier installs it through [`scripts/install-tools.sh`](../../scripts/install-tools.sh), the repository's install manifest, which is where the fork's version is pinned and the only place it is written down:

```sh
sh scripts/install-tools.sh --version tinygo    # the pin, and nothing else
INSTALL_TOOLS_PREFIX=~/.local sh scripts/install-tools.sh tinygo
```

Two callers read it, the spike's `tinygo-spike-fork` stage and the darwin half, and each runs the manifest rather than repeating the command, so the two halves can never be asking about different forks.

### What crier needed on top of the fork

The fork was built for dispat, whose dependencies are few. crier's are not, and three things stood between the fork and a linked binary. None is a change to the fork; all three live in this repository, and each is written to be deleted the day the fork or the library makes it unnecessary.

**`net/http/cookiejar`.** The fork's `net/http` is still TinyGo's own tree, and it carries no `cookiejar`. The package is pure Go over `net/http`'s exported API, and Go's own copy compiles against the fork's `net/http` unchanged, so the install manifest lays it over the installed tree, taken from the Go the toolchain builds with. `fork.log` lists the folder afterwards.

**Assembly.** TinyGo compiles no `.s` files, and four of minio-go's checksum libraries carry hand-written assembly: `klauspost/cpuid`, `minio/crc64nvme`, `klauspost/crc32` and `zeebo/xxh3`, with `minio/md5-simd` on amd64. The first two and md5-simd keep a pure-Go path behind the `noasm` build tag, and the spike builds with `-tags noasm`. The other two select their assembly by file name, so under TinyGo the declarations stay and the definitions never arrive. [`internal/tinyshim`](../../internal/tinyshim/) is the definitions: each function is linknamed onto the symbol the library declared and implemented with the portable code the same library ships for the architectures its assembly does not cover — xxh3's scalar accumulators and, for crc32, the standard library's table-driven `hash/crc32`. A TinyGo binary hashes exactly what a gc binary hashes; it takes the road the library would take on a machine without the instructions.

**Feature detection.** TinyGo sets the `gc` build tag, so `golang.org/x/sys/cpu` compiles the files that read processor features through assembly the gc toolchain would have provided: `CPUID` and `XGETBV` on amd64, the `ID_AA64*` system registers on arm64. The same package answers those with a processor reporting no optional features, which is the answer that sends every consumer down the portable road above.

Every file of the shim package carries the `tinygo` build tag and `cmd/crier` imports it under the same tag, so the gc build, `go vet`, `golangci-lint` and the six release binaries never see a line of it.

**A Go the fork knows.** The fork builds against Go 1.26 and not against 1.27: Go 1.27's `internal/runtime/maps` wants an `abi.MapType` the fork's `internal/abi` override has not got, and a fork build on the upstream image, whose Go is 1.27, stops in the standard library before it reaches crier. The spike's fork stages therefore run on the `golang:1.26` image rather than on the upstream chain's, and the darwin half pins the same Go. A fork bump that moves to a newer Go moves `GO_VERSION` at the top of `Dockerfile.tinygo` and the pin in the darwin script with it.

## What the fork answered

With the three in place, the fork links crier for `linux/amd64`, `linux/arm64` and `darwin/arm64`.

**At 1.0.0, a fork-built crier rendered nothing.** On linux it started, reported its version and platform, and panicked on its first template:

```
panic: unimplemented: (reflect.Type).NumOut()
```

`text/template` inspects every function it is handed — the built-ins included — through `reflect.Type.NumOut`, `NumIn`, `In` and `Out`, and calls them through `reflect.Value.Call`. TinyGo's reflect implements none of those, in the fork exactly as upstream: each is a stub that panics with `unimplemented`. Templates are how crier lays out a card, writes a caption and reads a data document, so the panic was not one feature missing but the program's core, and there was no build tag to pass and no shim to write, because the stubs are definitions, not declarations.

**Since 1.0.1 the templates need no reflection**, and a fork-built crier renders. crier executes its templates with [an executor of its own](#what-would-have-to-change) over the standard parse tree, so the panic is gone and with it the last thing crier itself could do. The spike's `R` row renders `examples/business-promo` to a PNG under the fork, and the black-box suite reports 106 tests passing against the fork build where 1.0.0 managed 47. Of the 30 that fail, 14 are the announcement's tests, which the spike's build context had left the `announce` folder out of (since widened), one is the version line, which named the fork's version where a Go version was expected (since labelled `tinygo 0.43.0-net.1`), and the remaining fourteen are one fact about the fork:

```
crier: page 1: listening on 127.0.0.1:0: dial:ListenConfig:Listen not implemented
```

**The fork's `net` dialled but did not listen.** Its host netdev had `Bind`, `Listen` and `Accept`, but `net.ListenConfig.Listen` above them was still the stub upstream ships, so nothing could serve. crier's stage server — the default way an image reaches Instagram's fetcher, through a tunnel — listens, and every test that staged through it failed under the fork at that line. Listening through the bare `net.Listen`, which the fork does implement, was the experiment that uncovered the second fact: on Linux `close` alone does not wake a thread blocked in `accept`, so a listener's `Close` left `net/http`'s serve loop hanging, and the first test to stop a stage server hung its process for the suite's ten-minute timeout.

crier itself keeps listening through a `ListenConfig`, as the standard library and its linters want. Two commits in the fork's `net` close both gaps, and neither is large: `ListenConfig.Listen` and `ListenPacket` delegating to the package-level functions that have served over the host netdev all along, and the host netdev shutting a socket down for both directions before it closes the descriptor, so a blocked `accept` or `recv` ends with the error a closed listener or connection owes it. With the two applied to a fork build for linux/arm64, the black-box suite passes whole: 142 tests, none failing. That includes the announcement's tests, which since 1.0.1 run the binary under test rather than a fresh gc build, and the upload tests, which post a six-megabyte clip and a twelve-megabyte picture to every platform and read the bytes back out of the fakes, and one post over real TLS that the fork verifies and, without the certificate, refuses. The cards the fork renders are pixel for pixel gc's. Until the fork ships a release carrying the two commits, the pinned 0.43.0-net.1 stops the spike's `e2e` stage at the first test that stages through the server, ten minutes in, which is the hang.

## Sizes

The size question is what made the rest worth proving, so it is measured the same way every time: the same source, both toolchains, both stamped. The gc column is the release pipeline's exact line (`-trimpath -s -w`), the TinyGo column the line a tiny build would use (`-opt=z -no-debug -tags noasm`). Measured natively on a Mac at the fork's 0.43.0-net.1 over go1.26.7, from the same checkout:

```
target                tinygo        gc            ratio
linux/amd64           29097120      32542882      0.894
linux/arm64           29025472      30408866      0.955
darwin/arm64          25518224      31274242      0.816
```

The spike's own `fork-sizes.log`, taken inside the container for the builder's architecture, agrees to within a few kilobytes (linux/arm64: 29020920 against 30408866, ratio 0.954).

After 1.0.1 dropped html/template, the same measurement on linux/arm64 reads 29010632 against 30212258, a ratio of 0.960: leaving reflection behind saved the gc binary more than it saved the TinyGo one.

crier is not dispat. Its bulk is layout, rasterising, font parsing and fourteen HTTP clients rather than a runtime, and TinyGo saves less on data than it does on code, so the ratio is nothing like the 60% dispat's page reports. A twentieth of the size is not the prize dispat's spike was chasing; what the pair is for is having a second compiler's answer to every test, which is a different kind of prize.

## The self-update matrix

`tinygo-spike-selfupdate` is the acceptance test the fork exists for: crier updating *itself*, through the one release path that touches every layer at once. Listing, download, digest check, a smoke execution of the new binary, and the atomic swap that keeps the old one as `.backup`. None of it needs a template, which is why this half of the spike can still say something about the fork.

The server it runs against is `sufake`, generated per run: a CA and a leaf valid for `localhost` and `127.0.0.1`, the same release listing shape `test/e2e`'s fixture serves with a release-candidate decoy above the real release, and a log line for every connection and every request carrying the negotiated TLS version, cipher and SNI. The client is pointed at the root with `SSL_CERT_FILE`, so the fork's own x509 root loading is under test too, and the decoy is what proves the fork-built binary skips prereleases the way the gc one does.

| Row | Setup | Expected |
|-----|-------|----------|
| zero | The fork's binary stamped `1.0.0` | Reports `crier 1.0.0`, not `dev` |
| R | The fork's binary renders `examples/business-promo` | A PNG — today, the panic above |
| A | gc control, CA trusted, full update | Exit 0, now `1.1.0` |
| B | Fork `--check`, CA trusted | Exit 1, an update pending, API paths only |
| B2 | The same by IP literal | Exit 1, SNI waived by the client |
| C | Fork full update, CA trusted | Exit 0, binary `1.1.0`, backup `1.0.0` |
| C2 | `--rollback`, nothing listening | Exit 0, binary `1.0.0` again |
| D | C's setup with the CA withheld | Nonzero, a certificate error |
| E | Plain HTTP at the TLS port | Nonzero, refused by the listener |
| F | The net stage's layers, re-asked with the fork | Recorded |

Rows D and E are the ones a stub cannot survive. D fails only if the client really verified a chain against real roots, which a no-op handshake never does; E is refused by the listener itself, and the connection log shows the request method's bytes where a ClientHello belongs.

At 0.43.0-net.1 on linux/arm64 the matrix holds, R aside. The fork build stamped `1.0.0` reports `crier 1.0.0`. B and B2 list the fake over TLS, by name and by IP literal, and report `crier 1.1.0 is out; you have 1.0.0` with exit 1. C downloads the 29 MB asset, verifies it, runs it, swaps it in and keeps the old binary as `crier.backup`, and C2 puts it back with nothing listening. D is refused with `x509: certificate signed by unknown authority`, and `sufake` records `first-bytes=160301 clienthello=true` for that connection: a real handshake, really verified, really rejected. E is refused by the listener with `Client sent an HTTP request to an HTTPS server`, and the wire shows `474554` — the bytes of `GET` — where a ClientHello belongs. F walks the four layers again with the fork and the assertion server reads a ClientHello from it. So the release path the fork was built for works for crier too; only the renderer does not.

On macOS the two toolchains need not agree about where roots come from: a darwin build from the Go compiler hands certificate verification to the platform verifier rather than reading `SSL_CERT_FILE`, so a generated CA can be invisible to a trusted row even though the file is perfectly good. The darwin script probes that with both a Go client and curl, records what each said, and never modifies the keychain. Today the darwin fork rows never get that far, for the reason above.

## The unit-test probe

`tinygo-spike-test` runs `tinygo test` with the upstream toolchain, one package at a time, under three bounds each of which answers a way the row used to report one fact instead of a matrix: `-tags safe`, because testify's go-spew reaches into `reflect.Value` through `unsafe` and panics at init under TinyGo's reflect; `-p 2`, because one test binary per core does not fit in memory; and an external `timeout` per package, because a hung `httptest.Server.Close` is not something `-timeout` can end. Read `test.log` as what upstream's reflect and net can run of crier's suites, not as a verdict on a shipped binary; the black-box suite is that.

What it says at 0.42.0, on the image's Go 1.27: `internal/configgen`, `internal/logging` and `internal/version` pass. `internal/httpx` and `internal/selfupdate` hang in `httptest` and are cut off at the bound. `cmd/crier`, `internal/app` and `internal/stage` stop at the `cookiejar` import that stops the upstream build. `internal/raster` and `internal/render` do not compile against that Go's `internal/runtime/maps`, the same `abi.MapType` mismatch the fork shows on 1.27. `internal/template` panics on `NumOut` in its first test, and `internal/config` fails two tests that locate their source through `runtime.Caller`, which under TinyGo reports nothing they can use.

## What would have to change

One fork release. The two `net` commits above are the whole of it; a fork release carrying them, pinned in [`scripts/install-tools.sh`](../../scripts/install-tools.sh), is what turns the tiny pair from a spike into a release asset. Everything else is written: the install manifest fetches the fork and lays cookiejar over it, the shim package links the assembly-bearing dependencies, the templates execute without reflection, the suite runs against a fork build and passes, and dispat's own root Dockerfile shows what the release stages look like — two more linux binaries under names of their own, additive to the six the [asset contract](./install.md#the-asset-names-are-a-contract) names, validated and put through the black-box suite before upload.

Re-asking the fork's question is bumping one version number in the manifest and running one command. Re-asking upstream's is bumping `TINYGO_VERSION` at the top of `Dockerfile.tinygo`.

## Reading the logs

The logs are the artefact. Each is a sequence of `=== what ===` headers followed by the step's output and its `exit=` status, so a step that failed says so where it happened rather than stopping the run. `fork.log` carries the toolchain version that was installed and the cookiejar listing; `fork-sizes.log` the fork's size row for the builder's own architecture; `selfupdate.log` the matrix above, with `sufake:` lines interleaved from the far end of the wire; `e2e.log` the suite's verdict with `e2e-verbose.log` beside it; `test.log` the unit-test probe, one `exit=` line per package.

The spike fetches the fork once (180 MB, a cached layer until the manifest changes). Set `GITHUB_TOKEN` to keep that fetch's one API call off the unauthenticated rate limit; the script forwards it as a build secret when it is set and passes nothing when it is not.
