//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The publish-input fit tests, which are rc.8's third defect.
//
// The anthem story was the square anthem.mp4 published with the story frame's
// flags, and publish-only mode passed the file through as it arrived: the
// flags did nothing and Instagram padded the square to 9:16 on its own
// servers, in black rather than in the card's own colour.
//
// These use a real ffmpeg because the fix is a real re-encode. Where there is
// none they skip, the way the rest of the video suite does.

func requireFFmpeg(t *testing.T) (ffmpeg, ffprobe string) {
	t.Helper()
	var err error
	if ffmpeg, err = exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	if ffprobe, err = exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is not installed")
	}
	return ffmpeg, ffprobe
}

// squareClip writes a square clip with a real audio stream, which is what the
// announcement anthem is.
func squareClip(t *testing.T, ffmpeg, path string) {
	t.Helper()
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=c=red:s=240x240:d=1",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the input clip: %v\n%s", err, out)
	}
}

// probe reads one comma-separated set of stream entries out of a file.
func probe(t *testing.T, ffprobe, path, stream, entries string) string {
	t.Helper()
	out, err := exec.Command(ffprobe, "-v", "error", "-select_streams", stream,
		"-show_entries", entries, "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	return strings.TrimSpace(string(out))
}

// TestPublishInputFitsAClipToTheStoryFrame is the defect, end to end and on
// the posted bytes.
//
// Telegram is the platform under test because Telegram takes the bytes: the
// fake receives the actual file, so the assertion is on what was posted rather
// than on what crier said it posted. Instagram would only ever hand over a URL.
func TestPublishInputFitsAClipToTheStoryFrame(t *testing.T) {
	ffmpeg, ffprobe := requireFFmpeg(t)

	f := newFakes(t)
	dir := newProject(t, strings.Join([]string{
		"  telegram:",
		"    enabled: true",
		"    api-base-url: " + f.URL,
		"    token: tg-token",
		"    chat-id: \"@crier\"",
		"    width: 360",
		"    height: 640",
		"    fit: contain",
		"    fit-background: \"#04140c\"",
	}, "\n"))
	clip := filepath.Join(dir, "square.mp4")
	squareClip(t, ffmpeg, clip)

	res := crier(t, dir, nil, "publish", "--publish-input", clip)
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}

	sent, ok := f.find("/sendVideo")
	if !ok {
		t.Fatalf("no video was posted: %v", pathsOf(f))
	}
	posted := filepath.Join(t.TempDir(), "posted.mp4")
	writeUploadedFile(t, sent, "video", posted)

	if got := probe(t, ffprobe, posted, "v", "stream=width,height"); got != "360,640" {
		t.Errorf("the posted clip is %s, want 360,640; the platform should not have to crop it", got)
	}
	// The soundtrack is the point of the clip. A fit that produced a silent
	// story would trade one defect for a worse one.
	if got := probe(t, ffprobe, posted, "a", "stream=codec_name"); got != "aac" {
		t.Errorf("the posted clip's audio is %q, want the aac that was copied over", got)
	}
}

// TestPublishInputLeavesAClipAloneWithoutAFit is the other half: a platform
// that asked for no frame still gets the bytes exactly as they arrived.
func TestPublishInputLeavesAClipAloneWithoutAFit(t *testing.T) {
	ffmpeg, ffprobe := requireFFmpeg(t)

	f := newFakes(t)
	dir := newProject(t, strings.Join([]string{
		"  telegram:",
		"    enabled: true",
		"    api-base-url: " + f.URL,
		"    token: tg-token",
		"    chat-id: \"@crier\"",
	}, "\n"))
	clip := filepath.Join(dir, "square.mp4")
	squareClip(t, ffmpeg, clip)

	res := crier(t, dir, nil, "publish", "--publish-input", clip)
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}

	sent, _ := f.find("/sendVideo")
	posted := filepath.Join(t.TempDir(), "posted.mp4")
	writeUploadedFile(t, sent, "video", posted)
	if got := probe(t, ffprobe, posted, "v", "stream=width,height"); got != "240,240" {
		t.Errorf("the posted clip is %s, want the 240,240 it arrived as", got)
	}
}

// TestPublishInputFitsAClipForInstagramOnly checks the grouping: one platform
// asks for a frame and the other does not, so one clip is reshaped and one is
// passed through, from a single input file.
func TestPublishInputFitsAClipForInstagramOnly(t *testing.T) {
	ffmpeg, ffprobe := requireFFmpeg(t)

	f := newFakes(t)
	dir := newProject(t, strings.Join([]string{
		"  instagram:",
		"    enabled: true",
		"    api-base-url: " + f.URL + "/instagram",
		"    token: ig-token",
		"    user-id: ig-user",
		"    poll-interval: 1ms",
		"    poll-timeout: 5s",
		"    story: true",
		"    width: 360",
		"    height: 640",
		"    fit: contain",
		"    fit-background: \"#04140c\"",
		"  telegram:",
		"    enabled: true",
		"    api-base-url: " + f.URL,
		"    token: tg-token",
		"    chat-id: \"@crier\"",
		"",
		"stage:",
		"  mode: url",
		"  url: " + f.URL + "/staged/clip.mp4",
	}, "\n"))
	clip := filepath.Join(dir, "square.mp4")
	squareClip(t, ffmpeg, clip)

	res := crier(t, dir, nil, "publish", "--publish-input", clip, "--json")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}

	// Instagram was given a story container for a video, from the staged URL.
	story, ok := f.find("/instagram/ig-user/media")
	if !ok {
		t.Fatal("instagram got no container")
	}
	if !strings.Contains(story.Body, "media_type=STORIES") || !strings.Contains(story.Body, "video_url") {
		t.Errorf("the story container = %q", story.Body)
	}

	// Telegram asked for nothing, so it got the file as it arrived.
	sent, _ := f.find("/sendVideo")
	posted := filepath.Join(t.TempDir(), "posted.mp4")
	writeUploadedFile(t, sent, "video", posted)
	if got := probe(t, ffprobe, posted, "v", "stream=width,height"); got != "240,240" {
		t.Errorf("telegram's clip is %s; it asked for no frame", got)
	}

	// The report says one variant was reshaped to the story frame and one was
	// not, which is the grouping working.
	var rep struct {
		Variants []struct {
			Platforms []string `json:"platforms"`
			Width     int      `json:"width"`
			Height    int      `json:"height"`
		} `json:"variants"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &rep); err != nil {
		t.Fatalf("%v\n%s", err, res.Stdout)
	}
	if len(rep.Variants) != 2 {
		t.Fatalf("reported %d variants, want the fitted one and the untouched one: %+v", len(rep.Variants), rep.Variants)
	}
	for _, v := range rep.Variants {
		if len(v.Platforms) == 1 && v.Platforms[0] == "instagram" {
			if v.Width != 360 || v.Height != 640 {
				t.Errorf("instagram's clip is reported as %dx%d, want 360x640", v.Width, v.Height)
			}
		}
	}
}

// writeUploadedFile pulls one file part out of a recorded multipart request
// and writes it to disk, so a test can probe the bytes that were posted.
func writeUploadedFile(t *testing.T, req request, field, out string) {
	t.Helper()
	_, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("the request is not multipart: %v", err)
	}
	r := multipart.NewReader(strings.NewReader(req.Body), params["boundary"])
	for {
		part, err := r.NextPart()
		if err != nil {
			t.Fatalf("no %q part in the request: %v", field, err)
		}
		if part.FormName() != field {
			continue
		}
		f, err := os.Create(out)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		if _, err := io.Copy(f, part); err != nil {
			t.Fatal(err)
		}
		return
	}
}
