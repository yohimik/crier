package render

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fc "github.com/benoitkugler/textprocessing/fontconfig"
	"github.com/rs/zerolog"
	"golang.org/x/image/font/gofont/goregular"
)

func be16(v uint16) []byte { b := make([]byte, 2); binary.BigEndian.PutUint16(b, v); return b }
func be32(v uint32) []byte { b := make([]byte, 4); binary.BigEndian.PutUint32(b, v); return b }

// poisonFont builds a file that makes the font parser crash rather than refuse.
//
// It is the shape of the real one: an Apple bitmap TrueType font, carrying a
// `bhed` table where a `head` would be and no outlines at all. The parser's
// summary loader returns early for that kind of file — before it stores the
// summary it built — so the summary stays zero, and reading its style
// dereferences a nil header.
//
// macOS ships exactly such a font at
// /System/Library/Fonts/Supplemental/NISC18030.ttf, which is what took every
// Mac's entire font collection down with it.
func poisonFont() []byte {
	head := []byte{}
	head = append(head, be32(0x00010000)...) // version
	head = append(head, be32(0x00010000)...) // fontRevision
	head = append(head, be32(0)...)          // checkSumAdjustment
	head = append(head, be32(0x5F0F3CF5)...) // magic
	head = append(head, be16(0)...)          // flags
	head = append(head, be16(1000)...)       // unitsPerEm, in range so the load gets past it
	head = append(head, make([]byte, 16)...) // created, modified
	head = append(head, be16(0)...)          // xMin
	head = append(head, be16(0)...)          // yMin
	head = append(head, be16(1000)...)       // xMax
	head = append(head, be16(1000)...)       // yMax
	head = append(head, be16(0)...)          // macStyle
	head = append(head, be16(8)...)          // lowestRecPPEM
	head = append(head, be16(2)...)          // fontDirectionHint
	head = append(head, be16(0)...)          // indexToLocFormat
	head = append(head, be16(0)...)          // glyphDataFormat

	// A format 4 character map with one segment and the mandatory terminator.
	// Without a usable cmap the file is refused rather than parsed, and a
	// refusal exercises the wrong path entirely.
	sub := []byte{}
	sub = append(sub, be16(4)...)    // format
	sub = append(sub, be16(32)...)   // length
	sub = append(sub, be16(0)...)    // language
	sub = append(sub, be16(4)...)    // segCountX2
	sub = append(sub, be16(4)...)    // searchRange
	sub = append(sub, be16(1)...)    // entrySelector
	sub = append(sub, be16(0)...)    // rangeShift
	sub = append(sub, be16(0x41)...) // endCode
	sub = append(sub, be16(0xFFFF)...)
	sub = append(sub, be16(0)...)    // reservedPad
	sub = append(sub, be16(0x41)...) // startCode
	sub = append(sub, be16(0xFFFF)...)
	sub = append(sub, be16(uint16(0xFFFF&(1-0x41+0x10000)))...) // idDelta
	sub = append(sub, be16(1)...)
	sub = append(sub, be16(0)...) // idRangeOffset
	sub = append(sub, be16(0)...)

	cmap := []byte{}
	cmap = append(cmap, be16(0)...) // version
	cmap = append(cmap, be16(1)...) // numTables
	cmap = append(cmap, be16(3)...) // platformID
	cmap = append(cmap, be16(1)...) // encodingID
	cmap = append(cmap, be32(12)...)
	cmap = append(cmap, sub...)

	tables := []struct {
		tag  string
		body []byte
	}{
		{"bdat", []byte{0, 1, 0, 0}},
		{"bhed", head},
		{"bloc", []byte{0, 1, 0, 0, 0, 0, 0, 0}},
		{"cmap", cmap},
		{"maxp", append(be32(0x00005000), be16(1)...)},
		{"name", append(append(be16(0), be16(0)...), be16(6)...)},
		{"post", append(be32(0x00030000), make([]byte, 28)...)},
	}

	out := append([]byte{}, "true"...) // the Apple TrueType signature
	out = append(out, be16(uint16(len(tables)))...)
	out = append(out, be16(0)...) // searchRange
	out = append(out, be16(0)...) // entrySelector
	out = append(out, be16(0)...) // rangeShift

	offset := uint32(12 + 16*len(tables))
	var dir, body []byte
	for _, t := range tables {
		dir = append(dir, t.tag...)
		dir = append(dir, be32(0)...) // checksum, unverified by the parser
		dir = append(dir, be32(offset)...)
		dir = append(dir, be32(uint32(len(t.body)))...)
		body = append(body, t.body...)
		pad := (4 - len(t.body)%4) % 4
		body = append(body, make([]byte, pad)...)
		offset += uint32(len(t.body) + pad)
	}
	return append(append(out, dir...), body...)
}

