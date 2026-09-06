# Crier TinyGo restart checkpoint

> Archived checkpoint, not current execution instructions. Crier v1.1.1 is
> published and its follow-up CI validation completed successfully. See
> [current acceptance](./tinygo-acceptance.md). Do not resume the old monitor,
> repeat publication, or treat the historical pending items below as active.

Saved September 6, 2026 for the requested app update/restart. No Crier build
or test process is running. All implementation and acceptance documentation
are committed locally on `codex/crier-tiny-acceptance`. Nothing was pushed,
no Crier pipeline was triggered, and no Crier release was created.

## Resume first

Read this file, `tinygo-acceptance.md`, and new task messages. Check current
state before starting any work. Resume the existing paused heartbeat
`crier-tinygo-acceptance-and-patch` when the user resumes after the restart;
do not create another automation or duplicate candidate builds.

Crier task: `01a06bfb-3443-7362-ab4c-18159087f4a1`.
TinyGo owner: `01a06bfb-347e-7df0-8a6c-7a77e849be3a`.
Dispat owner: `01a06e7b-e8ba-7722-ad9c-c6da024126a8`.
Own only `/Users/yohimik/Projects/crier`; do not edit TinyGo/net, Dispat,
or `/Users/yohimik/Projects/crier-size-comparison`.

## Completed acceptance — do not repeat the candidate phase

User approved the two-line explicit-rounding patch. Both release compilers
use `scripts/prepare-renderer.sh` with a private webrender copy and alternate
modfile. Root `go.mod`, shared dependency cache and compiler sources remain
unchanged. Direct unprepared source builds retain upstream rounding behavior.
Pixel thresholds are unchanged.

Candidate input commit: `7d687fc8a17cb0350d95698be0f74f6e8ce4b4b4`.
Later commits only fix the disposable lint-stage manifest handling and docs.
Both candidate source manifests have SHA-256
`bf9a198eb807cff025087b53f0ebb3ce2e45e180627d9de430ab91572e73a112`.

- Full Go unit/E2E/real-FFmpeg/coverage gate passed; coverage 90.6%.
- Formatting, vet, lint, actionlint, shellcheck and generated docs passed.
- Native Linux ARM64 and emulated Linux AMD64 candidate runs passed.
- Raw and stripped each passed 144 top-level tests / 156 passing events,
  with zero failures/skips, including TinyGo-stamped TLS updates and rollback.
- Both compilers passed the explicit-origin regression on both targets.
- All 13 PNGs pass: AMD64 exact; ARM64 12 exact, event card four delta-1 pixels.
- Artifact hashes and current production/harness input hashes were rechecked.
- Explicit pre-tag confirmation was sent. TinyGo independently verified it;
  Dispat's candidate confirmation is also complete.

Evidence directories (persisted, ignored by Git):

- `coverage/tinygo-rounded-candidate-arm64/`
- `coverage/tinygo-rounded-candidate-amd64/`

Logs: `/tmp/crier-rounded-candidate-{arm64,amd64}.log`,
`/tmp/crier-rounded-go-tests.log`,
`/tmp/crier-rounded-{gofmt,vet}-final.log`, and
`/tmp/crier-rounded-{golangci-lint,actionlint,docs,shellcheck}-r2.log`.
Old build sessions 81174 and 29765 are completed, not resumable builds.
The complete report retains historical failed runs separately.

## External state at restart

TinyGo version-only release commit is
`95fba82a76c198a4543a031b29c14fc878dfc05f`, child of accepted frozen compiler
`e7d34c8c126e0eecd2ce711915f2f88d112d833a`. Net pin remains
`0f460803c832e5edad095ce02731f1000496dd88`.
Branch CI and tagged release workflow `34042895556` passed.

Read-only GitHub verification during restart preparation found
`v0.43.0-net.2` published (not a draft) at `2026-09-06T16:49:57Z`:
https://github.com/yohimik/tinygo/releases/tag/v0.43.0-net.2

Before that, the TinyGo task reported an app safety-system error. Crier did
not retry or route around the blocked operation and did not publish the fork.
Check the owner's latest state and downloaded-artifact verification handoff;
do not treat publication alone as completed artifact verification. The latest
owner snapshot subsequently reports recovery: net.2 is published, three
toolchains are downloaded and hash-verified, no owned commands are running,
and its restart checkpoint is saved. Published-toolchain runtime probes and
the final downstream handoff remain its next steps. Crier has not yet received
the toolchain paths or independently checked those downloads.

The draft asset listing previously reported Linux archive digests below.
Re-fetch published metadata and independently verify downloads before use:

- AMD64: `e94fcab3acad305fc2b7eb729578aa911af153a3750038cf54b4c11de6cffa6d`
- ARM64: `af96dc321b172c1418dffd877063cd830f01c685ae4dbc10e6846c8b5fd62c16`

No published-toolchain acceptance has started in Crier. Do not mistake the
frozen candidate paths or their printed `net.1` version for published net.2.

## Remaining work

1. Obtain the owner's verified published toolchain identities/paths; verify
   published metadata, archives and version/provenance independently.
2. Update Crier's toolchain pin to net.2 and remove the obsolete cookiejar
   overlay in `scripts/install-tools.sh`. Do not mutate frozen compiler trees.
3. Build the intended release source with published toolchains. Repeat all
   applicable Go gates and exact-artifact raw/stripped TinyGo integrations,
   stamped update/TLS/rollback, real FFmpeg, arithmetic and pixel matrices on
   both Linux targets. Label AMD64 emulation explicitly. Diagnostic Docker
   export success is not acceptance: inspect verdicts, JSON, failures/skips.
4. Only after all gates pass, publish Crier patch `v1.1.1` with six standard
   assets plus stripped `crier-tiny-linux-{amd64,arm64}`. Keep installer and
   self-update standard asset names unchanged. Honor no Git push and no Crier
   pipeline trigger. The exact no-push source/tag publication mechanism is
   still to be implemented; ensure the release source matches tested bytes,
   and do not attach new binaries to an unrelated old source commit.
5. Verify uploaded release assets/digests and notify Dispat with the actual
   published release readiness and public report link. It keeps Crier v1.1.0
   pinned until that handoff. Pause follow-up on completion or required input.

Do not duplicate generic networking probes owned by TinyGo/Dispat. Do not
relax tests or silently accept skipped platform execution. Keep unchanged
monitoring quiet. No material external mutations are needed merely to resume.

The unrelated persistent Docker fixtures `failproxy` and `verdaccio` were
left running and must not be stopped as part of Crier restart preparation.
