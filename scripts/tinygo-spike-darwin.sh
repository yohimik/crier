#!/bin/sh
# The darwin half of the TinyGo spike. Buildx only reaches linux, so the
# darwin binaries Dockerfile.tinygo builds have never been *run*; this script
# mirrors its build, run and net stages natively on a Mac, for darwin/arm64
# and — when Rosetta answers — darwin/amd64. Same philosophy as the
# Dockerfile: no step aborts the spike, every step records its own exit
# status, and the logs are the artefact, written beside the container's under
# coverage/tinygo-spike/ as darwin-{build,sizes,run,net,selfupdate,e2e}.log.
#
# The probe and assertion programs are the Dockerfile's own heredocs,
# extracted from it at run time rather than copied here, so the two halves of
# the spike can never drift apart and the instruments stay what they are
# there: the spike's, not the repository's code.
#
# The self-update half of the matrix runs here too, against the same sufake
# server, with one darwin difference the script probes rather than assumes:
# Go's crypto/x509 consults SSL_CERT_FILE on unix and hands verification to
# the platform verifier on darwin, so the CA a run generates may or may not be
# trusted by an unmodified system. The script asks that question first,
# records the answer, and reads the trusted rows against it. It never touches
# the keychain: the rows that matter to the fork's TLS — a withheld CA and a
# plaintext request — hold whichever way the answer goes.
#
# Toolchains are fetched on first use into a cache folder and reused after:
# Go from go.dev, pinned below with its published checksums, and TinyGo from
# the fork's GitHub release through install-tools.sh, the repository's install
# manifest, the same one line the container spike runs, so both halves fetch
# the same fork and lay the same net/http/cookiejar over it. Re-asking the
# question is bumping the one number in that file.
#
# Requirements: macOS, dispat on PATH, git checkout, network.
set -u

# The Go pin. TinyGo's is not here: it is read from install-tools.sh below,
# because a second copy of it beside the manifest is a second thing to bump.
GO_VERSION=1.26.7
GO_SHA256_ARM64=020a1e8224811be75163e920bc77e0926a1390a6aeea19bdcf23f74b9d749f6d
GO_SHA256_AMD64=92e8b34bff3c89ab16404c595669ac8cb004cc2f676dcbd1f5b87a6b8def3b47

die() {
	echo "tinygo-spike-darwin: $*" >&2
	exit 1
}

[ "$(uname -s)" = Darwin ] || die "this half runs on macOS; the linux half is Dockerfile.tinygo"
command -v dispat >/dev/null 2>&1 || die "dispat is not on PATH; https://dispat.dev/reference/ci/#the-install-script"

root=$(cd "$(dirname "$0")/.." && pwd)
manifest="$root/scripts/install-tools.sh"

# The fork's pin, from the manifest that carries it, so the log line and the
# check below name the version the install is actually about to fetch.
TINYGO_VERSION=$(sh "$manifest" --version tinygo) || die "could not read the tinygo pin"

cache="${XDG_CACHE_HOME:-$HOME/.cache}/crier-tinygo-spike"
spike="$root/coverage/tinygo-spike"
out="$cache/out"
mkdir -p "$cache" "$spike" "$out"

case "$(uname -m)" in
arm64) host_arch=arm64 go_sha="$GO_SHA256_ARM64" ;;
x86_64) host_arch=amd64 go_sha="$GO_SHA256_AMD64" ;;
*) die "unexpected host arch $(uname -m)" ;;
esac

# --- the toolchains, fetched once ---------------------------------------------

# Go first: TinyGo refuses to run without a host Go, the gc twins are the
# control every probe is read against, and the manifest lays Go's own
# net/http/cookiejar over the fork from this very GOROOT.
if [ ! -x "$cache/go/bin/go" ]; then
	echo "fetching go$GO_VERSION darwin-$host_arch" >&2
	curl -fsSL -o "$cache/go.tar.gz" "https://go.dev/dl/go$GO_VERSION.darwin-$host_arch.tar.gz" \
		|| die "go download failed"
	echo "$go_sha  $cache/go.tar.gz" | shasum -a 256 -c - >/dev/null || die "go tarball checksum mismatch"
	tar -xzf "$cache/go.tar.gz" -C "$cache" && rm "$cache/go.tar.gz"
