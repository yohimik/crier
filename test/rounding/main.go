//go:build rounding

// This executable checks the release dependency with either compiler, before
// the slower CLI integration run. It deliberately asserts the rounded origin,
// not later coordinates where the Go specification permits other contractions.
package main

import (
	"fmt"
	"math"

	pr "github.com/benoitkugler/webrender/css/properties"
	"github.com/benoitkugler/webrender/images"
)

func main() {
	for _, tc := range []struct {
		width, height pr.Float
		x, y          uint32
	}{
		{1920, 1080, 0xc20d7b40, 0x4297b400},
		{1080, 1920, 0xc387a086, 0x44116d34},
	} {
		stops := make(pr.ColorsStops, 4)
		for i, pos := range []pr.Float{0, 3, 3, 22} {
			stops[i].Position = pr.NewDim(pos, pr.Px)
		}
		g := images.NewLinearGradient(pr.LinearGradient{
			Direction:  pr.DirectionType{Angle: float32(115 * math.Pi / 180)},
			ColorStops: stops, Repeating: true,
		}).Layout(tc.width, tc.height)
		x, y := math.Float32bits(g.Coords[0]), math.Float32bits(g.Coords[1])
		if x != tc.x || y != tc.y {
			panic(fmt.Sprintf("%gx%g origin=(%08x,%08x), want (%08x,%08x)", tc.width, tc.height, x, y, tc.x, tc.y))
		}
		fmt.Printf("%gx%g rounded origin=(%08x,%08x): PASS\n", tc.width, tc.height, x, y)
	}
}
