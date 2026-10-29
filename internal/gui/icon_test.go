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

func TestFailureIconHasRedDot(t *testing.T) {
	img := decode(t, failureIcon())

	bounds := img.Bounds()
	var redPixels int
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			// 红色像素：红通道高，绿蓝通道明显低。
			if r > 0xC000 && g < 0x4000 && b < 0x4000 {
				redPixels++
			}
		}
	}
	if redPixels == 0 {
		t.Fatal("失败图标必须含红色圆点以表示签到失败")
	}
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