fi
export PATH="$cache/go/bin:$cache/tinygo/bin:$PATH"
export GOPATH="$cache/gopath"

# TinyGo from the fork's release, through the install manifest, which is the
# command under test with its pin already in it. The manifest is idempotent on
# `tinygo version` and unpacks the whole toolchain tree rather than one binary,
# because tinygo is its bin/ plus the lib/ and src/ beside it. dispat's own
# update check is turned off for the one call it makes here.
DISPAT_UPDATE_CHECK=false INSTALL_TOOLS_PREFIX="$cache" sh "$manifest" tinygo \
	|| die "install-tools.sh tinygo failed (is v$TINYGO_VERSION published?)"

# --- the build probe and the size record (mirrors tinygo-spike-build) ---------

# The lines a tiny build would use: -tags noasm takes the pure-Go paths of the
# checksum libraries that offer one, and internal/tinyshim (built in under the
# tinygo tag) stands in for the ones that do not. -interp-timeout raises the
# budget of TinyGo's compile-time initialiser pass, which minio-go's regexps
# take minutes of; the default three overran once on a busy machine.
log="$spike/darwin-build.log"
: >"$log"
cd "$root" || die "no checkout at $root"
V=github.com/yohimik/crier/internal/version.Version
noise="Reserved registers on the clobber list"
for arch in amd64 arm64; do
	echo "=== tinygo build darwin/$arch ===" >>"$log"
	GOOS=darwin GOARCH="$arch" tinygo build -opt=z -no-debug -tags noasm \
		-interp-timeout=15m -ldflags="-X $V=0.0.0-spike" \
		-o "$out/tinygo-crier-darwin-$arch" ./cmd/crier >"$cache/tinygo.raw" 2>&1
	status=$?
	n=$(grep -c "$noise" "$cache/tinygo.raw" || true)
	grep -v "$noise" "$cache/tinygo.raw" >>"$log"
	[ "$n" -eq 0 ] || echo "($n identical \"$noise\" warnings elided)" >>"$log"
	echo "exit=$status" >>"$log"
	echo "=== gc build darwin/$arch ===" >>"$log"
	GOOS=darwin GOARCH="$arch" CGO_ENABLED=0 go build -trimpath \
		-ldflags "-s -w -X $V=0.0.0-spike" \
		-o "$out/gc-crier-darwin-$arch" ./cmd/crier >>"$log" 2>&1
	echo "exit=$?" >>"$log"
done
{
	echo "target                tinygo        gc            ratio"
	for arch in amd64 arm64; do
		t=$(wc -c <"$out/tinygo-crier-darwin-$arch" 2>/dev/null || echo 0)
		g=$(wc -c <"$out/gc-crier-darwin-$arch" 2>/dev/null || echo 0)
		if [ "$g" -gt 0 ] && [ "$t" -gt 0 ]; then
			r=$(awk -v t="$t" -v g="$g" 'BEGIN{printf "%.3f", t/g}')
		else
			r="n/a"
		fi
		printf '%-20s  %-12s  %-12s  %s\n' "darwin/$arch" "$t" "$g" "$r"
	done
} >"$spike/darwin-sizes.log"
cat "$log" "$spike/darwin-sizes.log"

# The arches this host can execute: its own always, the other through
# Rosetta when installed. A skipped arch is recorded, not silent.
runnable=$host_arch
skipped=""
if [ "$host_arch" = arm64 ] && arch -x86_64 /usr/bin/true 2>/dev/null; then
	runnable="arm64 amd64"
elif [ "$host_arch" = arm64 ]; then
	skipped="darwin/amd64 skipped: Rosetta not installed"
