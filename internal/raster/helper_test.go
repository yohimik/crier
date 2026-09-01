package raster

import (
	"github.com/benoitkugler/webrender/backend"
	"github.com/benoitkugler/webrender/text"
)

// fakeFont is a backend.Font that names itself and nothing else. It is what the
// font-cache tests register, since AddFont only ever keys on the value.
type fakeFont struct{ file string }

func (f fakeFont) Origin() text.FontOrigin { return text.FontOrigin{File: f.file} }

func (f fakeFont) Description() backend.FontDescription {
	return backend.FontDescription{Family: "Fake", Size: 10}
}

// foreignCanvas is a backend.Canvas from another implementation, which the
// group and pattern methods have to refuse rather than assert their way into a
// panic.
type foreignCanvas struct{}

func (foreignCanvas) GetBoundingBox() (backend.Fl, backend.Fl, backend.Fl, backend.Fl) {
	return 0, 0, 0, 0
}
func (foreignCanvas) SetBoundingBox(_, _, _, _ backend.Fl)                        {}
func (foreignCanvas) OnNewStack(f func())                                         { f() }
func (foreignCanvas) State() backend.GraphicState                                 { return nil }
func (foreignCanvas) NewGroup(_, _, _, _ backend.Fl) backend.Canvas               { return nil }
func (foreignCanvas) DrawWithOpacity(backend.Fl, backend.Canvas)                  {}
func (foreignCanvas) Paint(backend.PaintOp)                                       {}
func (foreignCanvas) Rectangle(_, _, _, _ backend.Fl)                             {}
func (foreignCanvas) MoveTo(_, _ backend.Fl)                                      {}
func (foreignCanvas) LineTo(_, _ backend.Fl)                                      {}
func (foreignCanvas) CubicTo(_, _, _, _, _, _ backend.Fl)                         {}
func (foreignCanvas) ClosePath()                                                  {}
func (foreignCanvas) AddFont(backend.Font, []byte) *backend.FontChars             { return nil }
func (foreignCanvas) DrawText([]backend.TextDrawing)                              {}
func (foreignCanvas) DrawRasterImage(backend.RasterImage, backend.Fl, backend.Fl) {}
func (foreignCanvas) DrawGradient(backend.GradientLayout, backend.Fl, backend.Fl) {}
