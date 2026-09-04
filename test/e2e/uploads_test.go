//go:build e2e

package e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The upload paths under load. Every other publish test posts a few kilobytes
// and asks whether the right endpoint was called; these post megabytes and ask
// the fakes what arrived, byte for byte. That is what a network stack gets
// wrong first — a short write, a chunk out of order, a body cut at a buffer
// boundary — and it is the question a different toolchain building crier has
// to answer before it ships: the same suite runs against a TinyGo build of
// the binary, whose net is a port rather than the Go runtime's.

// uploadSize is what a clip weighs here: past X's 5 MiB segment and
// LinkedIn's 4 MiB part, so both split it, and past TikTok's 5 MiB minimum,
// so its one chunk carries a remainder. Well under the fakes' record cap.
const uploadSize = 6 << 20

// noise fills a buffer from a seed, deterministically, so a run is
// reproducible and no transport can quietly repeat, drop or reorder a piece
// of it without the digest changing.
func noise(buf []byte, seed uint64) {
	x := seed | 1
	for i := range buf {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		buf[i] = byte(x)
	}
}

// clipBytes is an MP4-shaped payload: the ftyp box crier sniffs to call a
// file a video, then noise to the size asked for.
func clipBytes(size int) []byte {
	out := make([]byte, size)
	header := append([]byte{0, 0, 0, 0x18}, "ftypmp42\x00\x00\x00\x00moov"...)
	copy(out, header)
	noise(out[len(header):], 0x5eed)
	return out
}

// bigPNG is a square of noise as a PNG: incompressible, so its file is as
// large as its pixels, and decodable, so a platform's copy can be checked
// for its dimensions whatever format crier turned it into on the way.
func bigPNG(t *testing.T, side int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	noise(img.Pix, 0xb16)
	// Opaque, so a JPEG transcode has nothing to flatten and every platform's
	// copy is the same picture.
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 0xff
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// digest names a payload in a failure message.
func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%d bytes, sha256 %x", len(b), sum[:6])
}

// order sorts the requests a fragment matched into upload order, for the
// platforms that split a file: by a multipart field, a path suffix or a
// Content-Range.
type order func(request) int

func bySegmentIndex(r request) int {
	fields := formFields(r)
	n, _ := strconv.Atoi(fields.Get("segment_index"))
	return n
}

func byPathSuffix(r request) int {
	n, _ := strconv.Atoi(r.Path[strings.LastIndex(r.Path, "-")+1:])
	return n
}

func byContentRange(r request) int {
	// bytes start-end/total
	rng := strings.TrimPrefix(r.Header.Get("Content-Range"), "bytes ")
	start, _ := strconv.Atoi(strings.SplitN(rng, "-", 2)[0])
	return start
}

func byPartNumber(r request) int {
	n, _ := strconv.Atoi(r.Header.Get("X-PartNumber"))
	return n
}

// formFields reads the non-file fields of a multipart body.
func formFields(r request) url.Values {
	out := url.Values{}
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return out
	}
	mr := multipart.NewReader(strings.NewReader(r.Body), params["boundary"])
	for {
		p, err := mr.NextPart()
		if err != nil {
			return out
		}
		if p.FileName() == "" {
			v, _ := io.ReadAll(p)
			out.Add(p.FormName(), string(v))
		}
	}
}

// partsOn returns the files the fakes received on the path fragment, in
// upload order: every file part of a multipart body, or the body itself when
// the request was not multipart. multipartOnly says a request without a file
// part carries nothing: X's INIT and FINALIZE calls share the upload path
// with its APPENDs and are forms, not chunks.
func partsOn(t *testing.T, f *fakes, fragment string, by order, multipartOnly bool) [][]byte {
	t.Helper()
	reqs := f.findAll(fragment)
	if by != nil {
		sort.SliceStable(reqs, func(i, j int) bool { return by(reqs[i]) < by(reqs[j]) })
	}
	var out [][]byte
	for _, r := range reqs {
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err == nil && strings.HasPrefix(mediaType, "multipart/") {
			mr := multipart.NewReader(strings.NewReader(r.Body), params["boundary"])
			for {
				p, err := mr.NextPart()
				if err != nil {
					break
				}
				if p.FileName() == "" {
					continue
				}
				part, err := io.ReadAll(p)
				if err != nil {
					t.Fatalf("%s: reading a part: %v", fragment, err)
				}
				out = append(out, part)
			}
			continue
		}
		if !multipartOnly {
			out = append(out, []byte(r.Body))
		}
	}
	return out
}

