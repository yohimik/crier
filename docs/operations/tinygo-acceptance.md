# TinyGo release acceptance

Status on September 6, 2026: **pending; no TinyGo Crier release has been published**.
The current stable Crier release is `v1.1.0`. The intended next patch is `v1.1.1`,
subject to every acceptance gate below. TinyGo `v0.43.0-net.2` was published
on September 6 at 16:49:57 UTC; its tagged release workflow `34042895556`
passed. Crier now pins `v0.43.0-net.2` without the obsolete cookiejar source
overlay. Published-toolchain acceptance is the current gate; see the saved
restart checkpoint for historical state, not current running-job instructions.

The user approved the explicit-rounding build patch on September 6. Fresh
patched candidate acceptance passed and Crier's pre-tag confirmation was
sent; the TinyGo owner independently verified the evidence. Published-fork
acceptance and Crier publication remain pending. Historical unpatched results
below are retained, not reclassified as passing.

## Published-toolchain release validation

The release tag resolves to version-only compiler commit
`95fba82a76c198a4543a031b29c14fc878dfc05f`. Published Linux archives are
downloaded under `coverage/tinygo-net2-published-toolchains/` and checked
against GitHub's published SHA-256 digests before unpacking:

- AMD64: `e94fcab3acad305fc2b7eb729578aa911af153a3750038cf54b4c11de6cffa6d`
- ARM64: `af96dc321b172c1418dffd877063cd830f01c685ae4dbc10e6846c8b5fd62c16`

For this manual release, one fixed source snapshot and timestamp identify all
eight assets. `go-export` exports the six standard artifacts only after the
ordinary Go test gate. The published-toolchain diagnostic runner creates the
two tiny artifacts with the same version, source and timestamp, retains raw
copies, and tests both raw and stripped executables. Exported candidates are
not published unless all verdicts pass. No rebuild or re-stamping of accepted
bytes is permitted between their final acceptance and upload.

The source snapshot is intended to be transferred using GitHub's Git-object
API with exact Git tree/commit hash verification, without a Git push or
updating main. Only after acceptance will its release tag and assets be
published. The snapshot's source archive must match the tested source tree;
the normal release workflow is not dispatched. The local development branch
and its complete history are retained separately.

## Reproducible renderer rounding

Both Docker release compilers use `scripts/prepare-renderer.sh` and
`patches/webrender-explicit-rounding.patch`. The script verifies webrender
`v0.0.14` and the original gradient source SHA-256, copies that module into
a new private build directory, applies exactly two explicit `pr.Fl`
conversions, and generates an alternate module file. The shared module cache,
root `go.mod` and TinyGo compiler are not modified. Dependency upgrades fail
closed until the patch is reviewed. Test subprocesses inherit the alternate
module file, including all stamped self-update fixtures.

The conversions enforce rounding of the two gradient-origin products before
subtraction. This removes a visible ARM64 fused-arithmetic difference without
compiler debug flags or changes to pixel tolerances. The tagged executable
`test/rounding` checks the two known origins with both compilers before full
diagnostic acceptance. Full pixel and integration gates remain mandatory.

To reproduce outside Docker on Linux, install Go and GNU `patch`/`sha256sum`,
then from the repository root (the output directory must not already exist):

```sh
sh scripts/prepare-renderer.sh /tmp/crier-renderer-build
export GOFLAGS=-modfile=/tmp/crier-renderer-build/crier.mod
export GOWORK=off
go run -tags=rounding ./test/rounding
go build -trimpath -o /tmp/crier-rounded ./cmd/crier
```

The root module intentionally has no local `replace`, preserving ordinary
`go install github.com/yohimik/crier/cmd/crier@version` compatibility. Direct
`go build`/`go install` without this preparation still uses upstream webrender
and can retain the Go-spec-permitted gradient variation. The reproducible
rounding contract applies to the packaged release binaries and prepared
source builds; it is not a claim about every unpatched source build.

### Patched candidate accepted

