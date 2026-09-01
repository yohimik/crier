package app

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/render"
)

// samplePNG is a real encoded image, because sniffing reads bytes and a
// hand-written header would not survive being decoded afterwards.
func samplePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sampleJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeBytes(t *testing.T, dir, name string, body []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestSniffReadsBytesNotNames is the point of sniffing at all: a file called
// .png that holds a JPEG would otherwise be uploaded with the wrong content
// type, and the platforms that check reject it with a message about something
// else entirely.
func TestSniffReadsBytesNotNames(t *testing.T) {
	dir := t.TempDir()
	for _, tt := range []struct {
		name        string
		body        []byte
		wantKind    render.Kind
		wantFormat  config.Format
		contentType string
	}{
		{"a.png", samplePNG(t, 4, 3), render.KindImage, config.PNG, "image/png"},
		{"b.png", sampleJPEG(t, 4, 3), render.KindImage, config.JPEG, "image/jpeg"},
		{"c.gif", []byte("GIF89a" + strings.Repeat("\x00", 10)), render.KindGIF, "", render.GIFContentType},
		{"d.gif", []byte("GIF87a" + strings.Repeat("\x00", 10)), render.KindGIF, "", render.GIFContentType},
		{"e.mp4", []byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00"), render.KindVideo, "", render.VideoContentType},
	} {
		path := writeBytes(t, dir, tt.name, tt.body)
		kind, format, contentType, err := sniff(path)
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if kind != tt.wantKind || format != tt.wantFormat || contentType != tt.contentType {
			t.Errorf("%s = %s/%s/%s, want %s/%s/%s",
				tt.name, kind, format, contentType, tt.wantKind, tt.wantFormat, tt.contentType)
		}
	}

	// Anything else is refused by name rather than uploaded and rejected.
	path := writeBytes(t, dir, "notes.txt", []byte("hello"))
	if _, _, _, err := sniff(path); err == nil || !strings.Contains(err.Error(), "PNG, JPEG, GIF or MP4") {
		t.Errorf("err = %v", err)
	}
}

func TestLoadInputMeasuresTheImage(t *testing.T) {
	dir := t.TempDir()
	path := writeBytes(t, dir, "card.png", samplePNG(t, 40, 20))

	art, err := LoadInput(path)
	if err != nil {
		t.Fatal(err)
	}
	if art.Kind != render.KindImage || art.Format != config.PNG {
		t.Errorf("artifact = %+v", art)
	}
	if art.Width != 40 || art.Height != 20 {
		t.Errorf("size = %dx%d", art.Width, art.Height)
	}
	if art.Size == 0 {
		t.Error("no size")
	}

	if _, err := LoadInput(filepath.Join(dir, "missing.png")); err == nil {
		t.Error("a missing file should fail")
	}
	if _, err := LoadInput(dir); err == nil {
		t.Error("a directory should fail")
	}
}

// TestModeExclusivity: two answers to "where does the artifact come from" is a
// configuration whose author believed two different things.
func TestModeExclusivity(t *testing.T) {
	base := config.Defaults()
	if got, err := ModeOf(&base); err != nil || got != ModeFull {
		t.Errorf("= %v, %v", got, err)
	}

	input := config.Defaults()
	input.Publish.Input = "card.png"
	if got, err := ModeOf(&input); err != nil || got != ModePublishInput {
		t.Errorf("= %v, %v", got, err)
	}
	if got := ModePublishInput.String(); got != "publish.input" {
		t.Errorf("String() = %q", got)
	}

	frames := config.Defaults()
	frames.Render.Video.FramesInput = "frames/"
	if got, err := ModeOf(&frames); err != nil || got != ModeEncodeFrames {
		t.Errorf("= %v, %v", got, err)
	}
	if got := ModeEncodeFrames.String(); got != "render.video.frames-input" {
		t.Errorf("String() = %q", got)
	}
	if got := ModeFull.String(); got != "full" {
		t.Errorf("String() = %q", got)
	}

	both := config.Defaults()
	both.Publish.Input = "card.png"
	both.Render.Video.FramesInput = "frames/"
	if _, err := ModeOf(&both); err == nil || codeOf(err) != ExitConfig {
		t.Errorf("two sources should be a config error: %v", err)
	}

	withVideo := config.Defaults()
	withVideo.Publish.Input = "card.png"
	withVideo.Render.Video.Enabled = true
	if _, err := ModeOf(&withVideo); err == nil {
		t.Error("an input plus a video to make should be a config error")
	}
}

// TestFrameFilesAreLexicographic is the ordering everything downstream assumes,
// and the reason a frame numbered without padding is a bug worth finding here.
func TestFrameFilesAreLexicographic(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"frame-0003.png", "frame-0001.png", "frame-0002.png",
		"notes.txt", "sub",
	} {
		if name == "sub" {
			if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		writeBytes(t, dir, name, samplePNG(t, 4, 4))
	}

	files, err := FrameFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("files = %v, want the three images", files)
	}
	for i, want := range []string{"frame-0001.png", "frame-0002.png", "frame-0003.png"} {
		if filepath.Base(files[i]) != want {
			t.Errorf("file %d = %s, want %s", i, filepath.Base(files[i]), want)
		}
	}

	// A glob works too, and picks the same files.
	globbed, err := FrameFiles(filepath.Join(dir, "frame-*.png"))
	if err != nil {
		t.Fatal(err)
	}
	if len(globbed) != 3 || filepath.Base(globbed[0]) != "frame-0001.png" {
		t.Errorf("glob = %v", globbed)
	}

	if _, err := FrameFiles(filepath.Join(dir, "nothing-*.png")); err == nil {
		t.Error("matching nothing should be an error")
	}
	if _, err := FrameFiles("  "); err == nil {
		t.Error("an empty spec should be an error")
	}
}

// TestFramesMustAgreeAboutTheSize: ffmpeg is told one frame size up front, so
// a frame of another size would be written into the stream as garbage.
func TestFramesMustAgreeAboutTheSize(t *testing.T) {
	dir := t.TempDir()
	writeBytes(t, dir, "a.png", samplePNG(t, 8, 8))
	writeBytes(t, dir, "b.png", samplePNG(t, 8, 8))
	writeBytes(t, dir, "c.png", samplePNG(t, 9, 8))

	files, err := FrameFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	reader, first, err := newFrameReader(files)
	if err != nil {
		t.Fatal(err)
	}
	if first.Bounds().Dx() != 8 {
		t.Errorf("the first frame decides the size: %v", first.Bounds())
	}
	if _, err := reader.at(t.Context(), 1); err != nil {
		t.Errorf("a matching frame should read: %v", err)
	}
	err = func() error { _, err := reader.at(t.Context(), 2); return err }()
	if err == nil || !strings.Contains(err.Error(), "same size") {
		t.Fatalf("err = %v, want the size mismatch", err)
	}
	if _, err := reader.at(t.Context(), 99); err == nil {
		t.Error("a frame past the end should fail")
	}
}

// TestDecodeFrameNormalisesTheOrigin: a decoded image may have bounds that do
// not start at (0,0), and the encoder indexes from the origin.
func TestDecodeFrameNormalisesTheOrigin(t *testing.T) {
	dir := t.TempDir()
	path := writeBytes(t, dir, "a.jpg", sampleJPEG(t, 6, 4))
	img, err := decodeFrame(path)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Min != (image.Point{}) {
		t.Errorf("bounds = %v", img.Bounds())
	}
	if img.Bounds().Dx() != 6 || img.Bounds().Dy() != 4 {
		t.Errorf("size = %v", img.Bounds())
	}
	if _, err := decodeFrame(filepath.Join(dir, "nope.png")); err == nil {
		t.Error("a missing frame should fail")
	}
	broken := writeBytes(t, dir, "broken.png", []byte("not a png"))
	if _, err := decodeFrame(broken); err == nil {
		t.Error("an undecodable frame should fail")
	}
}