// assembled is a split upload put back together.
func assembled(parts [][]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// largest is the biggest file a platform received on a path, which is the
// picture or the clip when the request also carried a poster or a thumbnail.
func largest(parts [][]byte) []byte {
	var out []byte
	for _, p := range parts {
		if len(p) > len(out) {
			out = p
		}
	}
	return out
}

// uploadSpec is where one platform's bytes land in the fakes.
type uploadSpec struct {
	platform      string
	fragment      string
	by            order
	multipartOnly bool
	// split says the client sent the file in pieces, which are put back
	// together; otherwise the largest file on the path is the one, because a
	// request may carry a poster or a thumbnail beside it.
	split bool
}

// videoPlatforms enables every platform that takes a video, and no other:
// Boosty takes pictures only, and a run that named it would refuse it before
// uploading anything, which is the right behaviour and the wrong test.
func videoPlatforms(f *fakes) string {
	var out []string
	skipping := false
	for _, line := range strings.Split(f.platformConfig(), "\n") {
		header := strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") && strings.HasSuffix(line, ":")
		if header {
			skipping = strings.HasPrefix(line, "  boosty:")
			if skipping {
				continue
			}
			out = append(out, line, "    enabled: true")
			continue
		}
		if skipping && strings.HasPrefix(line, "    ") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n") + "\n" + f.youtubeConfig()
}

// publishReport is the --json report's shape.
type publishReport struct {
	Results []struct {
		Platform string `json:"platform"`
		OK       bool   `json:"ok"`
		ID       string `json:"id"`
		Error    string `json:"error"`
	} `json:"results"`
}

func decodeReport(t *testing.T, res result) publishReport {
	t.Helper()
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	var rep publishReport
	if err := json.Unmarshal([]byte(res.Stdout), &rep); err != nil {
		t.Fatalf("%v\n%s", err, res.Stdout)
	}
	for _, r := range rep.Results {
		if !r.OK {
			t.Errorf("%s failed: %s", r.Platform, r.Error)
		}
	}
	return rep
}

// TestVideoUploadReachesEveryPlatform publishes a six-megabyte clip to the
// thirteen platforms that take one and reads every platform's copy back out
// of the fakes: whole where the platform takes the file in one request, put
// together from its parts where the client split it — X's APPEND segments,
// LinkedIn's ranges, TikTok's chunk with the remainder folded in, YouTube's
// resumable PUT — and by reference where the platform fetches from a URL.
func TestVideoUploadReachesEveryPlatform(t *testing.T) {
	f := newFakes(t)
	dir := t.TempDir()
	clip := clipBytes(uploadSize)
	if err := os.WriteFile(filepath.Join(dir, "clip.mp4"), clip, 0o600); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "crier.yaml", strings.Join([]string{
		"log:",
		"  level: debug",
		"render:",
		"  video:",
		// Reddit wants a poster with a video and YouTube a thumbnail; the
		// fake ffmpeg writes a real one-pixel JPEG for both.
		"    ffmpeg-bin: " + selfPath(t),
		"publish:",
		"  input: clip.mp4",
		"  caption: \"a clip for {{ .Platform }}\"",
		videoPlatforms(f),
		"stage:",
		"  mode: url",
		"  url: " + f.URL + "/staged/clip.mp4",
	}, "\n"))

	res := crier(t, dir, []string{helperEnv + "=ffmpeg-poster"}, "publish", "--json")
	rep := decodeReport(t, res)
	if len(rep.Results) != 13 {
		t.Fatalf("published to %d platforms, want 13: %+v", len(rep.Results), rep.Results)
	}

	for _, spec := range []uploadSpec{
		{"telegram", "/sendVideo", nil, false, false},
		{"discord", "/discord/webhook", nil, false, false},
		{"mastodon", "/mastodon/api/v2/media", nil, false, false},
		{"facebook", "/videos", nil, false, false},
		{"x", "/x/2/media/upload", bySegmentIndex, true, true},
		{"linkedin", "/linkedin-part-", byPathSuffix, false, true},
		{"slack", "/slack-upload", nil, false, false},
		{"reddit", "/reddit-store", nil, false, false},
		{"vk", "/vk-video-upload", nil, false, false},
		{"tiktok", "/tiktok-upload", byContentRange, false, true},
		{"youtube", "/youtube-upload/", nil, false, true},
	} {
		parts := partsOn(t, f, spec.fragment, spec.by, spec.multipartOnly)
		got := largest(parts)
		if spec.split {
			got = assembled(parts)
		}
		if !bytes.Equal(got, clip) {
			t.Errorf("%s received %s on %s (%d files); the clip is %s", spec.platform, digest(got), spec.fragment, len(parts), digest(clip))
		}
	}

	// The client split where the platform's limits say it must.
	if n := len(f.findAll("/linkedin-part-")); n != 2 {
		t.Errorf("linkedin got %d parts of a %d-byte clip; 4 MiB ranges make two", n, uploadSize)
	}
	appends := 0
	for _, r := range f.findAll("/x/2/media/upload") {
		if formFields(r).Get("command") == "APPEND" {
			appends++
		}
	}
	if appends != 2 {
		t.Errorf("x got %d APPEND segments of a %d-byte clip; 5 MiB segments make two", appends, uploadSize)
	}
	tiktok := f.findAll("/tiktok-upload")
	if len(tiktok) != 1 || tiktok[0].Header.Get("Content-Range") != fmt.Sprintf("bytes 0-%d/%d", uploadSize-1, uploadSize) {
		t.Errorf("tiktok got %d chunks; the remainder past 5 MiB folds into one, with the range saying so", len(tiktok))
	}
	if r, ok := f.find("/youtube-upload/"); !ok || len(r.Body) != uploadSize {
		t.Errorf("youtube's resumable PUT carried %d bytes, want %d", len(r.Body), uploadSize)
	}

	// The two that fetch rather than take: the container names the staged
	// clip, and nothing of it reaches the API host.
	for _, spec := range []struct{ platform, fragment string }{
		{"instagram", "/instagram/ig-user/media"},
		{"threads", "/threads/th-user/threads"},
	} {
		found := false
		for _, r := range f.findAll(spec.fragment) {
			if strings.Contains(r.Body+r.Query, "staged%2Fclip.mp4") || strings.Contains(r.Body+r.Query, "staged/clip.mp4") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s never named the staged clip on %s", spec.platform, spec.fragment)
		}
	}
}

// TestLargeImageUploadReachesEveryPlatform is the picture half: a
// twelve-megabyte PNG of noise, publish-only, to the thirteen platforms that
// take a picture. crier is free to transcode it on the way — Instagram wants
// a JPEG, Boosty splits into parts — so what each platform's copy is held to
// is that it decodes to the same square, which a truncated or reordered body
// cannot.
func TestLargeImageUploadReachesEveryPlatform(t *testing.T) {
	const side = 1800
	f := newFakes(t)
	dir := t.TempDir()
	pic := bigPNG(t, side)
	if len(pic) < 4<<20 {
		t.Fatalf("the noise compressed to %d bytes; the test wants megabytes", len(pic))
	}
	if err := os.WriteFile(filepath.Join(dir, "big.png"), pic, 0o600); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "crier.yaml", strings.Join([]string{
		"log:",
		"  level: debug",
		"publish:",
		"  input: big.png",
		"  caption: \"a picture for {{ .Platform }}\"",
		enableAll(f, ""),
		"stage:",
		"  mode: url",
		"  url: " + f.URL + "/staged/big.png",
	}, "\n"))

	res := crier(t, dir, nil, "publish", "--json")
	rep := decodeReport(t, res)
	if len(rep.Results) != 13 {
		t.Fatalf("published to %d platforms, want 13: %+v", len(rep.Results), rep.Results)
	}

	for _, spec := range []uploadSpec{
		{"telegram", "/sendPhoto", nil, false, false},
		{"discord", "/discord/webhook", nil, false, false},
		{"mastodon", "/mastodon/api/v2/media", nil, false, false},
		{"facebook", "/photos", nil, false, false},
		{"x", "/x/2/media/upload", bySegmentIndex, true, false},
		{"linkedin", "/linkedin-upload", nil, false, false},
		{"slack", "/slack-upload", nil, false, false},
		{"reddit", "/reddit-store", nil, false, false},
		{"vk", "/vk-photo-upload", nil, false, false},
		{"boosty", "/boosty-upload/upload/", byPartNumber, false, true},
	} {
		parts := partsOn(t, f, spec.fragment, spec.by, spec.multipartOnly)
		got := largest(parts)
		if spec.split {
			got = assembled(parts)
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(got))
		if err != nil {
			t.Errorf("%s received %s on %s, which does not decode: %v", spec.platform, digest(got), spec.fragment, err)
			continue
		}
		if cfg.Width != side || cfg.Height != side {
			t.Errorf("%s received a %dx%d %s on %s, want %dx%d", spec.platform, cfg.Width, cfg.Height, format, spec.fragment, side, side)
		}
		// crier re-encodes what it publishes, so the bytes are its own; the
		// floor is what keeps this a test of a large body rather than a
		// small one that happened to decode.
		if len(got) < 1<<20 {
			t.Errorf("%s received only %d bytes of a %d-pixel picture", spec.platform, len(got), side*side)
		}
	}
	// Boosty's completion counts what arrived; a part that went missing
	// would have been refused there, so one accepted post is the assertion.
	if n := f.count("/boosty-upload/upload/"); n < 2 {
		t.Errorf("boosty saw %d upload calls; a 5 MiB part and its completion make two", n)
	}

	// The three that fetch rather than take.
	for _, spec := range []struct{ platform, fragment string }{
		{"instagram", "/instagram/ig-user/media"},
		{"threads", "/threads/th-user/threads"},
		{"tiktok", "/tiktok/v2/post/publish/content/init/"},
	} {
		found := false
		for _, r := range f.findAll(spec.fragment) {
			if strings.Contains(r.Body+r.Query, "staged%2Fbig.png") || strings.Contains(r.Body+r.Query, "staged/big.png") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s never named the staged picture on %s", spec.platform, spec.fragment)
		}
	}
}

