package main

import (
	"context"
	"image/png"
	"os"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/render"
)

func main() {
	fonts, err := render.NewFonts(render.FontOptions{Hermetic: true, Logger: zerolog.Nop()})
	if err != nil {
		panic(err)
	}
	img, err := render.RenderOne(context.Background(), render.Options{
		HTML:  `<style>body{font-family:Go;margin:0}div{padding:16px;background:linear-gradient(90deg,#eef,#cce)}</style><div>Crier renders text</div>`,
		Width: 320, Height: 90, Fonts: fonts, Logger: zerolog.Nop(),
	})
	if err != nil {
		panic(err)
	}
	if err := png.Encode(os.Stdout, img); err != nil {
		panic(err)
	}
}
