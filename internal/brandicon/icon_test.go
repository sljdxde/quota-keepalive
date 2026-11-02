package brandicon

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestApplicationIconScalesToRequestedSize(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(Application(64)))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 64 || img.Bounds().Dy() != 64 {
		t.Fatalf("应用图标尺寸 = %v，期望 64×64", img.Bounds())
	}
}

func TestTemplateIconIsMonochrome(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(Template()))
	if err != nil {
		t.Fatal(err)
	}
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r != g || g != b {
				t.Fatalf("模板图标 (%d,%d) 不是单色", x, y)
			}
		}
	}
}

func TestTemplateIconKeepsOpticalMargin(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(Template()))
	if err != nil {
		t.Fatal(err)
	}
	bbox := image.Rectangle{}
	seen := false
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			if a == 0 {
				continue
			}
			point := image.Rect(x, y, x+1, y+1)
			if !seen {
				bbox = point
				seen = true
				continue
			}
			bbox = bbox.Union(point)
		}
	}
	if !seen {
		t.Fatal("菜单栏模板图标为空")
	}
	// The glyph must read as a Q in the menu bar (about 16-18pt at 2x), not
	// collapse into a dot, while still keeping an optical margin on all sides.
	if bbox.Dx() < 28 || bbox.Dy() < 26 || bbox.Dx() > 36 || bbox.Dy() > 36 {
		t.Fatalf("菜单栏 Q 尺寸应在 28-36px 区间，实际 bbox=%v", bbox)
	}
	if bbox.Min.X < 4 || bbox.Min.Y < 4 || bbox.Max.X > 40 || bbox.Max.Y > 40 {
		t.Fatalf("菜单栏 Q 应保留光学边距，实际 bbox=%v", bbox)
	}
}

func TestApplicationIconHasVisibleOuterRim(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(Application(64)))
	if err != nil {
		t.Fatal(err)
	}

	rim := color.RGBAModel.Convert(img.At(4, 32)).(color.RGBA)
	interior := color.RGBAModel.Convert(img.At(7, 32)).(color.RGBA)
	rimLuma := luminance(rim)
	interiorLuma := luminance(interior)
	if rimLuma < 95 || rimLuma < interiorLuma+30 {
		t.Fatalf("应用图标外缘不够清晰：rim=%.1f interior=%.1f", rimLuma, interiorLuma)
	}
}

func luminance(c color.RGBA) float64 {
	return 0.2126*float64(c.R) + 0.7152*float64(c.G) + 0.0722*float64(c.B)
}
