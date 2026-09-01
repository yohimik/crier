package render

import (
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yohimik/crier/internal/config"
)

func transparentImage(w, h int) *image.RGBA {
	return image.NewRGBA(image.Rect(0, 0, w, h))
}

func TestEncodePNGKeepsTransparency(t *testing.T) {
	dir := t.TempDir()
	e := Encoder{Dir: dir, JPEGQuality: 90}
	art, err := e.Encode(transparentImage(4, 3), config.PNG, "card")
	if err != nil {
		t.Fatal(err)
	}
	if art.Kind != KindImage || art.Format != config.PNG || art.ContentType != "image/png" {
		t.Errorf("artifact = %+v", art)
	}
	if art.Width != 4 || art.Height != 3 || art.Size == 0 {
		t.Errorf("artifact = %+v", art)
	}
	if filepath.Ext(art.Path) != ".png" {
		t.Errorf("path = %q", art.Path)
	}

	f, err := os.Open(art.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, a := img.At(1, 1).RGBA(); a != 0 {
		t.Errorf("alpha = %d, want the transparency kept", a)
	}
}

func TestEncodeJPEGFlattensOntoTheBackground(t *testing.T) {
	dir := t.TempDir()
	e := Encoder{Dir: dir, JPEGQuality: 95, Background: color.RGBA{R: 255, A: 255}}
	art, err := e.Encode(transparentImage(8, 8), config.JPEG, "card")
	if err != nil {
		t.Fatal(err)
	}
	if art.ContentType != "image/jpeg" || filepath.Ext(art.Path) != ".jpg" {
		t.Errorf("artifact = %+v", art)
	}

	f, err := os.Open(art.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, a := img.At(4, 4).RGBA()
	if a>>8 != 255 {
		t.Errorf("jpeg must be opaque, alpha = %d", a>>8)
	}
	if r>>8 < 200 || g>>8 > 60 || b>>8 > 60 {
		t.Errorf("pixel = %d,%d,%d, want the red background", r>>8, g>>8, b>>8)
	}
}

func TestEncodeDefaultBackgroundIsWhite(t *testing.T) {
	dir := t.TempDir()
	art, err := Encoder{Dir: dir, JPEGQuality: 95}.Encode(transparentImage(8, 8), config.JPEG, "c")
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(art.Path)
	defer func() { _ = f.Close() }()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := img.At(4, 4).RGBA()
	if r>>8 < 240 || g>>8 < 240 || b>>8 < 240 {
		t.Errorf("pixel = %d,%d,%d, want white", r>>8, g>>8, b>>8)
	}
}

func TestEncodeToCreatesTheDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "card.png")
	if _, err := (Encoder{}).EncodeTo(transparentImage(2, 2), config.PNG, path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestEncodeErrors(t *testing.T) {
	if _, err := (Encoder{}).Encode(transparentImage(1, 1), config.PNG, "x"); err == nil ||
		!strings.Contains(err.Error(), "no output directory") {
		t.Errorf("err = %v", err)
	}
	if _, err := (Encoder{Dir: t.TempDir()}).Encode(nil, config.PNG, "x"); err == nil ||
		!strings.Contains(err.Error(), "no image") {
		t.Errorf("err = %v", err)
	}
	// A path under a file rather than a directory cannot be created.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (Encoder{}).EncodeTo(transparentImage(1, 1), config.PNG, filepath.Join(blocker, "x.png")); err == nil {
		t.Error("expected an error writing under a file")
	}
}

func TestEncodeUnknownFormatFallsBackToPNG(t *testing.T) {
	dir := t.TempDir()
	art, err := (Encoder{Dir: dir}).EncodeTo(transparentImage(2, 2), config.Format("weird"), filepath.Join(dir, "x.bin"))
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(art.Path)
	defer func() { _ = f.Close() }()
	if _, err := png.Decode(f); err != nil {
		t.Errorf("want a PNG, got %v", err)
	}
}