The fresh runs build commit `7d687fc8a17cb0350d95698be0f74f6e8ce4b4b4`
against the same frozen compiler artifacts identified below. Native ARM64
logs to `/tmp/crier-rounded-candidate-arm64.log` and exports to
`coverage/tinygo-rounded-candidate-arm64/`. Emulated AMD64 logs to
`/tmp/crier-rounded-candidate-amd64.log` and exports to
`coverage/tinygo-rounded-candidate-amd64/`. Both builds completed successfully
and both diagnostic `verdict.txt` files are `PASS`. No candidate build remains
running. Each target passed both raw and stripped full E2E: 144 top-level
tests, 156 passing test events, zero failures and zero skips per run. The
stamped replacements were built with TinyGo, version/compiler checked,
stripped and hash recorded. Trusted TLS by name/IP, certificate refusal,
plaintext refusal, offline rollback and real FFmpeg all passed.

Both Go and TinyGo passed the explicit-origin arithmetic regression. The
13-PNG comparison passes unchanged thresholds on both targets: AMD64 is
pixel-exact for all thirteen; ARM64 is exact for twelve, with only four event
card pixels differing by one channel value. Both game gradients and the
isolated gradient are now pixel-exact.

Both source-file manifests have SHA-256
`bf9a198eb807cff025087b53f0ebb3ce2e45e180627d9de430ab91572e73a112`.
CLI, test, module, example, announcement, patch and diagnostic-script inputs
were rechecked against the current checkout. The patch SHA-256 is
`4e5da0b90cf3d9c0cd7dc8c94681b5d8624113405f3e5a064f8eed923e7a6373`;
patched gradient source SHA-256 is
`e439fe4b9e4136889d23e7d12969cd02049f83bf718b6d31baf89de39fd6f33b`.
Artifact hashes were independently rechecked after export:

| Target / artifact | Bytes | SHA-256 |
| --- | ---: | --- |
| ARM64 raw TinyGo | 29,109,072 | `7b7a1d967334eea95ab66b8dd0fe7fda8f4313cb2918eccf2c4224868a3c8ccc` |
| ARM64 stripped TinyGo | 13,835,824 | `4f437a332eacb9b05538aa07f2c9e574655b9bc444fcf65d7b8fc6aa2aaf5a71` |
| ARM64 Go reference | 30,277,794 | `097bde852483a3ba27d61537b5606edc7d5b835d075dd98ce9b2cc8d97478320` |
| AMD64 raw TinyGo | 29,125,112 | `46d228d2c3fe851079a089500fc18e4c3db00beeb4416263bd2e23a4958e16a2` |
| AMD64 stripped TinyGo | 16,446,968 | `2e1c89981c9e0338f925b5fc2a509d7be3a96142ab8e795ec72f78e0be47184d` |
| AMD64 Go reference | 32,518,306 | `1387d434354692a90543e859fffaf33b6fc5f9c05b47dd43e37aa1dce309a20a` |

Crier's explicit pre-tag confirmation is now given for frozen candidate
`e7d34c8c126e0eecd2ce711915f2f88d112d833a` (net
`0f460803c832e5edad095ce02731f1000496dd88`). This is not approval to publish
Crier from candidate artifacts. The next gate is the verified published
TinyGo release, followed by fresh intended-release artifact acceptance.

The full patched Go gate passed in `/tmp/crier-rounded-go-tests.log`: all
unit tests, E2E (110.677 seconds), real FFmpeg, and all coverage floors;
combined statement coverage is 90.6%. Both arithmetic origins passed.
Formatting and vet passed in `/tmp/crier-rounded-{gofmt,vet}-final.log`.
The first lint attempt found a harness incompatibility: go/packages performs
a GOPATH-mode probe, which rejects a globally set `-modfile`. The lint-only
Docker stage now copies the same prepared manifests into its disposable
source tree and clears `GOFLAGS`; the host manifests remain unchanged.
Lint (zero issues), actionlint, generated docs and shell checks all passed
in `/tmp/crier-rounded-{golangci-lint,actionlint,docs,shellcheck}-r2.log`.
This subsequent lint-only adjustment does not change candidate compiler,
renderer, CLI, fixtures or diagnostic scripts.

## Ownership and order

Crier owns its renderer, templates, publishers, fixtures, harness and release.
The TinyGo task owns compiler/net changes and its release. Dispat owns its
Git, webhook, install and shell-stage workflows. Generic DNS, socket, TLS,
spawn, signal and descriptor probes belong to TinyGo; Crier does not copy
another generic network suite. Each application still tests its own TLS
self-update lifecycle against its actual artifacts.

The fork owner requires fresh Crier and Dispat candidate acceptance before
publishing the fork. Crier then repeats acceptance with the verified published
toolchain before publishing its patch. A printed version string from a CI
candidate is not proof that the candidate is the published release.