// testFontDir builds a directory holding one real font and the files that are
// meant to be skipped.
func testFontDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string][]byte{
		"good.ttf":   goregular.TTF,
		"poison.ttf": poisonFont(),
		"empty.ttf":  {},
		"notes.txt":  []byte("not a font at all"),
		".hidden":    goregular.TTF,
		"metrics.afm": []byte(
			"StartFontMetrics 2.0\nEndFontMetrics\n"),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestOneBadFontDoesNotCostTheOthers is the regression test.
//
// The scan used to be one call for a whole directory tree, wrapped in a single
// recover — so one file the parser crashes on lost every font on the machine.
// On macOS that was not hypothetical: NISC18030.ttf ships with the operating
// system, and every Mac silently fell back to the bundled Go faces while 2,611
// real faces sat there unread.
func TestOneBadFontDoesNotCostTheOthers(t *testing.T) {
	dir := testFontDir(t)
	var buf bytes.Buffer
	logger := zerolog.New(&buf).Level(zerolog.DebugLevel)

	set, stats := scanFontFiles(fc.Standard.Copy(), []string{dir}, logger)

	// The healthy font in the same directory as the poison one is available.
	if len(set) == 0 {
		t.Fatalf("no fonts survived: %+v", stats)
	}
	if stats.Faces != len(set) {
		t.Errorf("stats say %d faces, the set holds %d", stats.Faces, len(set))
	}
	if len(stats.Skipped) == 0 {
		t.Fatal("nothing was skipped, so this test is not exercising the failure")
	}
	var skippedPoison bool
	for _, path := range stats.Skipped {
		if filepath.Base(path) == "poison.ttf" {
			skippedPoison = true
		}
		if filepath.Base(path) == "good.ttf" {
			t.Error("the healthy font was skipped")
		}
	}
	if !skippedPoison {
		t.Errorf("the poison font was not skipped: %v", stats.Skipped)
	}
	// And it was skipped by the recover rather than by an ordinary refusal —
	// otherwise this test would pass with the per-file recover removed.
	if !strings.Contains(buf.String(), "the font parser crashed on this file") {
		t.Errorf("the poison font did not take the panic path:\n%s", buf.String())
	}

	// The hidden file and the metrics sidecar were never opened.
	if stats.Files != 4 {
		t.Errorf("opened %d files, want the four that look like fonts", stats.Files)
	}

	// And the survivor is really usable: it carries the family the file names.
	found := false
	for _, pattern := range set {
		if family, ok := pattern.GetString(fc.FAMILY); ok && strings.Contains(family, "Go") {
			found = true
		}
	}
	if !found {
		t.Error("the surviving font has no family, so it would never be matched")
	}
}

// TestScanReportsWhatItSkipped: a machine quietly losing a font is the thing
// this whole change is about, so the loss has to be visible.
func TestScanReportsWhatItSkipped(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf).Level(zerolog.DebugLevel)

	stats := scanStats{Files: 373, Faces: 2611, Skipped: []string{"/x/NISC18030.ttf"}}
	stats.report(logger)

	out := buf.String()
	if !strings.Contains(out, `"level":"warn"`) {
		t.Errorf("the summary should be a warning: %s", out)
	}
	if !strings.Contains(out, `"skipped":1`) || !strings.Contains(out, `"fonts":2611`) {
		t.Errorf("the summary should carry the counts: %s", out)
	}
	if !strings.Contains(out, "NISC18030.ttf") {
		t.Errorf("the skipped path should be logged: %s", out)
	}

	// A clean scan says so at debug level and warns about nothing.
	buf.Reset()
	scanStats{Files: 10, Faces: 20}.report(logger)
	if strings.Contains(buf.String(), `"level":"warn"`) {
		t.Errorf("a clean scan should not warn: %s", buf.String())
	}
}

// TestPartialScanCaching is the decision this change had to make: the survivors
// are cached even though a Mac always skips at least one file, because
// rescanning several hundred fonts per render to avoid that would be a poor
// trade. A scan that lost a large fraction is not cached, because that is a
// transient failure and a cache is the wrong place to make one permanent.
func TestPartialScanCaching(t *testing.T) {
	for _, tt := range []struct {
		name  string
		stats scanStats
		want  bool
	}{
		{"a whole machine with three bad files", scanStats{Files: 373, Faces: 2611, Skipped: make([]string, 3)}, true},
		{"a clean scan", scanStats{Files: 10, Faces: 30}, true},
		{"half the collection failed", scanStats{Files: 10, Faces: 5, Skipped: make([]string, 5)}, false},
		{"nothing was read at all", scanStats{Files: 10, Skipped: make([]string, 10)}, false},
	} {
		if got := tt.stats.cacheable(); got != tt.want {
			t.Errorf("%s: cacheable = %t, want %t", tt.name, got, tt.want)
		}
	}
}