// TestPublishOverTLSVerifiesThePlatform is the one publish test with a real
// handshake in it. The fakes serve HTTPS behind a self-signed certificate;
// with that certificate in SSL_CERT_FILE crier posts, and the fake records
// that the request came in over a completed handshake; without it crier
// refuses to post rather than trusting a certificate it cannot verify, and
// says why. A client whose TLS was a stub would pass the first half and fail
// the second, which is why both are here.
//
// darwin hands certificate verification to the platform verifier, which does
// not read SSL_CERT_FILE, so the trusting half is skipped there rather than
// modifying a keychain to make it pass.
func TestPublishOverTLSVerifiesThePlatform(t *testing.T) {
	f, pemFile := newTLSFakes(t)
	dir := newProject(t, strings.Join([]string{
		"  telegram:",
		"    enabled: true",
		"    api-base-url: " + f.URL,
		"    token: t",
		"    chat-id: c",
		"  discord:",
		"    enabled: true",
		"    webhook-url: " + f.URL + "/discord/webhook",
	}, "\n"))

	// Untrusted: nothing posted, and the reason is the certificate.
	res := crier(t, dir, nil, "publish", "--json")
	if res.Code != exitPublish {
		t.Fatalf("without the certificate: code=%d stderr=%s", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "certificate") && !strings.Contains(res.Stderr, "x509") {
		t.Errorf("the refusal does not name the certificate: %s", res.Stderr)
	}
	if n := len(f.all()); n != 0 {
		t.Errorf("%d requests reached the platform without a verified certificate", n)
	}

	if runtime.GOOS == "darwin" {
		t.Skip("darwin's verifier does not read SSL_CERT_FILE")
	}

	// Trusted: posted, over a handshake the server completed.
	res = crier(t, dir, []string{"SSL_CERT_FILE=" + pemFile}, "publish", "--json")
	rep := decodeReport(t, res)
	if len(rep.Results) != 2 {
		t.Fatalf("published to %d platforms, want 2: %+v", len(rep.Results), rep.Results)
	}
	for _, fragment := range []string{"/sendPhoto", "/discord/webhook"} {
		r, ok := f.find(fragment)
		if !ok {
			t.Errorf("nothing reached %s", fragment)
			continue
		}
		if !r.TLS {
			t.Errorf("%s arrived without a completed TLS handshake", fragment)
		}
	}
}
