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

// failureIcon 生成带右上角红色圆点的失败图标（用于签到失败时的菜单栏红点）。
// 含红色，不能作为模板图标，应以 SetIcon 设置。
func failureIcon() []byte {
	const size = 18
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	cx, cy := float64(size)/2, float64(size)/2

	// 黑色基底圆。
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			if math.Sqrt(dx*dx+dy*dy) <= 6.5 {
				img.Set(x, y, color.Black)
			}
		}
	}

	// 右上角红色圆点（iOS 系统红）。
	red := color.RGBA{R: 255, G: 59, B: 48, A: 255}
	dotCx, dotCy := float64(size-3), float64(3)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := float64(x)+0.5-dotCx, float64(y)+0.5-dotCy
			if math.Sqrt(dx*dx+dy*dy) <= 3.0 {
				img.Set(x, y, red)
			}
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