// TestFontCacheRoundTrip covers the cache crier now writes itself, since the
// library's own ScanAndCache does the very whole-directory scan this change
// exists to avoid.
func TestFontCacheRoundTrip(t *testing.T) {
	dir := testFontDir(t)
	logger := zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.DebugLevel)
	set, _ := scanFontFiles(fc.Standard.Copy(), []string{dir}, logger)

	cache := filepath.Join(t.TempDir(), "fonts.cache")
	writeFontCache(cache, set, logger)

	loaded, err := fc.LoadFontsetFile(cache)
	if err != nil {
		t.Fatalf("the cache does not load back: %v", err)
	}
	if len(loaded) != len(set) {
		t.Errorf("cached %d fonts, loaded %d", len(set), len(loaded))
	}

	// A cache that cannot be written costs the next run a scan and nothing
	// else, so it must not fail anything.
	writeFontCache(filepath.Join(dir, "no-such-dir", "fonts.cache"), set, logger)
}

// TestSystemScanUsesTheCache checks the whole path, including that a second
// call does not walk the disk again.
func TestSystemScanUsesTheCache(t *testing.T) {
	cacheDir := t.TempDir()
	var buf bytes.Buffer
	o := FontOptions{
		CacheDir: cacheDir,
		Logger:   zerolog.New(&buf).Level(zerolog.DebugLevel),
	}

	first, err := systemFontset(o)
	if err != nil {
		t.Fatalf("the first scan failed: %v", err)
	}
	if len(first) == 0 {
		t.Skip("this machine has no readable system fonts")
	}
	if !strings.Contains(buf.String(), "scanning system fonts") {
		t.Errorf("the first call should have scanned: %s", buf.String())
	}

	buf.Reset()
	second, err := systemFontset(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != len(first) {
		t.Errorf("the cached set has %d fonts, the scan found %d", len(second), len(first))
	}
	if !strings.Contains(buf.String(), "loaded the font cache") {
		t.Errorf("the second call should have used the cache: %s", buf.String())
	}
	if strings.Contains(buf.String(), "scanning system fonts") {
		t.Errorf("the second call scanned again: %s", buf.String())
	}
}

func TestFontFileName(t *testing.T) {
	for name, want := range map[string]bool{
		"Helvetica.ttf":    true,
		"font.otf":         true,
		"":                 false,
		".hidden.ttf":      false,
		"metrics.afm":      false,
		"metrics.pfm":      false,
		"encodings.enc.gz": false,
		"fonts.dir":        false,
		"fonts.scale":      false,
		"fonts.alias":      false,
	} {
		if got := fontFileName(name); got != want {
			t.Errorf("fontFileName(%q) = %t, want %t", name, got, want)
		}
	}
}

// --- the standard library log capture ----------------------------------------

// TestCaptureStdlibRoutesThroughZerolog is requirement 11 applied to a
// dependency: fontconfig reports a missing font directory with a bare
// log.Println, and a raw line on standard error is a line crier's log level
// cannot turn down and its format does not describe.
func TestCaptureStdlibRoutesThroughZerolog(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf).Level(zerolog.DebugLevel)

	before := log.Writer()
	restore := captureStdlib(logger, "fontconfig", zerolog.DebugLevel)
	log.Println("invalid font dir", "/nowhere", "no such file or directory")
	log.Println("") // an empty line writes no record
	restore()

	if log.Writer() != before {
		t.Error("the default logger's output was not put back")
	}

	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	if record["level"] != "debug" {
		t.Errorf("level = %v", record["level"])
	}
	if record["from"] != "fontconfig" {
		t.Errorf("from = %v, want the library named", record["from"])
	}
	if msg, _ := record["message"].(string); !strings.Contains(msg, "invalid font dir") {
		t.Errorf("message = %q", msg)
	}
	if strings.Count(buf.String(), "\n") != 1 {
		t.Errorf("an empty line should write no record: %s", buf.String())
	}

	// Nothing reaches zerolog once the capture is over.
	buf.Reset()
	log.SetOutput(bytes.NewBuffer(nil))
	log.Println("after")
	if buf.Len() != 0 {
		t.Errorf("the capture outlived its scope: %s", buf.String())
	}
	log.SetOutput(before)
}
