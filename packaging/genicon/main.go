// Command genicon 生成 QKA 的 App 图标主图（1024×1024）：
// 保活绿圆角底 + 白色心电线，与 002设计内容/logo/qka-logo-final.svg 一致。
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

const (
	size = 1024
	// 64 空间中的圆角矩形参数（x=4,y=4,w=56,h=56,rx=14）。
	rectX, rectY, rectW, rectH, radius = 4.0, 4.0, 56.0, 56.0, 14.0
)

var ecgRaw = [][2]float64{
	{12, 32}, {21, 32}, {27, 15}, {35, 47}, {41, 32}, {52, 32},
}

type pt struct{ x, y float64 }

func distToSeg(p, a, b pt) float64 {
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

func distToPoly(p pt, pts []pt) float64 {
	best := math.Inf(1)
	for i := 0; i+1 < len(pts); i++ {
		if d := distToSeg(p, pts[i], pts[i+1]); d < best {
			best = d
		}
	}
	return best
}

// insideRoundedRect 判定 64 空间点 p 是否在圆角矩形内。
func insideRoundedRect(px, py float64) bool {
	if px < rectX || px > rectX+rectW || py < rectY || py > rectY+rectH {
		return false
	}
	// 仅当同时靠近一条水平边与一条垂直边（真正的角部）时才做圆测试；
	// 只靠近一条边（直边区域）或内部区域都在矩形内。
	var cornerX, cornerY float64
	nearX, nearY := false, false
	if px < rectX+radius {
		cornerX, nearX = rectX+radius, true
	} else if px > rectX+rectW-radius {
		cornerX, nearX = rectX+rectW-radius, true
	}
	if py < rectY+radius {
		cornerY, nearY = rectY+radius, true
	} else if py > rectY+rectH-radius {
		cornerY, nearY = rectY+rectH-radius, true
	}
	if nearX && nearY {
		dx, dy := px-cornerX, py-cornerY
		return dx*dx+dy*dy <= radius*radius
	}
	return true
}

func main() {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	green := color.RGBA{R: 14, G: 163, B: 113, A: 255}
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}

	// 心电折线（64 空间，stroke-width=6）。
	pts := make([]pt, len(ecgRaw))
	for i, r := range ecgRaw {
		pts[i] = pt{x: r[0], y: r[1]}
	}
	half := 6.0 / 2.0

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			// 像素中心映射回 64 空间。
			p64 := pt{
				x: (float64(x) + 0.5) * 64.0 / size,
				y: (float64(y) + 0.5) * 64.0 / size,
			}
			if !insideRoundedRect(p64.x, p64.y) {
				continue
			}
			c := green
			if distToPoly(p64, pts) <= half {
				c = white
			}
			img.Set(x, y, c)
		}
	}

	out := "packaging/appicon_1024.png"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	f, err := os.Create(out)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}
