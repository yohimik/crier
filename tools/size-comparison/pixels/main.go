package main

import (
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) != 3 {
		panic("use pixels GO_DIR TINYGO_DIR")
	}
	count, failed := 0, false
	err := filepath.WalkDir(os.Args[1], func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".png") {
			return nil
		}
		rel, err := filepath.Rel(os.Args[1], path)
		if err != nil {
			return err
		}
		a, err := os.Open(path)
		if err != nil {
			return err
		}
		defer a.Close()
		b, err := os.Open(filepath.Join(os.Args[2], rel))
		if err != nil {
			return err
		}
		defer b.Close()
		ai, err := png.Decode(a)
		if err != nil {
			return err
		}
		bi, err := png.Decode(b)
		if err != nil {
			return err
		}
		if ai.Bounds() != bi.Bounds() {
			return fmt.Errorf("%s has different dimensions", rel)
		}
		changed, worst := 0, uint32(0)
		loose := 0
		var samples [][3]int
		for y := ai.Bounds().Min.Y; y < ai.Bounds().Max.Y; y++ {
			for x := ai.Bounds().Min.X; x < ai.Bounds().Max.X; x++ {
				ar, ag, ab, aa := ai.At(x, y).RGBA()
				br, bg, bb, ba := bi.At(x, y).RGBA()
				av, bv := []uint32{ar, ag, ab, aa}, []uint32{br, bg, bb, ba}
				different := false
				pixelDelta := uint32(0)
				for i := range av {
					v, w := av[i]>>8, bv[i]>>8
					delta := max(v, w) - min(v, w)
					worst = max(worst, delta)
					pixelDelta = max(pixelDelta, delta)
					different = different || delta != 0
				}
				if different {
					changed++
					if len(samples) < 16 {
						samples = append(samples, [3]int{x, y, int(pixelDelta)})
					}
				}
				if pixelDelta > 2 {
					loose++
				}
			}
		}
		count++
		failed = failed || changed != 0
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"fixture": rel, "width": ai.Bounds().Dx(), "height": ai.Bounds().Dy(), "changed_pixels": changed, "max_channel_delta": worst, "loose_pixels": loose, "samples": samples, "golden_tolerance_pass": worst <= 8 && float64(loose)/float64(ai.Bounds().Dx()*ai.Bounds().Dy()) <= 0.001})
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if count == 0 || failed {
		os.Exit(1)
	}
}
