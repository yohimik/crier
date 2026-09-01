package raster

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/benoitkugler/textlayout/fonts"
	"github.com/benoitkugler/webrender/text"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
)

// buildTTC packs several TrueType files into one collection.
//
// A .ttc is a small header pointing at the table directory of each face; the
// faces themselves keep their own directories and share nothing here, which is
// legal and is the simplest collection that exercises face selection.
func buildTTC(t *testing.T, faces ...[]byte) []byte {
	t.Helper()
	header := 12 + 4*len(faces)

	var body bytes.Buffer
	offsets := make([]uint32, len(faces))
	for i, face := range faces {
		offsets[i] = uint32(header + body.Len())
		body.Write(face)
		for body.Len()%4 != 0 {
			body.WriteByte(0)
		}
	}

	var out bytes.Buffer
	out.WriteString("ttcf")
	_ = binary.Write(&out, binary.BigEndian, uint16(1)) // major version
	_ = binary.Write(&out, binary.BigEndian, uint16(0)) // minor version
	_ = binary.Write(&out, binary.BigEndian, uint32(len(faces)))
	for _, off := range offsets {
		_ = binary.Write(&out, binary.BigEndian, off)
	}
	// Each face's table directory offsets are absolute within the file, so the
	// packed copies have to be rewritten to point at where they now live.
	packed := body.Bytes()
	for i, face := range faces {
		base := offsets[i] - uint32(header)
		numTables := binary.BigEndian.Uint16(face[4:6])
		for tbl := 0; tbl < int(numTables); tbl++ {
			at := int(base) + 12 + 16*tbl + 8
			old := binary.BigEndian.Uint32(packed[at : at+4])
			binary.BigEndian.PutUint32(packed[at:at+4], old+offsets[i])
		}
	}
	out.Write(packed)
	return out.Bytes()
}

// TestCollectionFaceIsSelected is the regression test.
//
// Only face 0 was ever parsed, so a layout that chose face 1 of a .ttc had its
// glyph ids looked up in face 0's outlines — the wrong letters, drawn
// confidently, with no error anywhere. macOS and Windows ship most of their
// system fonts as collections, so this was most system text on both.
func TestCollectionFaceIsSelected(t *testing.T) {
	ttc := buildTTC(t, goregular.TTF, gobold.TTF)

	regular, err := loadFace(ttc, text.FontOrigin{File: "test.ttc", Index: 0})
	if err != nil {
		t.Fatalf("face 0: %v", err)
	}
	bold, err := loadFace(ttc, text.FontOrigin{File: "test.ttc", Index: 1})
	if err != nil {
		t.Fatalf("face 1: %v", err)
	}

	// The two faces are different fonts, so they describe themselves
	// differently. If face 1 were silently face 0 these would match.
	regularSummary, err := regular.LoadSummary()
	if err != nil {
		t.Fatal(err)
	}
	boldSummary, err := bold.LoadSummary()
	if err != nil {
		t.Fatal(err)
	}
	if regularSummary.Style == boldSummary.Style {
		t.Errorf("both faces report style %q; face 1 is face 0", regularSummary.Style)
	}
	if !boldSummary.IsBold {
		t.Errorf("face 1 should be the bold one: %+v", boldSummary)
	}
	if regularSummary.IsBold {
		t.Errorf("face 0 should be the regular one: %+v", regularSummary)
	}

	// And their outlines differ, which is what actually reaches the page: the
	// same glyph id drawn from the wrong face is the bug's visible half.
	var differed bool
	for gid := fonts.GID(1); gid < 60 && !differed; gid++ {
		a, aok := regular.GlyphData(gid, 0, 0).(fonts.GlyphOutline)
		b, bok := bold.GlyphData(gid, 0, 0).(fonts.GlyphOutline)
		if !aok || !bok {
			continue
		}
		if len(a.Segments) != len(b.Segments) {
			differed = true
			break
		}
		for i := range a.Segments {
			if a.Segments[i] != b.Segments[i] {
				differed = true
				break
			}
		}
	}
	if !differed {
		t.Error("the two faces produced identical outlines for every glyph id checked")
	}

	// A face nobody has is an error rather than a silent fallback to face 0.
	if _, err := loadFace(ttc, text.FontOrigin{File: "test.ttc", Index: 7}); err == nil {
		t.Error("asking for a face that is not there should fail")
	}

	// A plain single-face file still works, and is face 0.
	if _, err := loadFace(goregular.TTF, text.FontOrigin{File: "go.ttf"}); err != nil {
		t.Errorf("a single-face file should still load: %v", err)
	}
	if _, err := loadFace([]byte("not a font"), text.FontOrigin{File: "x"}); err == nil {
		t.Error("a file that is not a font should fail")
	}
}
