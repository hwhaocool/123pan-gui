// Command genicon 生成 123 云盘客户端的多尺寸应用图标 .ico（纯标准库，无依赖）。
//
// 用法（在仓库根目录执行）：
//
//	go run ./tools/genicon -o app.ico -pngout internal/ui/appicon.png
//	go run ./tools/genicon -o app.ico -preview ./.icon-preview   # 额外输出各尺寸 PNG 便于检查
//
// 输出 16/20/24/32/40/48/64/128/256 九个尺寸；<=64px 用 32 位 DIB（兼容性最好），
// 更大尺寸用 PNG（Vista+ 支持），可直接交给 rsrc / goversioninfo / windres 打包进 exe。
//
// 图形：蓝紫渐变圆角底 + 白色云朵；48px 以上右下角加琥珀色圆形徽标（向上箭头）。
// 小尺寸（<=32px）只画云并放大到接近满版，避免细节糊成一团。
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const ss = 4 // 超采样倍数（抗锯齿）

var (
	colBGTop    color.NRGBA
	colBGBottom color.NRGBA
	colCloud    = color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}
	colBadge    = color.NRGBA{0xFF, 0xB0, 0x3A, 0xFF}
	colArrow    = color.NRGBA{0x1E, 0x2A, 0x5A, 0xFF}
)

// ---------------- 画布 ----------------

type canvas struct {
	w, h int
	pix  []color.NRGBA
}

func newCanvas(w, h int) *canvas { return &canvas{w: w, h: h, pix: make([]color.NRGBA, w*h)} }

// over 按 source-over 合成（straight alpha）。
func (c *canvas) over(x, y int, s color.NRGBA) {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return
	}
	i := y*c.w + x
	d := c.pix[i]

	sa := float64(s.A) / 255
	da := float64(d.A) / 255
	oa := sa + da*(1-sa)
	if oa <= 0 {
		c.pix[i] = color.NRGBA{}
		return
	}
	mix := func(sc, dc uint8) uint8 {
		return uint8(math.Round((float64(sc)*sa + float64(dc)*da*(1-sa)) / oa))
	}
	c.pix[i] = color.NRGBA{mix(s.R, d.R), mix(s.G, d.G), mix(s.B, d.B), uint8(math.Round(oa * 255))}
}

// insideRoundRect 判断像素中心是否落在圆角矩形内。
func insideRoundRect(px, py, x0, y0, x1, y1, r float64) bool {
	cx, cy := (x0+x1)/2, (y0+y1)/2
	hx, hy := (x1-x0)/2, (y1-y0)/2
	if r > math.Min(hx, hy) {
		r = math.Min(hx, hy)
	}
	qx := math.Abs(px-cx) - (hx - r)
	qy := math.Abs(py-cy) - (hy - r)
	if qx < 0 {
		qx = 0
	}
	if qy < 0 {
		qy = 0
	}
	return qx*qx+qy*qy <= r*r
}

func (c *canvas) fillRoundRect(x0, y0, x1, y1, r float64, col color.NRGBA) {
	for y := int(math.Floor(y0)); y <= int(math.Ceil(y1)); y++ {
		for x := int(math.Floor(x0)); x <= int(math.Ceil(x1)); x++ {
			if insideRoundRect(float64(x)+0.5, float64(y)+0.5, x0, y0, x1, y1, r) {
				c.over(x, y, col)
			}
		}
	}
}

func (c *canvas) fillRoundRectGradient(x0, y0, x1, y1, r float64) {
	for y := int(math.Floor(y0)); y <= int(math.Ceil(y1)); y++ {
		py := float64(y) + 0.5
		for x := int(math.Floor(x0)); x <= int(math.Ceil(x1)); x++ {
			px := float64(x) + 0.5
			if !insideRoundRect(px, py, x0, y0, x1, y1, r) {
				continue
			}
			t := ((px - x0) + (py - y0)) / ((x1 - x0) + (y1 - y0))
			t = math.Max(0, math.Min(1, t))
			c.over(x, y, lerpColor(colBGTop, colBGBottom, t))
		}
	}
}

func (c *canvas) fillCircle(cx, cy, r float64, col color.NRGBA) {
	for y := int(math.Floor(cy - r)); y <= int(math.Ceil(cy+r)); y++ {
		py := float64(y) + 0.5
		for x := int(math.Floor(cx - r)); x <= int(math.Ceil(cx+r)); x++ {
			px := float64(x) + 0.5
			dx, dy := px-cx, py-cy
			if dx*dx+dy*dy <= r*r {
				c.over(x, y, col)
			}
		}
	}
}

type pt struct{ x, y float64 }

