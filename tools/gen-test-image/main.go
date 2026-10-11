// gen-test-image writes the built-in test image of `toktape --image builtin`
// (TTP-208): internal/server/images/testimage.png.
//
// The picture is made to have a checkable answer. Three solid shapes in
// distinct colours stand at known places, and one line of text names the
// size, so "describe this image" can be judged by looking at the reply:
//
//	red circle     top left      centre (130,120) radius 70
//	green triangle centre        apex (320,170), base (250,310)-(390,310)
//	blue square    bottom right  (440,300)-(580,440)
//	text           bottom left   "TOKTAPE TEST 640x480" in JetBrains Mono
//
// It is deterministic: no clock, no random numbers, integer geometry, the
// repo's own font. A test (main_test.go) runs it again and compares the bytes
// with the committed file, so the image cannot drift from this program.
//
//	go run ./tools/gen-test-image -o internal/server/images/testimage.png
package main

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"os"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"github.com/midagedev/toktape/assets/fonts"
)

const (
	width  = 640
	height = 480
	label  = "TOKTAPE TEST 640x480"
)

var (
	background = color.RGBA{R: 245, G: 243, B: 236, A: 255}
	red        = color.RGBA{R: 220, G: 38, B: 38, A: 255}
	green      = color.RGBA{R: 22, G: 163, B: 74, A: 255}
	blue       = color.RGBA{R: 37, G: 99, B: 235, A: 255}
	ink        = color.RGBA{R: 31, G: 41, B: 55, A: 255}
)

// Render draws the image.
func Render() (*image.RGBA, error) {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), image.NewUniform(background), image.Point{}, draw.Src)

	// Red circle, top left.
	const cx, cy, r = 130, 120, 70
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			if (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r {
				img.SetRGBA(x, y, red)
			}
		}
	}

	// Blue square, bottom right.
	draw.Draw(img, image.Rect(440, 300, 580, 440), image.NewUniform(blue), image.Point{}, draw.Src)

	// Green triangle, centre: apex (320,170), base y=310 from x=250 to 390.
	// Row y spans 70*(y-170)/140 either side of x=320, in integers.
	for y := 170; y <= 310; y++ {
		half := 70 * (y - 170) / 140
		for x := 320 - half; x <= 320+half; x++ {
			img.SetRGBA(x, y, green)
		}
	}

	// One line of text, bottom left.
	f, err := opentype.Parse(fonts.JetBrainsMonoRegular)
	if err != nil {
		return nil, fmt.Errorf("parse font: %w", err)
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: 28, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, fmt.Errorf("font face: %w", err)
	}
	defer face.Close()
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(ink),
		Face: face,
		Dot:  fixed.P(30, 460),
	}
	d.DrawString(label)
	return img, nil
}

// Encode is the PNG as committed: best compression, no extras.
func Encode() ([]byte, error) {
	img, err := Render()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("gen-test-image", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "internal/server/images/testimage.png", "where to write the PNG")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	b, err := Encode()
	if err != nil {
		fmt.Fprintf(stderr, "gen-test-image: %v\n", err)
		return 1
	}
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		fmt.Fprintf(stderr, "gen-test-image: %v\n", err)
		return 1
	}
	return 0
}

func main() { os.Exit(run(os.Args[1:], os.Stderr)) }
