// mkicon 生成程序图标。
//
// 纯标准库实现，不依赖任何绘图库或外部资源：
//   - 用 4x 超采样（每个像素 4x4 子采样点）自绘，得到抗锯齿边缘
//   - 每个尺寸独立渲染，而不是把大图缩下来，保证 16px 也清晰
//   - 输出多尺寸 ICO（小尺寸内嵌 BMP，128 以上内嵌 PNG）与一张大 PNG
//
// 用法：go run ./tools/mkicon
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
)

// ---------- 配色（改这里就能换整套图标颜色） ----------

var (
	bgTop    = rgb(0x5C, 0x7C, 0xFF) // 背景渐变起点
	bgBottom = rgb(0x25, 0x36, 0xC4) // 背景渐变终点
	ink      = rgb(0x2B, 0x3E, 0xD6) // 气泡内的横线，用比背景略深的蓝做镂空
	paper    = rgb(0xFF, 0xFF, 0xFF) // 气泡主体
)

// 设计网格边长。所有几何都按这个坐标写，再等比缩放到目标尺寸。
const grid = 1024.0

func main() {
	out := "assets"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		log.Fatal(err)
	}

	sizes := []int{16, 20, 24, 32, 40, 48, 64, 128, 256}
	images := make([]*image.NRGBA, len(sizes))
	for i, s := range sizes {
		images[i] = render(s)
	}

	ico := buildICO(sizes, images)
	if err := os.WriteFile(filepath.Join(out, "icon.ico"), ico, 0o644); err != nil {
		log.Fatal(err)
	}

	// 大图：README 与网页用
	for _, s := range []int{256, 512} {
		var buf bytes.Buffer
		if err := png.Encode(&buf, render(s)); err != nil {
			log.Fatal(err)
		}
		name := fmt.Sprintf("icon-%d.png", s)
		if s == 256 {
			name = "icon.png"
		}
		if err := os.WriteFile(filepath.Join(out, name), buf.Bytes(), 0o644); err != nil {
			log.Fatal(err)
		}
	}

	// 网页 favicon：内嵌进界面资源，窗口标题栏显示的就是它
	fav := render(32)
	var fbuf bytes.Buffer
	if err := png.Encode(&fbuf, fav); err != nil {
		log.Fatal(err)
	}
	webDir := filepath.Join("internal", "server", "web")
	if _, err := os.Stat(webDir); err == nil {
		os.WriteFile(filepath.Join(webDir, "favicon.png"), fbuf.Bytes(), 0o644)
	}

	// 多尺寸对照图：把小尺寸等倍放大排一排，方便肉眼检查 16px 有没有糊
	if err := os.MkdirAll("docs", 0o755); err == nil {
		var sheet bytes.Buffer
		if err := png.Encode(&sheet, previewSheet(images[0:7])); err == nil {
			os.WriteFile(filepath.Join("docs", "icon-preview.png"), sheet.Bytes(), 0o644)
		}
	}

	fmt.Printf("图标已生成：%s/icon.ico（%d 个尺寸）\n", out, len(sizes))
}

// previewSheet 把 16~128 各尺寸按整数倍放大后排成一行，用于人工检查
func previewSheet(imgs []*image.NRGBA) *image.NRGBA {
	const cell, pad = 128, 14
	w := pad + len(imgs)*(cell+pad)
	h := cell + pad*2
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	bg := color.NRGBA{0xF4, 0xF6, 0xFB, 0xFF}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out.SetNRGBA(x, y, bg)
		}
	}
	for i, img := range imgs {
		b := img.Bounds().Dx()
		scale := cell / b
		if scale < 1 {
			continue
		}
		side := b * scale
		off := pad + (cell-side)/2
		ox := pad + i*(cell+pad) + (cell-side)/2
		for y := 0; y < side; y++ {
			for x := 0; x < side; x++ {
				out.SetNRGBA(ox+x, off+y, img.NRGBAAt(x/scale, y/scale))
			}
		}
	}
	return out
}

// ---------- 绘制 ----------