## Artifacts

The six standard Go asset names remain unchanged. The proposed additional
assets are `crier-tiny-linux-amd64` and `crier-tiny-linux-arm64`, statically
linked, stripped ELF executables. Raw compiler output is retained for the
comparison but is not the intended upload. `scripts/build-tiny.sh` records
compiler versions, flags, raw/stripped byte counts and SHA-256 hashes.

The installer and self-update still resolve `crier-{os}-{arch}`. Choosing a tiny
asset is explicit; a normal self-update changes it to the standard Go flavor.
The test harness additionally exercises TinyGo-to-TinyGo updates to prove the
new compiler's process and rollback behavior without silently switching to Go.
This is test coverage, not a change to the public self-update asset contract.

## Gates

- Standard Go: formatting, vet, lint, workflow/shell checks, generated docs,
  all unit tests, all E2E tests, real FFmpeg and per-package coverage floors.
- Candidate and published toolchains: full current E2E against both raw and
  stripped CLI artifacts, with no hidden test skips on Linux. This includes
  multi-megabyte platform uploads, chunk reassembly, staged rendering and
  listener shutdown, real FFmpeg streams, and helper/tunnel processes.
- `CRIER_E2E_COMPILER=tinygo` explicitly builds the versioned update fixtures
  with TinyGo and strips them. The harness refuses a prebuilt TinyGo CLI when
  this setting is missing; it validates fixture compiler/version identity and
  records fixture hashes. The test runner itself remains standard Go.
- Self-update over trusted TLS by hostname/IP, unknown-CA refusal, plaintext
  rejection by a TLS listener, and offline rollback with byte-identical
  restoration. No keychain or system root-store changes.
- Same-source, same-target Go/TinyGo rendering: example cards, fonts, text,
  gradients, story variants and both paginated announcement layouts. The pixel
  gate uses the existing raster tolerance (maximum channel delta 8; no more
  than 0.1% of pixels differing by more than 2), never a relaxed fork-specific
  threshold. Both sets of PNGs are retained when comparison fails.
- Every intended published target must have execution evidence. A native
  ARM64 pass is not an AMD64 pass. Any emulation, timeout, skipped platform or
  failed fixture stays explicit in the report.

## Diagnostic runner

`Dockerfile.tinygo-acceptance` accepts a verified, unpacked compiler directory
as the named build context `toolchain`. It copies the directory without source
overlays and runs unprivileged. Supply `SOURCE_COMMIT` and `TOOLCHAIN_ID` with
the exact candidate/artifact identities. It exports a source-file hash manifest,
compiler identity, build logs, raw/stripped binaries, hashes, E2E JSON/counts
and PNG evidence. Its export is deliberately diagnostic: **read `verdict.txt`;
a successful Docker export is not a passing acceptance result**.

The root Dockerfile's `release-test` gate is different: it fails closed on an
E2E failure, skip or pixel mismatch. `export` depends on that gate. The
ordinary `test` target remains the standard Go coverage gate. No release or
pipeline is triggered by either diagnostic command.

## Open limitations

The earlier gradient discrepancy is resolved by the verified explicit-rounding
build patch above, without changing pixel tolerance. The fork owner reports
incomplete in-flight deadline/cancellation
and descriptor-lifetime behavior. Application test passes do not establish
general Go networking or server compatibility. Historical size reductions and
old test counts in the spike page are not current release measurements.

## Historical unpatched candidate results: ARM64

This earlier September 6 native Linux ARM64 run was **not accepted**. Both full E2E
runs passed: raw and stripped each report 144 top-level tests, 156 passing
test events including subtests, zero failures and zero skips. The replacement
fixtures were built by TinyGo, stripped, version-checked and hash-recorded.
The pixel gate failed; E2E success does not override it.

Toolchain provenance: frozen TinyGo `e7d34c8c126e0eecd2ce711915f2f88d112d833a`,
Actions artifact `9962890989`, run `33944102078`, archive SHA-256
`f81dc83c6660295824dee9f918c5234fcc5fb4af61997e45d66f27d1dfcc5211`.
It prints `0.43.0-net.1` but is an unpublished candidate, not that release.
It uses Go 1.26.8 and LLVM 22.1.4. No compiler source overlays were applied.

