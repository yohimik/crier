# Development

There are two ways to run crier's gates, and they run the same commands.

## In Docker, the way CI does

Every gate is a target of the root `Dockerfile`, driven by
[dispat](https://dispat.dev) so a runner needs Docker, git and dispat and no Go
toolchain of its own:

```sh
dispat run gofmt          # formatting
dispat run vet            # go vet, including the e2e build tag
dispat run lint           # golangci-lint
dispat run docs           # the generated reference and crier.example.yaml are current
dispat run tests          # both suites, the real-ffmpeg smoke, the coverage floor
dispat run cross-compile  # all six targets, each validated
dispat run coverage       # the same suites, with the merged profile in coverage/
```

Or without dispat:

```sh
docker buildx build --target test --output type=cacheonly .
```

## On the host, for the inner loop

```sh
go test ./...                          # the unit suite
go test -tags e2e ./test/e2e           # the black-box suite; builds the real binary
go test -tags ffmpeg ./internal/render -run TestRealFFmpeg   # needs ffmpeg
go run ./cmd/crier render --config examples/business-promo/crier.yaml
```

Needs Go 1.26 or newer.

## The test suites

**Unit tests** live beside their package. `internal/render` also carries golden
images in `testdata/golden/`, compared with a tolerance rather than byte for
byte — the rasterizer works in float32, and float32 does not associate the same
way on every architecture.

```sh
CRIER_UPDATE_GOLDEN=1 go test ./internal/render   # after an intended change
```

Look at the regenerated images before committing them: the point of a golden is
that somebody saw it.

**The black-box suite** in `test/e2e` builds the real binary with coverage
instrumentation and drives it as a subprocess against fake platform servers, a
fake S3, a fake tunnel and a fake ffmpeg. It asserts the things that only exist
at the edges: exit codes, what lands on standard output, and whether the config
a project directory carries is the one that gets used.

It can also be pointed at a binary that already exists, which is how the
release build tests the artefact it is about to upload rather than a fresh
build of the same source:

```sh
CRIER_E2E_BINARY=/path/to/crier go test -tags e2e ./test/e2e -run '^TestSmoke'
```

In that mode nothing is built and no coverage is collected — a released binary
is not instrumented. `^TestSmoke` is the subset the release runs: a render, the
configuration precedence, the nine-platform fan-out, and the version stamp. See
[releasing](./release.md).

## Coverage

The unit and black-box profiles are merged, because the black-box run exercises
code no unit test reaches. `scripts/coverage-gate.sh` holds a floor per package
rather than one number for the repository: a big well-covered package would
otherwise hide a small uncovered one.

```sh
dispat run coverage
go tool cover -html=coverage/profile.txt
```

## The generated files

`docs/configuration/reference/` and `crier.example.yaml` come out of the
configuration registry:

```sh
go run ./tools/gendocs
```

Adding a key to `internal/config/registry.go` and forgetting this is a failing
gate rather than a later surprise. There are anti-drift tests as well: the
registry, the field bindings and the flag set are compared as sets, so a key
that is settable in two layers and not the third cannot exist.

## Commits

Conventional, and scoped:

```
feat(crier): add the reddit publisher
fix(crier): stop transforming the glyph cache in place
```

The scope is what attributes a change under `internal/` to the package whose
path is `cmd/crier`. See [releasing](./release.md).
