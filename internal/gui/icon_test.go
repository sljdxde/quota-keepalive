package gui

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func decode(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestTemplateIconHasNoRed(t *testing.T) {
	img := decode(t, templateIcon())
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r != g || g != b {
				t.Fatal("模板图标应为单色，不含彩色（红）像素")
			}
		}
	}
}