// render 画一张 size×size 的图标。
// 小尺寸会主动减细节：16px 下三条横线必然糊成一团，不如只留气泡轮廓。
func render(size int) *image.NRGBA {
	const ss = 4 // 超采样倍数
	w := size * ss
	c := newCanvas(w)

	scale := float64(w) / grid
	p := func(x, y float64) pt { return pt{x * scale, y * scale} }
	r := func(v float64) float64 { return v * scale }

	// 1) 背景：圆角方形 + 上浅下深的线性渐变，左上加一点高光
	c.fillRoundedRectGradient(p(40, 40), p(984, 984), r(228), bgTop, bgBottom)
	c.fillRadial(p(300, 240), r(620), color.NRGBA{255, 255, 255, 26})

	// 2) 气泡主体：圆角矩形 + 左下角尾巴，两者同色，重叠处不会出现接缝
	const bx0, by0, bx1, by1 = 196.0, 268.0, 784.0, 688.0
	c.fillRoundedRect(p(bx0, by0), p(bx1, by1), r(112), paper)
	c.fillPolygon([]pt{p(288, by1-20), p(288, 792), p(428, by1-20)}, paper)

	// 3) 气泡内三条横线：表示一条条会话记录。
	// 左右内边距取同一个值（108），上下也对称，否则整块视觉重心会偏。
	if size >= 32 {
		bar := r(36)
		c.fillCapsule(p(304, 384), p(304+370, 384), bar, ink)
		c.fillCapsule(p(304, 478), p(304+302, 478), bar, ink)
		c.fillCapsule(p(304, 572), p(304+302, 572), bar, ink)
	} else {
		// 极小尺寸只留一条粗线，保证一眼能看出“列表”
		c.fillCapsule(p(314, 478), p(314+296, 478), r(58), ink)
	}

	// 4) 右上角四角星：表示“清理干净、焕然一新”
	if size >= 24 {
		cx, cy := 838.0, 200.0
		outer, inner := 112.0, 38.0
		c.fillPolygon([]pt{
			p(cx, cy-outer), p(cx+inner*0.72, cy-inner*0.72),
			p(cx+outer, cy), p(cx+inner*0.72, cy+inner*0.72),
			p(cx, cy+outer), p(cx-inner*0.72, cy+inner*0.72),
			p(cx-outer, cy), p(cx-inner*0.72, cy-inner*0.72),
		}, paper)
	}

	return c.downsample(ss)
}

// ---------- 画布 ----------

type pt struct{ x, y float64 }

type canvas struct {
	w   int
	buf []float64 // 直通 alpha 的 RGBA，0..1
}

func newCanvas(w int) *canvas {
	return &canvas{w: w, buf: make([]float64, w*w*4)}
}

// blend 把一个颜色以给定覆盖率混合到 (x,y)
func (c *canvas) blend(x, y int, col color.NRGBA, cov float64) {
	if cov <= 0 || x < 0 || y < 0 || x >= c.w || y >= c.w {
		return
	}
	if cov > 1 {
		cov = 1
	}
	a := float64(col.A) / 255 * cov
	if a <= 0 {
		return
	}
	i := (y*c.w + x) * 4
	sr, sg, sb := float64(col.R)/255, float64(col.G)/255, float64(col.B)/255
	dstA := c.buf[i+3]
	outA := a + dstA*(1-a)
	if outA <= 0 {
		return
	}
	c.buf[i] = (sr*a + c.buf[i]*dstA*(1-a)) / outA
	c.buf[i+1] = (sg*a + c.buf[i+1]*dstA*(1-a)) / outA
	c.buf[i+2] = (sb*a + c.buf[i+2]*dstA*(1-a)) / outA
	c.buf[i+3] = outA
}

func (c *canvas) fillRoundedRect(p0, p1 pt, radius float64, col color.NRGBA) {
	c.fillRoundedRectGradient(p0, p1, radius, col, col)
}

// fillRoundedRectGradient 圆角矩形，颜色按 y 做线性渐变
func (c *canvas) fillRoundedRectGradient(p0, p1 pt, radius float64, top, bottom color.NRGBA) {
	x0, y0, x1, y1 := p0.x, p0.y, p1.x, p1.y
	for y := int(math.Floor(y0)); y <= int(math.Ceil(y1)); y++ {
		for x := int(math.Floor(x0)); x <= int(math.Ceil(x1)); x++ {
			if !insideRoundedRect(float64(x)+0.5, float64(y)+0.5, x0, y0, x1, y1, radius) {
				continue
			}
			t := clamp((float64(y)+0.5-y0)/(y1-y0), 0, 1)
			c.blend(x, y, mix(top, bottom, t), 1)
		}
	}
}