The production Go sources, module manifests, examples and announcement inputs
were checked against the exported source-file hash manifest. They match the
`b610462` production tree. Harness `1cc67eb` adds acceptance infrastructure;
`153026c` adds the isolated gradient fixture and changed-pixel coordinates.
These local harness commits do not change production rendering code.

| Artifact | Bytes | SHA-256 |
| --- | ---: | --- |
| Raw TinyGo ARM64 | 29,109,072 | `36201cc30565e35f094cee25ca4a55c09b1c904c01a46117ab171725408b4a73` |
| Stripped TinyGo ARM64 | 13,835,824 | `747742288a5151ae29fe20c0aad082458f5f46e07f49330ee66f110aa835eebe` |
| Corrected Go ARM64 reference | 30,277,794 | `b7482890a0e3154e2ee78ccabee0907fa66f3e402902bcf2c6fdac2a0ba413a0` |

The stripped candidate is 54.30% smaller than this Go reference. These are
candidate measurements, not final release sizes. TinyGo used `-opt=z
-no-debug -tags=noasm -interp-timeout=15m -p=2`, then `strip --strip-all`.
Go used `CGO_ENABLED=0`, `-trimpath`, `-s -w` and `-buildvcs=false` for the
read-only mounted checkout. Both carry explicit version `1.1.1` and commit
label `b610462d008d-with-acceptance-harness`; the file manifest, not that
diagnostic label, identifies the complete test input. The first comparison
used default CGO; it was superseded by this corrected reference.

The corrected 12-PNG matrix has nine exact matches, one within tolerance
(`event-invite`: six pixels, maximum delta 1), and two failures:
`video-game-release` (seven pixels) and `video-game-story` (four pixels), both
maximum channel delta 38. Adding the isolated gradient makes 13 images; that
extra fixture fails at the exact same seven coordinates as the wide game card.
All 13 raw-versus-stripped TinyGo PNG pairs are byte-identical and pixel-identical.
All 13 repeated corrected-Go PNG pairs are also byte-identical and pixel-identical.
Thus neither stripping nor observed run-to-run nondeterminism explains this
failure. The root cause was not yet established at this stage; the later
FMA diagnosis and accepted patch are documented separately.

The isolated fixture contains only two CSS background gradients: no text,
fonts or external assets. Run each binary with its own output path from the
repository root:

```sh
printf '{}\n' | "$CRIER_PIXEL_BINARY" render \
  --config examples/video-game-release/crier.yaml \
  --render-pool "$PWD/test/pixels/testdata/repeating-gradient.html" \
  --render-data - --render-seed 12345 --render-format png \
  --render-output gradient.png
```

The changed `[x,y,delta]` coordinates are `[602,68,38]`, `[1861,75,38]`,
`[429,439,36]`, `[1688,446,35]`, `[1873,726,35]`, `[256,810,36]`,
`[1515,817,36]`. The automated reproduction is the `gradient-only` subtest in
`test/pixels`, using the environment variables described there.

Local evidence is under `coverage/tinygo-candidate-20260906-r2/` (E2E,
provenance and binaries), `coverage/tinygo-pixel-corrected-arm64/` (corrected
Go reference/build metadata) and `coverage/tinygo-pixel-isolation-arm64/`
(corrected comparison, raw/stripped equality and Go repeatability JSON/PNGs).
The TinyGo owner received the failure and minimal reproduction. No candidate
confirmation was given on this unpatched evidence; the later patched
confirmation is recorded above.

## Historical unpatched candidate results: AMD64

The AMD64 run used the matching frozen compiler artifact `9962835595`, run
`33944102078`, archive SHA-256
`1e921a14fb6447bc853976cddd563abfb4cc8169f89c13775f8bc6656b6b4f12`.
It ran under Docker Desktop AMD64 emulation on the ARM64 host, not native
AMD64 hardware. Both raw and stripped full E2E runs passed: 144 top-level
tests, 156 passing test events, zero failures and zero skips for each.
The corrected `CGO_ENABLED=0` reference comparison passes all thirteen PNGs
pixel-exact, including the minimal gradient. All thirteen raw/stripped pairs
are byte-identical, as are all thirteen repeated ordinary-Go render pairs.
This supersedes the initial default-CGO comparison.