func (c *canvas) fillTriangle(a, b, d pt, col color.NRGBA) {
	minX := int(math.Floor(math.Min(a.x, math.Min(b.x, d.x))))
	maxX := int(math.Ceil(math.Max(a.x, math.Max(b.x, d.x))))
	minY := int(math.Floor(math.Min(a.y, math.Min(b.y, d.y))))
	maxY := int(math.Ceil(math.Max(a.y, math.Max(b.y, d.y))))

	sign := func(p1, p2, p3 pt) float64 {
		return (p1.x-p3.x)*(p2.y-p3.y) - (p2.x-p3.x)*(p1.y-p3.y)
	}

	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			p := pt{float64(x) + 0.5, float64(y) + 0.5}
			d1, d2, d3 := sign(p, a, b), sign(p, b, d), sign(p, d, a)
			if !((d1 < 0 || d2 < 0 || d3 < 0) && (d1 > 0 || d2 > 0 || d3 > 0)) {
				c.over(x, y, col)
			}
		}
	}
}

func lerpColor(a, b color.NRGBA, t float64) color.NRGBA {
	l := func(x, y uint8) uint8 { return uint8(math.Round(float64(x) + (float64(y)-float64(x))*t)) }
	return color.NRGBA{l(a.R, b.R), l(a.G, b.G), l(a.B, b.B), l(a.A, b.A)}
}

// drawCloud 在 (bx0,by0)-(bx1,by1) 的框内画一朵云：底部圆角横条 + 三个圆。
func drawCloud(c *canvas, bx0, by0, bx1, by1 float64) {
	w, h := bx1-bx0, by1-by0
	pillTop := by1 - 0.52*h
	c.fillRoundRect(bx0, pillTop, bx1, by1, 0.26*h, colCloud)
	c.fillCircle(bx0+0.24*w, pillTop, 0.30*h, colCloud)
	c.fillCircle(bx0+0.50*w, pillTop-0.10*h, 0.36*h, colCloud)
	c.fillCircle(bx0+0.78*w, pillTop+0.02*h, 0.26*h, colCloud)
}

// drawBadge 画右下角的圆形徽标：琥珀底 + 向上的箭头。
func drawBadge(c *canvas, u, bx, by, r float64) {
	c.fillCircle(bx, by, r, colBadge)

	// 箭头：上方三角 + 下方圆柱
	head := 0.62 * r
	stemW := 0.34 * r
	stemTop := by - 0.10*r
	stemBottom := by + 0.52*r
	c.fillTriangle(
		pt{bx, by - head},
		pt{bx - head*0.92, by - head + head*1.02},
		pt{bx + head*0.92, by - head + head*1.02},
		colArrow,
	)
	c.fillRoundRect(bx-stemW/2, stemTop, bx+stemW/2, stemBottom, stemW/2, colArrow)
}

// draw 绘制 n×n 的（已超采样的）画布。
func draw(n int) *canvas {
	c := newCanvas(n, n)
	u := float64(n)
	c.fillRoundRectGradient(0, 0, u, u, 0.22*u)

	size := n / ss // 最终像素尺寸：据此决定细节多少
	if size <= 32 {
		// 16/20/24/32px：只画云，放大到接近满版；细节在这么小的尺寸会糊。
		drawCloud(c, 0.10*u, 0.30*u, 0.90*u, 0.72*u)
		return c
	}

	// 40px 起：云 + 右下角徽标（云盘 / 同步）。
	drawCloud(c, 0.09*u, 0.26*u, 0.79*u, 0.66*u)
	drawBadge(c, u, 0.775*u, 0.765*u, 0.155*u)
	return c
}

// downsample 按 ss×ss 块做 alpha 加权平均，得到带抗锯齿的最终像素。
func downsample(src *canvas, f int) *image.NRGBA {
	w, h := src.w/f, src.h/f
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var sr, sg, sb, sa float64
			for dy := 0; dy < f; dy++ {
				for dx := 0; dx < f; dx++ {
					p := src.pix[(y*f+dy)*src.w+(x*f+dx)]
					a := float64(p.A)
					sr += float64(p.R) * a
					sg += float64(p.G) * a
					sb += float64(p.B) * a
					sa += a
				}
			}
			if sa == 0 {
				continue
			}
			n := float64(f * f)
			dst.SetNRGBA(x, y, color.NRGBA{
				R: uint8(math.Round(sr / sa)),
				G: uint8(math.Round(sg / sa)),
				B: uint8(math.Round(sb / sa)),
				A: uint8(math.Round(sa / n)),
			})
		}
	}
	return dst
}

func render(size int) *image.NRGBA { return downsample(draw(size*ss), ss) }

// ---------------- ICO 编码 ----------------