// fillRadial 以 (center, radius) 为中心叠加一层柔和高光
func (c *canvas) fillRadial(center pt, radius float64, col color.NRGBA) {
	for y := int(center.y - radius); y <= int(center.y+radius); y++ {
		for x := int(center.x - radius); x <= int(center.x+radius); x++ {
			d := math.Hypot(float64(x)+0.5-center.x, float64(y)+0.5-center.y)
			if d >= radius {
				continue
			}
			t := 1 - d/radius
			c.blend(x, y, col, t*t)
		}
	}
}

// fillCapsule 圆头线段，用于横线
func (c *canvas) fillCapsule(a, b pt, radius float64, col color.NRGBA) {
	minX := math.Min(a.x, b.x) - radius
	maxX := math.Max(a.x, b.x) + radius
	minY := math.Min(a.y, b.y) - radius
	maxY := math.Max(a.y, b.y) + radius
	for y := int(minY); y <= int(maxY); y++ {
		for x := int(minX); x <= int(maxX); x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			if distToSegment(px, py, a.x, a.y, b.x, b.y) <= radius {
				c.blend(x, y, col, 1)
			}
		}
	}
}

// fillPolygon 扫描线填充，用于气泡尾巴和星星
func (c *canvas) fillPolygon(pts []pt, col color.NRGBA) {
	minY, maxY := math.Inf(1), math.Inf(-1)
	minX, maxX := math.Inf(1), math.Inf(-1)
	for _, p := range pts {
		minY = math.Min(minY, p.y)
		maxY = math.Max(maxY, p.y)
		minX = math.Min(minX, p.x)
		maxX = math.Max(maxX, p.x)
	}
	for y := int(minY); y <= int(maxY); y++ {
		for x := int(minX); x <= int(maxX); x++ {
			if pointInPolygon(float64(x)+0.5, float64(y)+0.5, pts) {
				c.blend(x, y, col, 1)
			}
		}
	}
}

// downsample 盒式降采样，把超采样的画布缩到目标尺寸
func (c *canvas) downsample(ss int) *image.NRGBA {
	size := c.w / ss
	out := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b, a float64
			for dy := 0; dy < ss; dy++ {
				for dx := 0; dx < ss; dx++ {
					i := ((y*ss+dy)*c.w + (x*ss + dx)) * 4
					sa := c.buf[i+3]
					r += c.buf[i] * sa
					g += c.buf[i+1] * sa
					b += c.buf[i+2] * sa
					a += sa
				}
			}
			n := float64(ss * ss)
			if a > 0 {
				r, g, b = r/a, g/a, b/a
			}
			out.SetNRGBA(x, y, color.NRGBA{
				R: clamp8(r * 255),
				G: clamp8(g * 255),
				B: clamp8(b * 255),
				A: clamp8(a / n * 255),
			})
		}
	}
	return out
}

// ---------- 几何工具 ----------

func insideRoundedRect(px, py, x0, y0, x1, y1, r float64) bool {
	if px < x0 || px > x1 || py < y0 || py > y1 {
		return false
	}
	cx := clamp(px, x0+r, x1-r)
	cy := clamp(py, y0+r, y1-r)
	dx, dy := px-cx, py-cy
	return dx*dx+dy*dy <= r*r
}

func distToSegment(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	len2 := dx*dx + dy*dy
	if len2 == 0 {
		return math.Hypot(px-ax, py-ay)
	}
	t := clamp(((px-ax)*dx+(py-ay)*dy)/len2, 0, 1)
	return math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
}

func pointInPolygon(px, py float64, pts []pt) bool {
	inside := false
	for i, j := 0, len(pts)-1; i < len(pts); j, i = i, i+1 {
		if (pts[i].y > py) != (pts[j].y > py) &&
			px < (pts[j].x-pts[i].x)*(py-pts[i].y)/(pts[j].y-pts[i].y)+pts[i].x {
			inside = !inside
		}
	}
	return inside
}