else
	skipped="darwin/arm64 skipped: an amd64 host cannot run it"
fi

# --- the execution probe (mirrors tinygo-spike-run) ---------------------------

log="$spike/darwin-run.log"
: >"$log"
echo "=== host arch: $host_arch, runnable: $runnable ===" >>"$log"
[ -z "$skipped" ] || echo "=== $skipped ===" >>"$log"
for arch in $runnable; do
	bin="$out/tinygo-crier-darwin-$arch"
	echo "=== $bin --version ===" >>"$log"
	"$bin" --version >>"$log" 2>&1
	echo "exit=$?" >>"$log"
	echo "=== darwin/$arch render probe: examples/business-promo ===" >>"$log"
	"$bin" render --config "$root/examples/business-promo/crier.yaml" \
		--render-output "$out/card-darwin-$arch.png" >>"$log" 2>&1
	echo "exit=$?" >>"$log"
	ls -la "$out/card-darwin-$arch.png" >>"$log" 2>&1
	echo "=== darwin/$arch https probe: self-update --check ===" >>"$log"
	"$bin" self-update --check --json >>"$log" 2>&1
	echo "exit=$?" >>"$log"
	echo "=== darwin/$arch https probe: gc twin, for comparison ===" >>"$log"
	"$out/gc-crier-darwin-$arch" self-update --check --json >>"$log" 2>&1
	echo "exit=$?" >>"$log"
done
cat "$log"

# --- the bare net stack and the tls-reality rounds (mirrors tinygo-spike-net) -

work=$(mktemp -d) || die "mktemp failed"
trap 'rm -rf "$work"' EXIT

# The instruments, verbatim from Dockerfile.tinygo.
mkdir "$work/netprobe" "$work/tlsreality" "$work/sufake"
sed -n '/^COPY --chown=gopher:gopher <<.PROBE. /,/^PROBE$/p' "$root/Dockerfile.tinygo" \
	| sed '1d;$d' >"$work/netprobe/main.go"
sed -n '/^COPY --chown=gopher:gopher <<.TLSREALITY. /,/^TLSREALITY$/p' "$root/Dockerfile.tinygo" \
	| sed '1d;$d' >"$work/tlsreality/main.go"
sed -n '/^COPY --chown=gopher:gopher <<.SUFAKE. /,/^SUFAKE$/p' "$root/Dockerfile.tinygo" \
	| sed '1d;$d' >"$work/sufake/main.go"
[ -s "$work/netprobe/main.go" ] || die "failed to extract the netprobe heredoc from Dockerfile.tinygo"
[ -s "$work/tlsreality/main.go" ] || die "failed to extract the tlsreality heredoc from Dockerfile.tinygo"
[ -s "$work/sufake/main.go" ] || die "failed to extract the sufake heredoc from Dockerfile.tinygo"

log="$spike/darwin-net.log"
: >"$log"
[ -z "$skipped" ] || echo "=== $skipped ===" >>"$log"
export GOWORK=off
cd "$work/netprobe" || die "no netprobe folder under $work"
printf 'module netprobe\n\ngo 1.24\n' >go.mod
X="-X main.Bare=1.2.3 -X main.Initialed=1.2.3"
for arch in $runnable; do
	echo "=== tinygo build darwin/$arch ===" >>"$log"
	GOOS=darwin GOARCH="$arch" tinygo build -ldflags "$X" -o "$out/netprobe-tinygo-$arch" . >>"$log" 2>&1
	echo "exit=$?" >>"$log"
	echo "=== gc build darwin/$arch ===" >>"$log"
	GOOS=darwin GOARCH="$arch" CGO_ENABLED=0 go build -ldflags "$X" -o "$out/netprobe-gc-$arch" . >>"$log" 2>&1
	echo "exit=$?" >>"$log"
