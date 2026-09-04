# syntax=docker/dockerfile:1
#
# crier's build-and-test image: the six release binaries, built from this
# checkout with the release version baked in, validated, tested, and exported
# to the host as a plain folder.
#
# The export descends from the test stage, so a failed check fails this build
# and nothing comes out: what lands in dist/ is six binaries that were tested,
# never six that merely compiled.
#
# One binary per mainstream platform. The names — crier-{goos}-{goarch}[.exe] —
# are a contract: install.sh, install.ps1 and a bare `dispat install` all
# resolve exactly these, so they must not change.
#
# Everything runs here rather than on the runner, so a CI job needs Docker and
# dispat and no Go toolchain of its own. Cross-compilation is Go's own from one
# native builder — no --platform fan-out and no emulation, which is why six
# targets cost roughly what one does.
#
# Every binary is gc's. Whether a TinyGo fork could build smaller ones is the
# question Dockerfile.tinygo asks; docs/operations/tinygo.md has the answer,
# which as of the fork's 0.43.0-net.1 is that a fork-built crier links but
# cannot render, so nothing here is built with it.
ARG GO_VERSION=1.26

# --- dependencies -------------------------------------------------------------
#
# Keyed on the manifests alone, so the module cache survives every source edit
# and a CI run downloads nothing it downloaded last time.
FROM golang:${GO_VERSION}-alpine AS deps
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

FROM deps AS source
COPY . .

# --- lint ---------------------------------------------------------------------
#
# Three gates, three targets, so a failure names which one. Each is invoked
# from dispat.yaml with --output type=cacheonly: the log is the whole product.

FROM source AS gofmt
RUN set -eu; \
    unformatted="$(gofmt -l .)"; \
    if [ -n "$unformatted" ]; then \
      echo "these files are not gofmt'd:" >&2; echo "$unformatted" >&2; exit 1; \
    fi; \
    echo "gofmt: clean"

FROM source AS vet
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go vet ./... && go vet -tags e2e ./test/... && echo "vet: clean"

FROM source AS golangci-lint
ARG GOLANGCI_LINT_VERSION=v2.6.1
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/.cache/golangci-lint \
    --mount=type=cache,target=/go/pkg/mod \
    golangci-lint run ./...

# --- workflows ----------------------------------------------------------------
#
# The workflows are as much a part of the release as the code: the install
# matrix is what proves a release actually installs, and a typo in an
# expression there fails at 3am on a tag rather than here. actionlint also
# knows shellcheck, so the `run:` blocks are checked as shell too.
FROM source AS actionlint
ARG ACTIONLINT_VERSION=v1.7.7
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go install github.com/rhysd/actionlint/cmd/actionlint@${ACTIONLINT_VERSION}
RUN apk add --no-cache shellcheck >/dev/null && actionlint -color && echo "actionlint: clean"

# --- shell --------------------------------------------------------------------
#
# The scripts are the install story and the release announcement, which is to
# say they are the parts most likely to be run by somebody who cannot debug
# them.
#
# The darwin half of the TinyGo spike is checked apart from the rest: it is the
# one script that runs without `set -e` on purpose (its probes never abort, the
# logs are what it produces), so shellcheck cannot see that a failing `cd`
# there is followed by commands which record their own failure. Its `cd` calls
# fail through `die` like anything fatal there; only the grouped-redirect style
# note (SC2129) is left alone, because one log line per probe is the shape its
# logs are read in.
FROM source AS shellcheck
RUN apk add --no-cache shellcheck >/dev/null && \
    shellcheck install.sh announce/announce.sh announce/notes.sh cmd/crier/build.sh \
      scripts/install-tools.sh && \
    shellcheck --exclude=SC2129 scripts/tinygo-spike-darwin.sh && \
    echo "shellcheck: clean"