// ---------- ICO 封装 ----------

// buildICO 按 Windows 规范拼 ICO：
// 16~64 用 BMP 位图（兼容性最好），128 以上用 PNG（体积小）。
func buildICO(sizes []int, imgs []*image.NRGBA) []byte {
	type entry struct {
		size int
		data []byte
	}
	entries := make([]entry, len(sizes))
	for i, s := range sizes {
		if s >= 128 {
			var buf bytes.Buffer
			png.Encode(&buf, imgs[i])
			entries[i] = entry{s, buf.Bytes()}
		} else {
			entries[i] = entry{s, bmpPayload(imgs[i])}
		}
	}

	var out bytes.Buffer
	// ICONDIR
	binary.Write(&out, binary.LittleEndian, uint16(0))
	binary.Write(&out, binary.LittleEndian, uint16(1)) // 1 = 图标
	binary.Write(&out, binary.LittleEndian, uint16(len(entries)))

	offset := 6 + 16*len(entries)
	for _, e := range entries {
		d := byte(e.size)
		if e.size >= 256 {
			d = 0 // 0 代表 256
		}
		out.WriteByte(d)                                    // 宽
		out.WriteByte(d)                                    // 高
		out.WriteByte(0)                                    // 调色板数
		out.WriteByte(0)                                    // 保留
		binary.Write(&out, binary.LittleEndian, uint16(1))  // 平面数
		binary.Write(&out, binary.LittleEndian, uint16(32)) // 位深
		binary.Write(&out, binary.LittleEndian, uint32(len(e.data)))
		binary.Write(&out, binary.LittleEndian, uint32(offset))
		offset += len(e.data)
	}
	for _, e := range entries {
		out.Write(e.data)
	}
	return out.Bytes()
}

// bmpPayload 生成 ICO 内嵌的 32 位 BMP：BITMAPINFOHEADER + BGRA 自下而上 + AND 掩码
func bmpPayload(img *image.NRGBA) []byte {
	w := img.Bounds().Dx()
	h := img.Bounds().Dy()

	var out bytes.Buffer
	// BITMAPINFOHEADER，高度写两倍：XOR 位图 + AND 掩码
	binary.Write(&out, binary.LittleEndian, uint32(40))
	binary.Write(&out, binary.LittleEndian, int32(w))
	binary.Write(&out, binary.LittleEndian, int32(h*2))
	binary.Write(&out, binary.LittleEndian, uint16(1))
	binary.Write(&out, binary.LittleEndian, uint16(32))
	binary.Write(&out, binary.LittleEndian, uint32(0)) // BI_RGB
	binary.Write(&out, binary.LittleEndian, uint32(w*h*4))
	binary.Write(&out, binary.LittleEndian, int32(0))
	binary.Write(&out, binary.LittleEndian, int32(0))
	binary.Write(&out, binary.LittleEndian, uint32(0))
	binary.Write(&out, binary.LittleEndian, uint32(0))

	// 像素：BT.709 感知的直通 alpha，行序自下而上
	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			c := img.NRGBAAt(x, y)
			out.WriteByte(c.B)
			out.WriteByte(c.G)
			out.WriteByte(c.R)
			out.WriteByte(c.A)
		}
	}
	// AND 掩码：32 位图标用 alpha 通道即可，这里全 0，但仍需按行补齐到 4 字节
	rowBytes := ((w + 31) / 32) * 4
	out.Write(make([]byte, rowBytes*h))
	return out.Bytes()
}

// ---------- 小工具 ----------

func rgb(r, g, b uint8) color.NRGBA { return color.NRGBA{r, g, b, 255} }

func mix(a, b color.NRGBA, t float64) color.NRGBA {
	return color.NRGBA{
		R: clamp8(float64(a.R) + (float64(b.R)-float64(a.R))*t),
		G: clamp8(float64(a.G) + (float64(b.G)-float64(a.G))*t),
		B: clamp8(float64(a.B) + (float64(b.B)-float64(a.B))*t),
		A: clamp8(float64(a.A) + (float64(b.A)-float64(a.A))*t),
	}
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func clamp8(v float64) uint8 { return uint8(math.Round(clamp(v, 0, 255))) }