The raw TinyGo AMD64 binary is 29,125,104 bytes, SHA-256
`6eb678c5fe5b8d0c9b28c533deb0feba296d8decad2fc743ba41f36efc93440c`.
The stripped copy is 16,446,960 bytes, SHA-256
`18c2bcd3c5df7603dbdf7332c2df686b29ee7569c3e701f0e41c2bfd87bbbe3f`.
The corrected Go reference is 32,518,306 bytes, SHA-256
`6523bc51e3085feb8d3cd82d8e10ffe342e28b0078c43fe221be62273993daed`.
Evidence is in `coverage/tinygo-candidate-20260906-amd64/`; corrected reference
work is in `coverage/tinygo-pixel-corrected-amd64/`. This evidence alone did
not resolve the then-open ARM64 failure or authorize either release.

Cross-architecture control: all thirteen TinyGo ARM64 PNGs are byte-identical
to the TinyGo AMD64 PNGs. Ordinary Go ARM64 and ordinary Go AMD64 differ on
the same four fixtures (two game variants, the minimal gradient and the small
event-card difference). This motivated investigation of architecture-specific
arithmetic; it did not establish a compiler defect. Standard-Go FMA-disabled
builds were used solely to isolate the difference, not to
replace the release reference or bypass the unchanged pixel threshold.

### Historical FMA isolation (diagnostic only)

The following native Go 1.26.8 ARM64 builds keep the same production source
and use `CGO_ENABLED=0`, `GOFLAGS=-buildvcs=false`, `-trimpath`, stripped Go
linker flags and version `1.1.1`. Each adds `-gcflags='SCOPE=-d=fmahash=n'`.
Compiler logs contain `[DISABLED]` and `fmahash triggered` records. No such
flag has been added to the production build.

| Scope with FMA disabled | Comparison with unchanged TinyGo ARM64 |
| --- | --- |
| `all` | All 13 PNGs pixel-exact |
| `github.com/benoitkugler/webrender/...` | All 13 PNGs pixel-exact |
| `github.com/benoitkugler/webrender/images` | Gate passes: 12 exact; event card has four pixels at delta 1 |
| `github.com/yohimik/crier/internal/raster` | Original hard-stop failures remain |
| `github.com/benoitkugler/webrender/matrix` | Original hard-stop failures remain |
| `github.com/benoitkugler/webrender/svg` | Fails: two pixels at delta 36 in wide/isolated gradients; story remains four at delta 38 |

This isolates the discrepancy to contraction during webrender computation;
cross-package inlining must be considered when interpreting package controls.
The [Go floating-point specification](https://go.dev/ref/spec#Floating_point_operators)
permits fused operations with different intermediate rounding and explains
how explicit floating-point conversions enforce rounding boundaries. Thus
this is not, on its own, evidence of a TinyGo compiler defect. The TinyGo task
then checked isolated gradient-layout operands and bits. The release gate
remained unchanged throughout that analysis and the subsequent build fix.
Logs and diagnostic binaries are in `coverage/tinygo-fma-isolation-arm64/`.

### Historical explicit-rounding dependency control

The independent compiler-task operand probe confirms permitted FMA, not a
TinyGo miscompile. For one actual gradient coordinate, ordinary ARM64 Go
produces bits `c20d7b3c`, while TinyGo and an explicitly rounded Go product
produce `c20d7b40`. The unrounded source has no required intermediate rounding
boundary, and ARM64 Go emits `FMSUBS` for it.

A private copy of webrender was tested with only these two substitutions:

```go
startX := (pr.Fl(width) - pr.Fl(dx*vectorLength)) / 2
startY := (pr.Fl(height) - pr.Fl(dy*vectorLength)) / 2
```

Ordinary Go ARM64 with this dependency copy passes the existing pixel gate
against the unchanged TinyGo artifact: twelve of thirteen PNGs are exact;
the event card has four pixels differing by 1. No compiler debug flag or
threshold change is involved. Control binary SHA-256 is
`fb65731f7756167f02fc761d362e47e36e6c274b6e6dc4bdc51ad725844dd750`.
Evidence is `coverage/tinygo-rounding-control-arm64/pixels.json`; the private
dependency and alternate module file are in `coverage/tinygo-rounding-overlay/`.
At this control stage, the module cache and production dependency were
unchanged; full patched acceptance had not yet been performed.

The user subsequently selected the explicit-rounding dependency patch to
preserve the cross-compiler pixel contract. See the build policy above.
Existing failures have not been relabelled. Fresh patched acceptance and
pre-tag confirmation are recorded above; Crier publication remains pending.