# --- documentation ------------------------------------------------------------
#
# The reference pages under docs/configuration/ and crier.example.yaml are
# generated from the configuration registry. A key added without documentation
# should fail here rather than surprise somebody later, so both are regenerated
# and compared. The whole directory is compared rather than the generated pages
# alone, which also catches a page left behind by a group that was renamed.
FROM source AS docs
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    set -eu; \
    mkdir -p /before; \
    cp -r docs/configuration /before/configuration; \
    cp crier.example.yaml /before/crier.example.yaml; \
    go run ./tools/gendocs; \
    diff -ru /before/configuration docs/configuration || { \
      echo "docs/configuration/ is stale; run: go run ./tools/gendocs" >&2; \
      exit 1; \
    }; \
    diff -u /before/crier.example.yaml crier.example.yaml || { \
      echo "crier.example.yaml is stale; run: go run ./tools/gendocs" >&2; \
      exit 1; \
    }; \
    echo "docs: in step with the registry"

# --- build --------------------------------------------------------------------
#
# The asset list is accumulated in the loop rather than written out a second
# time, so the export and the loop can never disagree about which binaries
# exist. It leaves this stage as bare names in /out/dispat-output; the staged
# stage below turns them into the paths the caller will see.
FROM source AS build

# The version the ldflags bake in. `dev` is what an un-stamped build reports,
# so it is the honest default for a build run outside a release.
ARG DISPAT_VERSION=dev

# The commit the ldflags bake in. build.sh passes it, because build.sh runs
# where git does. Left empty, the resolution below reads it out of .git, which
# is why .dockerignore keeps that directory.
ARG DISPAT_COMMIT=
ARG TARGETARCH

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    set -eu; \
    mkdir -p /out; \
    commit="${DISPAT_COMMIT}"; \
    if [ -z "$commit" ] && [ -f .git/HEAD ]; then \
      head="$(cat .git/HEAD)"; \
      case "$head" in \
        "ref: "*) \
          ref="${head#ref: }"; \
          if [ -f ".git/$ref" ]; then \
            commit="$(cat ".git/$ref")"; \
          else \
            commit="$(grep -F " $ref" .git/packed-refs 2>/dev/null | cut -d' ' -f1 || true)"; \
          fi ;; \
        *) commit="$head" ;; \
      esac; \
    fi; \
    commit="$(printf '%s' "${commit:-none}" | cut -c1-12)"; \
    date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"; \
    assets=""; \
    for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do \
      GOOS="${target%/*}"; \
      GOARCH="${target#*/}"; \
      case "$GOOS" in windows) ext=".exe" ;; *) ext="" ;; esac; \
      name="crier-${GOOS}-${GOARCH}${ext}"; \
      GOOS="$GOOS" GOARCH="$GOARCH" CGO_ENABLED=0 go build -trimpath \
        -ldflags "-s -w \
          -X github.com/yohimik/crier/internal/version.Version=${DISPAT_VERSION} \
          -X github.com/yohimik/crier/internal/version.Commit=${commit} \
          -X github.com/yohimik/crier/internal/version.Date=${date}" \
        -o "/out/$name" ./cmd/crier; \
      echo "built $name (version ${DISPAT_VERSION}, commit ${commit})"; \
      assets="${assets}${assets:+ }$name"; \
    done; \
    echo "DISPAT_EXPORT_GITHUB=$assets" > /out/dispat-output; \
    printf '%s' "$commit" > /commit