// encodeICO 输出 ICO：<=64px 用 32 位 DIB，更大尺寸用 PNG。
func encodeICO(imgs map[int]*image.NRGBA, sizes []int) []byte {
	sorted := append([]int(nil), sizes...)
	sort.Ints(sorted)

	type entry struct {
		size int
		data []byte
	}

	entries := make([]entry, 0, len(sorted))
	for _, s := range sorted {
		var data []byte
		if s > 64 {
			var buf bytes.Buffer
			if err := png.Encode(&buf, imgs[s]); err != nil {
				log.Fatal(err)
			}
			data = buf.Bytes()
		} else {
			data = encodeDIB(imgs[s])
		}
		entries = append(entries, entry{size: s, data: data})
	}

	var out bytes.Buffer
	le := binary.LittleEndian
	_ = binary.Write(&out, le, uint16(0)) // reserved
	_ = binary.Write(&out, le, uint16(1)) // type = icon
	_ = binary.Write(&out, le, uint16(len(entries)))

	offset := 6 + 16*len(entries)
	for _, e := range entries {
		w := byte(e.size)
		if e.size >= 256 {
			w = 0 // 256 在 ICO 目录里记作 0
		}
		out.WriteByte(w)                       // width
		out.WriteByte(w)                       // height
		out.WriteByte(0)                       // 调色板颜色数
		out.WriteByte(0)                       // reserved
		_ = binary.Write(&out, le, uint16(1))  // planes
		_ = binary.Write(&out, le, uint16(32)) // bpp
		_ = binary.Write(&out, le, uint32(len(e.data)))
		_ = binary.Write(&out, le, uint32(offset))
		offset += len(e.data)
	}
	for _, e := range entries {
		out.Write(e.data)
	}
	return out.Bytes()
}

// encodeDIB 生成 ICO 用的 32 位 DIB：BITMAPINFOHEADER + 自下而上 BGRA + AND 掩码。
// 注意：biHeight 必须写 2 倍高度（XOR 位图 + AND 掩码），AND 掩码每行按 4 字节对齐。
func encodeDIB(img *image.NRGBA) []byte {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	var out bytes.Buffer
	le := binary.LittleEndian

	_ = binary.Write(&out, le, uint32(40))    // biSize
	_ = binary.Write(&out, le, int32(w))      // biWidth
	_ = binary.Write(&out, le, int32(h*2))    // biHeight
	_ = binary.Write(&out, le, uint16(1))     // biPlanes
	_ = binary.Write(&out, le, uint16(32))    // biBitCount
	_ = binary.Write(&out, le, uint32(0))     // biCompression = BI_RGB
	_ = binary.Write(&out, le, uint32(w*h*4)) // biSizeImage
	_ = binary.Write(&out, le, int32(0))      // xppm
	_ = binary.Write(&out, le, int32(0))      // yppm
	_ = binary.Write(&out, le, uint32(0))     // clrUsed
	_ = binary.Write(&out, le, uint32(0))     // clrImportant

	for y := h - 1; y >= 0; y-- { // 自下而上，BGRA
		for x := 0; x < w; x++ {
			c := img.NRGBAAt(x, y)
			out.Write([]byte{c.B, c.G, c.R, c.A})
		}
	}

	row := make([]byte, ((w+31)/32)*4) // AND 掩码：全 0（不透明）
	for y := 0; y < h; y++ {
		out.Write(row)
	}
	return out.Bytes()
}

// ---------------- main ----------------

func parseHex(s string, def color.NRGBA) color.NRGBA {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "#"))
	if len(s) != 6 {
		return def
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return def
	}
	return color.NRGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xFF}
}

func writePNG(path string, img *image.NRGBA) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		log.Fatal(err)
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
}

func main() {
	out := flag.String("o", "app.ico", "输出的 .ico 文件")
	pngOut := flag.String("pngout", "", "可选：额外输出一张 256px PNG（用于运行时设置窗口图标）")
	preview := flag.String("preview", "", "可选：把各尺寸 PNG 输出到该目录（用于肉眼检查小尺寸）")
	c1 := flag.String("c1", "#3FA2FF", "圆角底渐变的起始色（#RRGGBB）")
	c2 := flag.String("c2", "#2B49D8", "圆角底渐变的结束色（#RRGGBB）")
	sizesFlag := flag.String("sizes", "16,20,24,32,40,48,64,128,256", "逗号分隔的尺寸列表")
	flag.Parse()

	colBGTop = parseHex(*c1, colBGTop)
	colBGBottom = parseHex(*c2, colBGBottom)

	var sizes []int
	for _, f := range strings.Split(*sizesFlag, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || n < 8 || n > 256 {
			log.Fatalf("非法尺寸: %q", f)
		}
		sizes = append(sizes, n)
	}

	imgs := make(map[int]*image.NRGBA, len(sizes))
	for _, s := range sizes {
		imgs[s] = render(s)
		if *preview != "" {
			writePNG(filepath.Join(*preview, fmt.Sprintf("icon-%d.png", s)), imgs[s])
		}
	}

	if err := os.WriteFile(*out, encodeICO(imgs, sizes), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("已生成 %s（尺寸 %v）\n", *out, sizes)

	if *pngOut != "" {
		biggest := 0
		for _, s := range sizes {
			if s > biggest {
				biggest = s
			}
		}
		writePNG(*pngOut, imgs[biggest])
		fmt.Printf("已生成 %s（%dpx PNG，用于窗口图标）\n", *pngOut, biggest)
	}
	if *preview != "" {
		fmt.Printf("预览 PNG 已输出到 %s，请用看图工具确认 16/24/32px 是否仍可辨识\n", *preview)
	}
}
