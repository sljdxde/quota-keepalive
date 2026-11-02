package brandicon

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
)

//go:embed assets/app-1024.png
var applicationMaster []byte

//go:embed assets/menu-44.png
var templateMaster []byte

// Application returns the full-color application icon at the requested size.
func Application(size int) []byte {
	return resizePNG(applicationMaster, size)
}

// MasterPNG returns the source application icon used by release packaging.
func MasterPNG() []byte {
	return bytes.Clone(applicationMaster)
}

// Template returns the monochrome menu bar icon. macOS recolors template
// images automatically for light and dark menu bars.
func Template() []byte {
	return bytes.Clone(templateMaster)
}

func resizePNG(data []byte, size int) []byte {
	if size < 1 {
		size = 1
	}
	src := decodeRGBA(data)
	b := src.Bounds()
	if b.Dx() == size && b.Dy() == size {
		return encodePNG(src)
	}

	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	scaleX := float64(b.Dx()) / float64(size)
	scaleY := float64(b.Dy()) / float64(size)

	for y := 0; y < size; y++ {
		y0 := float64(y) * scaleY
		y1 := float64(y+1) * scaleY
		sy0 := max(0, int(math.Floor(y0)))
		sy1 := min(b.Dy(), int(math.Ceil(y1)))
		for x := 0; x < size; x++ {
			x0 := float64(x) * scaleX
			x1 := float64(x+1) * scaleX
			sx0 := max(0, int(math.Floor(x0)))
			sx1 := min(b.Dx(), int(math.Ceil(x1)))

			var r, g, bl, a float64
			for sy := sy0; sy < sy1; sy++ {
				wy := math.Min(float64(sy+1), y1) - math.Max(float64(sy), y0)
				for sx := sx0; sx < sx1; sx++ {
					wx := math.Min(float64(sx+1), x1) - math.Max(float64(sx), x0)
					weight := wx * wy
					c := src.RGBAAt(sx+b.Min.X, sy+b.Min.Y)
					r += float64(c.R) * weight
					g += float64(c.G) * weight
					bl += float64(c.B) * weight
					a += float64(c.A) * weight
				}
			}

			area := (x1 - x0) * (y1 - y0)
			dst.SetRGBA(x, y, color.RGBA{
				R: uint8(math.Round(r / area)),
				G: uint8(math.Round(g / area)),
				B: uint8(math.Round(bl / area)),
				A: uint8(math.Round(a / area)),
			})
		}
	}
	return encodePNG(dst)
}

func decodeRGBA(data []byte) *image.RGBA {
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		panic("invalid embedded brand icon: " + err.Error())
	}
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic("encode brand icon: " + err.Error())
	}
	return buf.Bytes()
}