# Every binary is read back to prove it is a Go executable for the platform its
# name claims, and the linux ones are run: a binary that merely compiled is not
# what a release should carry. darwin and windows cannot run here at all —
# proving those falls to the release's post-release install matrix.
RUN set -eu; \
    for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do \
      GOOS="${target%/*}"; \
      GOARCH="${target#*/}"; \
      case "$GOOS" in windows) ext=".exe" ;; *) ext="" ;; esac; \
      name="crier-${GOOS}-${GOARCH}${ext}"; \
      info="$(go version -m "/out/$name")"; \
      echo "$info" | grep -q "GOOS=$GOOS" || { echo "$name is not a $GOOS binary" >&2; exit 1; }; \
      echo "$info" | grep -q "GOARCH=$GOARCH" || { echo "$name is not a $GOARCH binary" >&2; exit 1; }; \
      echo "validated $name"; \
    done; \
    out="$(/out/crier-linux-${TARGETARCH} --version)"; \
    echo "$out" | grep -q "linux/${TARGETARCH}" || \
      { echo "the native binary reports the wrong platform: $out" >&2; exit 1; }; \
    if [ "${DISPAT_VERSION}" != "dev" ]; then \
      echo "$out" | grep -q "${DISPAT_VERSION}" || \
        { echo "the native binary reports the wrong version: $out" >&2; exit 1; }; \
    fi; \
    commit="$(cat /commit)"; \
    if [ "$commit" != "none" ]; then \
      echo "$out" | grep -q "$commit" || \
        { echo "the native binary reports the wrong commit: $out (built $commit)" >&2; exit 1; }; \
    fi; \
    echo "executed crier-linux-${TARGETARCH}: $out"

# The release binaries are smoke-tested black box, against the exact bytes that
# will be uploaded.
#
# The validation above proves each binary is what its name says and that the
# native one starts. This proves it works: the end-to-end suite's smoke subset
# drives /out/crier-linux-<arch> as a user would — a render, the configuration
# precedence across all three layers, the nine-platform fan-out against fake
# servers, and the version stamp — with CRIER_E2E_BINARY naming the artefact so
# nothing is rebuilt. A binary that merely compiled never comes out of here.
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    set -eu; \
    CRIER_E2E_BINARY="/out/crier-linux-${TARGETARCH}" \
      go test -tags e2e ./test/e2e -run '^TestSmoke' -count=1 -v; \
    echo "smoke: the release binary passes"

# --- test ---------------------------------------------------------------------
#
# The gate the export descends from. ffmpeg is installed so the video path is
# exercised against a real encoder rather than only against the fake one the
# unit tests spawn.
FROM build AS test
RUN apk add --no-cache ffmpeg git && ffmpeg -version | head -n 1

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    set -eu; \
    mkdir -p /coverage/unit /coverage/e2e; \
    go test ./... -count=1 -covermode=atomic \
      -args -test.gocoverdir=/coverage/unit; \
    GOCOVERDIR=/coverage/e2e go test -tags e2e ./test/e2e -count=1; \
    go test -tags ffmpeg ./internal/render -run TestRealFFmpeg -count=1; \
    go tool covdata textfmt -i=/coverage/unit,/coverage/e2e -o /coverage/profile.txt; \
    go tool cover -func=/coverage/profile.txt | tail -n 1; \
    sh scripts/coverage-gate.sh /coverage/profile.txt

# The coverage profile on its own, for a caller that wants the numbers rather
# than the binaries.
FROM scratch AS coverage-export
COPY --from=test /coverage /

# --- export -------------------------------------------------------------------
#
# The stage's outputs, GITHUB_OUTPUT-style, as the caller will read them: the
# build knows the binaries' names and nothing about where the export will land,
# so the destination is passed in and joined onto them here.
#
# A stage of its own, and the only place DISPAT_EXPORT_BASE is declared, so
# building from a different checkout path does not invalidate the layers above.
FROM test AS staged
ARG DISPAT_EXPORT_BASE=.
RUN set -eu; \
    while IFS= read -r line; do \
      paths=""; \
      for name in ${line#*=}; do paths="$paths${paths:+ }$DISPAT_EXPORT_BASE/$name"; done; \
      printf '%s=%s\n' "${line%%=*}" "$paths"; \
    done < /out/dispat-output > /out/dispat-output.joined; \
    mv /out/dispat-output.joined /out/dispat-output; \
    cat /out/dispat-output

# Nothing but the artefacts: `--output type=local` writes this stage's whole
# filesystem to the destination folder, so anything else here would land in
# dist/ beside the binaries.
FROM scratch AS export
COPY --from=staged /out /