done
echo "=== tls-reality build (gc) ===" >>"$log"
cd "$work/tlsreality" || die "no tlsreality folder under $work"
printf 'module tlsreality\n\ngo 1.24\n' >go.mod
CGO_ENABLED=0 go build -o "$out/tlsreality" . >>"$log" 2>&1
echo "exit=$?" >>"$log"
for arch in $runnable; do
	for toolchain in tinygo gc; do
		for layer in ldflags tcp tls http https; do
			echo "=== $toolchain darwin/$arch $layer ===" >>"$log"
			"$out/netprobe-$toolchain-$arch" "$layer" >>"$log" 2>&1
			echo "exit=$?" >>"$log"
		done
		echo "=== $toolchain darwin/$arch tls-reality ===" >>"$log"
		: >"$work/reality.out"
		"$out/tlsreality" 127.0.0.1:14443 >"$work/reality.out" 2>&1 &
		server=$!
		i=0
		until grep -q listening "$work/reality.out" || [ "$i" -ge 50 ]; do
			i=$((i + 1))
			sleep 0.1
		done
		"$out/netprobe-$toolchain-$arch" tlslocal 127.0.0.1:14443 >>"$log" 2>&1
		wait "$server"
		status=$?
		cat "$work/reality.out" >>"$log"
		echo "exit=$status" >>"$log"
	done
done
cat "$log"

# --- the self-update matrix (mirrors tinygo-spike-selfupdate) -----------------

log="$spike/darwin-selfupdate.log"
: >"$log"
[ -z "$skipped" ] || echo "=== $skipped ===" >>"$log"
fake="$out/sufake"
ca="$work/sufake-ca.pem"
su="$work/su"
mkdir -p "$su"

echo "=== sufake build (gc) ===" >>"$log"
cd "$work/sufake" || die "no sufake folder under $work"
printf 'module sufake\n\ngo 1.24\n' >go.mod
# sufake is a module of its own in a temp folder, so it is built with the
# workspace off; the net section above left it off for its own standalone
# modules, and the crier builds below want the checkout's own module context.
GOWORK=off CGO_ENABLED=0 go build -o "$fake" . >>"$log" 2>&1
echo "exit=$?" >>"$log"
unset GOWORK

# The open point, asked before anything is read against it. Go consults
# SSL_CERT_FILE on unix and hands verification to the platform verifier on
# darwin, so a CA this script generates may be invisible to a trusted row.
# The answer is recorded either way and never worked around: modifying the
# keychain to make a test pass would be measuring the modification.
echo "=== darwin trust: SSL_CERT_FILE honoured? ===" >>"$log"
: >"$work/trust.out"
"$fake" -addr 127.0.0.1:0 -ca "$ca" -asset "$out/gc-crier-darwin-$host_arch" \
	-asset-name "crier-darwin-$host_arch" -version 1.1.0 >"$work/trust.out" 2>&1 &
trust_pid=$!
i=0
until grep -q "sufake: ca" "$work/trust.out" || [ "$i" -ge 100 ]; do
	i=$((i + 1))
	sleep 0.1
done
trust_base=$(sed -n 's/^sufake: listening //p' "$work/trust.out" | head -n 1)
if SSL_CERT_FILE="$ca" curl -fsS -o /dev/null "$trust_base/repos/yohimik/crier/releases" 2>>"$log"; then
	curl_trust=honoured
else
	curl_trust=ignored
fi
# And the client whose answer actually decides the rows below: a Go binary,
# the gc crier this script already built. --check exits 1 when there is
# something to install, which is not a trust failure, so the verdict is read
# from the error rather than from the exit code.
SSL_CERT_FILE="$ca" "$out/gc-crier-darwin-$host_arch" self-update \
	--api-url "$trust_base" --check >"$work/trust.go" 2>&1
if grep -qi "certificate\|x509" "$work/trust.go"; then
	go_trust=ignored
else
	go_trust=honoured
