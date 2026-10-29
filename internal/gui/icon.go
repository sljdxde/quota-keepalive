package gui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// templateIcon 生成一个单色模板图标（黑色），系统在深浅色菜单栏自动按 alpha 着色。
func templateIcon() []byte {
	const size = 18
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	cx, cy := float64(size)/2, float64(size)/2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			if math.Sqrt(dx*dx+dy*dy) <= 6.5 {
				img.Set(x, y, color.Black)
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
