package gui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// ecgRaw 是品牌心电线（qka-logo-template.svg）在 64×64 viewBox 中的折线顶点。
var ecgRaw = [][2]float64{
	{12, 32}, {21, 32}, {27, 15}, {35, 47}, {41, 32}, {52, 32},
}

// ecgStroke 是折线在 viewBox 中的描边宽度（与 logo 的 stroke-width=6 一致）。
const ecgStroke = 6.0

// f64 是一个二维点。
type f64 struct{ x, y float64 }

// distToSeg 返回点 p 到线段 ab 的最短距离。
func distToSeg(p, a, b f64) float64 {
	vx, vy := b.x-a.x, b.y-a.y
	wx, wy := p.x-a.x, p.y-a.y
	l2 := vx*vx + vy*vy
	t := 0.0
	if l2 > 0 {
		t = (wx*vx + wy*vy) / l2
		if t < 0 {
			t = 0
		} else if t > 1 {
			t = 1
		}
	}
	dx, dy := a.x+t*vx-p.x, a.y+t*vy-p.y
	return math.Sqrt(dx*dx + dy*dy)
}

// distToPolyline 返回点 p 到折线的最短距离。
func distToPolyline(p f64, pts []f64) float64 {
	best := math.Inf(1)
	for i := 0; i+1 < len(pts); i++ {
		if d := distToSeg(p, pts[i], pts[i+1]); d < best {
			best = d
		}
	}
	return best
}

// ecgPoints 把 64 空间的心电顶点缩放到目标尺寸。
func ecgPoints(size int) []f64 {
	scale := float64(size) / 64.0
	pts := make([]f64, len(ecgRaw))
	for i, r := range ecgRaw {
		pts[i] = f64{x: r[0] * scale, y: r[1] * scale}
	}
	return pts
}

// strokeECG 在 img 上以给定颜色描出心电线（圆头/圆连接，跟随描边宽度）。
func strokeECG(img *image.RGBA, c color.Color) {
	b := img.Bounds()
	size := b.Dx()
	pts := ecgPoints(size)
	half := (ecgStroke * float64(size) / 64.0) / 2.0

	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			p := f64{x: float64(x) + 0.5, y: float64(y) + 0.5}
			if distToPolyline(p, pts) <= half {
				img.Set(x, y, c)
			}
		}
	}
}

// fillDot 在 img 上以 c 填充圆心（cx,cy）、半径 r 的实心圆。
func fillDot(img *image.RGBA, cx, cy, r float64, c color.Color) {
	b := img.Bounds()
	r2 := r * r
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			if dx*dx+dy*dy <= r2 {
				img.Set(x, y, c)
			}
		}
	}
}

// templateIcon 生成单色心电线模板图标（黑色），系统在深浅色菜单栏自动按 alpha 着色。
func templateIcon() []byte {
	const size = 22
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	strokeECG(img, color.Black)
	return encodePNG(img)
}

// failureIcon 生成带右上角红色圆点的失败图标（签到失败时菜单栏红点）。
// 含红色，不能作为模板图标，应以 SetIcon 设置。
func failureIcon() []byte {
	const size = 22
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	strokeECG(img, color.Black)
	// 右上角红色圆点（iOS 系统红）。
	fillDot(img, float64(size)-4.5, 4.5, 3.5, color.RGBA{R: 255, G: 59, B: 48, A: 255})
	return encodePNG(img)
}

func encodePNG(img *image.RGBA) []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