fi
cat "$work/trust.go" >>"$log"
kill "$trust_pid" 2>/dev/null
wait "$trust_pid" 2>/dev/null
cat "$work/trust.out" >>"$log"
echo "SSL_CERT_FILE: curl=$curl_trust go=$go_trust" >>"$log"
echo "(curl reads the file directly; Go on darwin hands verification to the platform verifier, so the two may disagree and the Go answer is the one the rows below are read against)" >>"$log"

# The four binaries the matrix moves between, per runnable arch.
V=github.com/yohimik/crier/internal/version.Version
cd "$root" || die "no checkout at $root"
for arch in $runnable; do
	for pair in tinygo:1.0.0 tinygo:1.1.0 gc:1.0.0 gc:1.1.0; do
		toolchain="${pair%%:*}"
		ver="${pair#*:}"
		echo "=== $toolchain build crier $ver darwin/$arch ===" >>"$log"
		if [ "$toolchain" = tinygo ]; then
			GOOS=darwin GOARCH="$arch" tinygo build -opt=z -no-debug -tags noasm \
				-interp-timeout=15m -ldflags="-X $V=$ver" \
				-o "$out/su-tinygo-$arch-$ver" ./cmd/crier >"$cache/tinygo.raw" 2>&1
			status=$?
			n=$(grep -c "$noise" "$cache/tinygo.raw" || true)
			grep -v "$noise" "$cache/tinygo.raw" >>"$log"
			[ "$n" -eq 0 ] || echo "($n identical \"$noise\" warnings elided)" >>"$log"
			(exit $status)
		else
			GOOS=darwin GOARCH="$arch" CGO_ENABLED=0 go build -trimpath \
				-ldflags "-s -w -X $V=$ver" -o "$out/su-gc-$arch-$ver" ./cmd/crier >>"$log" 2>&1
		fi
		echo "exit=$?" >>"$log"
	done
done

# version_of reads the one line of --version that carries the version.
version_of() {
	if [ -x "$1" ]; then
		"$1" --version 2>&1 | grep '^crier ' | tail -n 1
	else
		echo "(absent)"
	fi
}

start_fake() {
	: >"$work/sufake.out"
	"$fake" -addr 127.0.0.1:0 -ca "$ca" -asset "$1" \
		-asset-name "crier-darwin-$2" -version 1.1.0 >"$work/sufake.out" 2>&1 &
	fake_pid=$!
	i=0
	until grep -q "sufake: ca" "$work/sufake.out" || [ "$i" -ge 100 ]; do
		i=$((i + 1))
		sleep 0.1
	done
	base=$(sed -n 's/^sufake: listening //p' "$work/sufake.out" | head -n 1)
}

# One case: a fresh copy of the old binary, a run against a fresh fake, and
# the versions the exe and its backup report afterwards. crier's self-update
# takes the API through --api-url and names the repository itself.
case_run() {
	label="$1"
	toolchain="$2"
	arch="$3"
	host="$4"
	trust="$5"
	scheme="$6"
	shift 6
	echo "=== $label ===" >>"$log"
	rm -f "$su/crier" "$su/crier.backup"
	cp "$out/su-$toolchain-$arch-1.0.0" "$su/crier"
	chmod 0755 "$su/crier"
	start_fake "$out/su-$toolchain-$arch-1.1.0" "$arch"
	port=${base##*:}
	if [ "$trust" = trusted ]; then
		SSL_CERT_FILE="$ca" "$su/crier" self-update \
			--api-url "$scheme://$host:$port" "$@" >>"$log" 2>&1
	else
		"$su/crier" self-update \
			--api-url "$scheme://$host:$port" "$@" >>"$log" 2>&1
	fi
	echo "exit=$?" >>"$log"
	kill "$fake_pid" 2>/dev/null
	wait "$fake_pid" 2>/dev/null
	cat "$work/sufake.out" >>"$log"
	echo "--- exe: $(version_of "$su/crier")" >>"$log"
	if [ -f "$su/crier.backup" ]; then
		echo "--- backup: $(version_of "$su/crier.backup")" >>"$log"
	else
		echo "--- backup: none" >>"$log"
	fi
}

for arch in $runnable; do
	echo "=== row zero: su-tinygo-$arch-1.0.0 --version ===" >>"$log"
	"$out/su-tinygo-$arch-1.0.0" --version >>"$log" 2>&1
	echo "exit=$?" >>"$log"

	# R: the renderer under the fork, the half of crier the network rows
	# never touch.
	echo "=== R fork render darwin/$arch ===" >>"$log"
	"$out/su-tinygo-$arch-1.0.0" render --config "$root/examples/business-promo/crier.yaml" \
		--render-output "$su/card-$arch.png" >>"$log" 2>&1
	echo "exit=$?" >>"$log"
	ls -la "$su/card-$arch.png" >>"$log" 2>&1

	# The trusted rows. Read them against the SSL_CERT_FILE answer above: on a
	# darwin where the platform verifier decides, a failure here is the
	# verifier refusing an untrusted root, not the fork failing at TLS, and
	# the two are told apart by comparing the gc control against the fork row.
	case_run "A gc control darwin/$arch, CA offered, full update" gc "$arch" localhost trusted https
	case_run "B fork --check darwin/$arch, CA offered" tinygo "$arch" localhost trusted https --check
	case_run "B2 fork --check darwin/$arch by IP literal" tinygo "$arch" 127.0.0.1 trusted https --check
	case_run "C fork full update darwin/$arch, CA offered" tinygo "$arch" localhost trusted https

	echo "=== C2 fork --rollback darwin/$arch, sufake stopped ===" >>"$log"
	"$su/crier" self-update --rollback >>"$log" 2>&1
	echo "exit=$?" >>"$log"
	echo "--- exe: $(version_of "$su/crier")" >>"$log"

	# The two verifier-independent rows. D must fail with a certificate error
	# however darwin decides to trust, because the CA is withheld either way,
	# and E must be refused by the TLS listener itself.
	case_run "D fork full update darwin/$arch, CA withheld" tinygo "$arch" localhost untrusted https
	case_run "E fork plaintext http at the TLS port darwin/$arch" tinygo "$arch" localhost trusted http --check
done
cat "$log"

# --- the black-box suite against the fork binary (mirrors tinygo-spike-e2e) ---

# The whole of test/e2e, driving the fork-built binary of the host's own
# architecture as a user would. CRIER_E2E_BINARY points the harness at it, so
# nothing is rebuilt and no coverage is asked for; the test binary and the
# update targets the self-update tests build are gc's, the control toolchain.
log="$spike/darwin-e2e.log"
: >"$log"
bin="$out/su-tinygo-$host_arch-1.0.0"
echo "=== e2e: test/e2e against the fork-built crier ($bin) ===" >>"$log"
if [ -x "$bin" ]; then
	cd "$root" || die "no checkout at $root"
	CRIER_E2E_BINARY="$bin" go test -tags e2e ./test/e2e -count=1 -timeout 40m -v \
		>"$spike/darwin-e2e-verbose.log" 2>&1
	echo "exit=$?" >>"$log"
	grep -E '^(--- (PASS|FAIL|SKIP)|PASS|FAIL|ok|panic)' "$spike/darwin-e2e-verbose.log" >>"$log"
	{
		echo "--- totals ---"
		printf 'pass=%s fail=%s skip=%s\n' \
			"$(grep -c '^--- PASS' "$spike/darwin-e2e-verbose.log")" \
			"$(grep -c '^--- FAIL' "$spike/darwin-e2e-verbose.log")" \
			"$(grep -c '^--- SKIP' "$spike/darwin-e2e-verbose.log")"
	} >>"$log"
else
	echo "(absent: the fork build did not produce it; see darwin-selfupdate.log)" >>"$log"
	echo "exit=127" >>"$log"
fi
cat "$log"

# The spike's whole answer is the logs; a probe that failed already said so
# in them, and gating here would report one fact where the matrix needs all.
exit 0
