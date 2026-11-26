package uiapp

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"
	"math"

	"github.com/LJMStark/Colab-Tools/assets"
	"github.com/LJMStark/Colab-Tools/internal/core"
	"github.com/egoist/mygo/ui"
)

var appIconBitmap *ui.Bitmap

func init() {
	if img, err := png.Decode(bytes.NewReader(assets.AppIconPNG)); err == nil {
		appIconBitmap = ui.NewBitmap(img)
	}
}

// TrayIconPNG generates a crisp 36x36 monochrome template PNG for macOS menu bar & Windows notification tray.
func TrayIconPNG(running bool) []byte {
	const sz = 36
	img := image.NewRGBA(image.Rect(0, 0, sz, sz))
	// Draw clean infinity/compute rings icon (template black on transparent)
	draw.Draw(img, img.Bounds(), image.Transparent, image.Point{}, draw.Src)
	drawRing := func(cx, cy, rOuter, rInner float64, alpha uint8) {
		for y := 0; y < sz; y++ {
			for x := 0; x < sz; x++ {
				dx := float64(x) + 0.5 - cx
				dy := float64(y) + 0.5 - cy
				d := math.Hypot(dx, dy)
				if d <= rOuter && d >= rInner {
					cur := img.RGBAAt(x, y)
					if alpha > cur.A {
						cur.R, cur.G, cur.B, cur.A = 0, 0, 0, alpha
						img.SetRGBA(x, y, cur)
					}
				}
			}
		}
	}
	drawRing(12.5, 18, 8.5, 4.5, 245)
	drawRing(23.5, 18, 8.5, 4.5, 245)
	if running {
		// Solid center core dot indicates active cloud GPU session
		drawRing(18, 18, 3.2, 0, 255)
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

type Palette struct {
	Dark                   bool
	BgTop                  ui.Color
	BgBottom               ui.Color
	CardBg                 ui.Color
	CardTop                ui.Color
	CardBottom             ui.Color
	CardBorder             ui.Color
	CardBorderTop          ui.Color
	CardBorderBottom       ui.Color
	CardInnerTopLight      ui.Color
	CardShadow             ui.Color
	RecessedBg             ui.Color
	RecessedBorder         ui.Color
	RecessedInnerShadow    ui.Color
	SubtleBg               ui.Color
	SubtleBorder           ui.Color
	DisabledBg             ui.Color
	DisabledBorder         ui.Color
	RowSelectedBg          ui.Color
	RowSelectedBorder      ui.Color
	RowDivider             ui.Color
	Divider                ui.Color
	TextPrimary            ui.Color
	TextMuted              ui.Color
	TextFaint              ui.Color
	NavBg                  ui.Color
	NavPillBg              ui.Color
	NavPillText            ui.Color
	Emerald                ui.Color
	Amber                  ui.Color
	Sky                    ui.Color
	Coral                  ui.Color
	Cyan                   ui.Color
	Violet                 ui.Color
	PrimaryBtnBg           ui.Color
	PrimaryBtnFg           ui.Color
	PrimaryBtnTop          ui.Color
	PrimaryBtnBottom       ui.Color
	PrimaryBtnBorderTop    ui.Color
	PrimaryBtnBorderBottom ui.Color
}

func PaletteFor(dark bool) Palette {
	if dark {
		return Palette{
			Dark:                   true,
			BgTop:                  ui.RGB(34, 32, 28),
			BgBottom:               ui.RGB(24, 23, 20),
			CardBg:                 ui.RGB(40, 38, 34),
			CardTop:                ui.RGB(45, 42, 37),
			CardBottom:             ui.RGB(36, 34, 30),
			CardBorder:             ui.RGBA(255, 255, 255, 0.095),
			CardBorderTop:          ui.RGBA(255, 255, 255, 0.14),
			CardBorderBottom:       ui.RGBA(255, 255, 255, 0.04),
			CardInnerTopLight:      ui.RGBA(255, 255, 255, 0.09),
			CardShadow:             ui.RGBA(0, 0, 0, 0.34),
			RecessedBg:             ui.RGB(23, 21, 18),
			RecessedBorder:         ui.RGBA(255, 255, 255, 0.06),
			RecessedInnerShadow:    ui.RGBA(0, 0, 0, 0.36),
			SubtleBg:               ui.RGBA(255, 255, 255, 0.055),
			SubtleBorder:           ui.RGBA(255, 255, 255, 0.10),
			DisabledBg:             ui.RGBA(255, 255, 255, 0.025),
			DisabledBorder:         ui.RGBA(255, 255, 255, 0.055),
			RowSelectedBg:          ui.RGBA(255, 255, 255, 0.06),
			RowSelectedBorder:      ui.RGBA(255, 255, 255, 0.115),
			RowDivider:             ui.RGBA(255, 255, 255, 0.055),
			Divider:                ui.RGBA(255, 255, 255, 0.09),
			TextPrimary:            ui.RGBA(252, 250, 245, 0.94),
			TextMuted:              ui.RGBA(252, 250, 245, 0.64),
			TextFaint:              ui.RGBA(252, 250, 245, 0.42),
			NavBg:                  ui.RGBA(255, 255, 255, 0.075),
			NavPillBg:              ui.RGBA(252, 250, 245, 0.94),
			NavPillText:            ui.RGB(24, 22, 20),
			Emerald:                ui.RGB(48, 209, 132),
			Amber:                  ui.RGB(245, 166, 35),
			Sky:                    ui.RGB(90, 168, 255),
			Coral:                  ui.RGB(240, 96, 86),
			Cyan:                   ui.RGB(72, 200, 220),
			Violet:                 ui.RGB(154, 118, 250),
			PrimaryBtnBg:           ui.RGB(245, 166, 35),
			PrimaryBtnFg:           ui.RGB(22, 20, 18),
			PrimaryBtnTop:          ui.RGB(255, 184, 61),
			PrimaryBtnBottom:       ui.RGB(235, 142, 10),
			PrimaryBtnBorderTop:    ui.RGB(255, 226, 163),
			PrimaryBtnBorderBottom: ui.RGB(182, 105, 0),
		}
	}
	return Palette{
		Dark:                   false,
		BgTop:                  ui.RGB(238, 236, 231),
		BgBottom:               ui.RGB(228, 226, 220),
		CardBg:                 ui.RGBA(252, 251, 248, 0.95),
		CardTop:                ui.RGBA(255, 254, 251, 0.96),
		CardBottom:             ui.RGBA(248, 247, 243, 0.94),
		CardBorder:             ui.RGBA(24, 22, 18, 0.11),
		CardBorderTop:          ui.RGBA(255, 255, 255, 0.92),
		CardBorderBottom:       ui.RGBA(24, 22, 18, 0.12),
		CardInnerTopLight:      ui.RGBA(255, 255, 255, 0.90),
		CardShadow:             ui.RGBA(24, 22, 18, 0.07),
		RecessedBg:             ui.RGB(234, 231, 224),
		RecessedBorder:         ui.RGBA(24, 22, 18, 0.09),
		RecessedInnerShadow:    ui.RGBA(24, 22, 18, 0.08),
		SubtleBg:               ui.RGBA(24, 22, 18, 0.045),
		SubtleBorder:           ui.RGBA(24, 22, 18, 0.09),
		DisabledBg:             ui.RGBA(24, 22, 18, 0.025),
		DisabledBorder:         ui.RGBA(24, 22, 18, 0.055),
		RowSelectedBg:          ui.RGBA(224, 132, 8, 0.08),
		RowSelectedBorder:      ui.RGBA(224, 132, 8, 0.24),
		RowDivider:             ui.RGBA(24, 22, 18, 0.055),
		Divider:                ui.RGBA(24, 22, 18, 0.09),
		TextPrimary:            ui.RGB(25, 24, 22),
		TextMuted:              ui.RGBA(25, 24, 22, 0.62),
		TextFaint:              ui.RGBA(25, 24, 22, 0.42),
		NavBg:                  ui.RGBA(24, 22, 18, 0.065),
		NavPillBg:              ui.RGB(34, 32, 29),
		NavPillText:            ui.RGB(252, 250, 245),
		Emerald:                ui.RGB(16, 164, 98),
		Amber:                  ui.RGB(224, 132, 8),
		Sky:                    ui.RGB(36, 118, 228),
		Coral:                  ui.RGB(218, 66, 56),
		Cyan:                   ui.RGB(16, 148, 174),
		Violet:                 ui.RGB(122, 82, 230),
		PrimaryBtnBg:           ui.RGB(245, 166, 35),
		PrimaryBtnFg:           ui.RGB(22, 20, 18),
		PrimaryBtnTop:          ui.RGB(255, 178, 46),
		PrimaryBtnBottom:       ui.RGB(232, 138, 6),
		PrimaryBtnBorderTop:    ui.RGB(255, 222, 148),
		PrimaryBtnBorderBottom: ui.RGB(186, 108, 0),
	}
}

func WithAlpha(c ui.Color, a float32) ui.Color {
	return ui.RGBA(c.R, c.G, c.B, a)
}

func AccountTagColor(pal Palette, tag core.AccountColorTag) ui.Color {
	switch tag {
	case core.ColorEmerald:
		return pal.Emerald
	case core.ColorSky:
		return pal.Sky
	case core.ColorViolet:
		return pal.Violet
	case core.ColorCoral:
		return pal.Coral
	case core.ColorCyan:
		return pal.Cyan
	default:
		return pal.Amber
	}
}

func WorkloadColor(pal Palette, w core.WorkloadKind) ui.Color {
	switch w {
	case core.WorkloadLLM:
		return pal.Sky
	case core.WorkloadVideo:
		return pal.Violet
	case core.WorkloadImage:
		return pal.Amber
	case core.WorkloadAudio:
		return pal.Cyan
	case core.WorkloadMusic:
		return pal.Emerald
	default:
		return pal.TextMuted
	}
}

func Badge(c *ui.Context, pal Palette, text string, tint *ui.Color) {
	bg := pal.SubtleBg
	fg := pal.TextMuted
	if tint != nil {
		bg = WithAlpha(*tint, 0.16)
		fg = *tint
	}
	ui.Row(c).Padding(3, 8).Radius(6).Background(bg).Shrink(0).AlignItems(ui.Center).Children(func() {
		ui.Text(c, text).FontSize(11.5).FontWeight(600).TextColor(fg).MaxLines(1)
	})
}

func PillButton(c *ui.Context, pal Palette, label string, primary bool, disabled bool, onClick func()) {
	bg := pal.SubtleBg
	border := pal.SubtleBorder
	fg := pal.TextPrimary
	weight := 500
	if primary {
		if disabled {
			bg = pal.SubtleBg
			fg = pal.TextFaint
		} else {
			bg = pal.PrimaryBtnBg
			border = WithAlpha(pal.PrimaryBtnBg, 0.6)
			fg = pal.PrimaryBtnFg
			weight = 600
		}
	} else if disabled {
		fg = pal.TextFaint
	}
	el := ui.Row(c).Padding(8, 15).Radius(9).Background(bg).Border(1, border).AlignItems(ui.Center).Justify(ui.Center)
	if !disabled && onClick != nil {
		el.OnClick(onClick)
	}
	el.Children(func() {
		ui.Text(c, label).FontSize(13).FontWeight(weight).TextColor(fg).MaxLines(1)
	})
}

func SmallActionButton(c *ui.Context, pal Palette, label string, tint *ui.Color, disabled bool, onClick func()) {
	bg := pal.SubtleBg
	border := pal.SubtleBorder
	fg := pal.TextPrimary
	if tint != nil && !disabled {
		bg = WithAlpha(*tint, 0.14)
		border = WithAlpha(*tint, 0.34)
		fg = *tint
	} else if disabled {
		fg = pal.TextFaint
	}
	el := ui.Row(c).Padding(5, 11).Radius(7).Background(bg).Border(1, border).AlignItems(ui.Center).Justify(ui.Center).Shrink(0)
	if !disabled && onClick != nil {
		el.OnClick(onClick)
	}
	el.Children(func() {
		ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(fg).MaxLines(1)
	})
}

func BentoCard(c *ui.Context, pal Palette, height float32, fn func()) {
	el := PrecisionCard(c, pal, 16, 16, nil).Gap(8)
	if height > 0 {
		el.Height(height)
	}
	if fn != nil {
		el.Children(fn)
	}
}

func SectionCard(c *ui.Context, pal Palette, title, rightNote string, fn func()) {
	PrecisionCard(c, pal, 16, 18, func() {
		if title != "" {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, title).FontSize(14).FontWeight(600).TextColor(pal.TextPrimary)
				ui.Spacer(c)
				if rightNote != "" {
					ui.Text(c, rightNote).FontSize(12).TextColor(pal.TextMuted)
				}
			})
		}
		if fn != nil {
			fn()
		}
	}).Gap(12)
}

func TileHeader(c *ui.Context, pal Palette, dot ui.Color, title, badge string, badgeTint *ui.Color) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(7).Children(func() {
		DrawPrecisionLED(c, dot, 12)
		ui.Text(c, title).FontSize(13).FontWeight(500).TextColor(pal.TextMuted).MaxLines(1)
		ui.Spacer(c)
		if badge != "" {
			Badge(c, pal, badge, badgeTint)
		}
	})
}

// Vector Micro-Charts

func DrawAuraOrb(c *ui.Context, pal Palette, phase core.InstancePhase, billingWarning bool, size float32) {
	ui.Box(c).Width(size).Height(size).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		switch {
		case phase.IsRunning():
			p.FillRadialGradient(r, ui.RadialGradient{
				Center: ui.Point{X: 0.5, Y: 0.5},
				Radius: r.W * 0.52,
				Stops: []ui.GradientStop{
					{Color: ui.RGBA(56, 232, 165, 0.96), At: 0.0},
					{Color: ui.RGBA(28, 198, 134, 0.86), At: 0.56},
					{Color: ui.RGBA(28, 198, 134, 0.22), At: 0.84},
					{Color: ui.RGBA(28, 198, 134, 0.0), At: 1.0},
				},
			}, r.W/2)
			bolt := &ui.Path{}
			s := size / 46.0
			bolt.MoveTo(cx+1.5*s, cy-8.5*s)
			bolt.LineTo(cx-5.5*s, cy+1.0*s)
			bolt.LineTo(cx-0.5*s, cy+1.0*s)
			bolt.LineTo(cx-2.0*s, cy+8.5*s)
			bolt.LineTo(cx+5.5*s, cy-1.0*s)
			bolt.LineTo(cx+0.5*s, cy-1.0*s)
			bolt.Close()
			p.FillPath(bolt, ui.RGB(255, 255, 255))
		case phase.IsQueuing() || billingWarning:
			p.FillRadialGradient(r, ui.RadialGradient{
				Center: ui.Point{X: 0.5, Y: 0.5},
				Radius: r.W * 0.52,
				Stops: []ui.GradientStop{
					{Color: ui.RGBA(250, 185, 62, 0.94), At: 0.0},
					{Color: ui.RGBA(235, 148, 22, 0.80), At: 0.56},
					{Color: ui.RGBA(235, 148, 22, 0.16), At: 0.84},
					{Color: ui.RGBA(235, 148, 22, 0.0), At: 1.0},
				},
			}, r.W/2)
			p.Fill(ui.Rect{X: cx - 2, Y: cy - 6, W: 4, H: 8}, ui.RGB(255, 255, 255), 2)
			p.Fill(ui.Rect{X: cx - 2, Y: cy + 4, W: 4, H: 4}, ui.RGB(255, 255, 255), 2)
		default:
			p.FillRadialGradient(r, ui.RadialGradient{
				Center: ui.Point{X: 0.5, Y: 0.5},
				Radius: r.W * 0.48,
				Stops: []ui.GradientStop{
					{Color: ui.RGBA(148, 154, 166, 0.42), At: 0.0},
					{Color: ui.RGBA(120, 126, 138, 0.20), At: 0.72},
					{Color: ui.RGBA(120, 126, 138, 0.0), At: 1.0},
				},
			}, r.W/2)
			p.Stroke(ui.Rect{X: r.X + 4, Y: r.Y + 4, W: r.W - 8, H: r.H - 8}, pal.SubtleBorder, (r.W-8)/2, 1)
		}
	})
}

func DrawVRAMSegments(c *ui.Context, pal Palette, ratio float64, tint ui.Color, height float32) {
	ui.Box(c).FillWidth().Height(height).Draw(func(p *ui.Painter, r ui.Rect) {
		const segs = 10
		gap := float32(4.5)
		segW := (r.W - gap*float32(segs-1)) / float32(segs)
		active := int(math.Round(math.Min(1, math.Max(0, ratio)) * segs))
		for i := 0; i < segs; i++ {
			hFrac := float32(0.42 + 0.58*(float64(i)/float64(segs-1)))
			h := r.H * hFrac
			if ratio <= 0 {
				h = 6
			}
			x := r.X + float32(i)*(segW+gap)
			y := r.Y + r.H - h
			col := pal.SubtleBg
			if i < active {
				col = tint
			}
			p.Fill(ui.Rect{X: x, Y: y, W: segW, H: h}, col, 3)
		}
	})
}

func DrawSparkline(c *ui.Context, pal Palette, points []float64, tint ui.Color, height float32) {
	pts := points
	if len(pts) < 2 {
		pts = []float64{2, 2, 2, 2}
	}
	ui.Box(c).FillWidth().Height(height).Draw(func(p *ui.Painter, r ui.Rect) {
		poly := make([][2]float32, len(pts))
		area := &ui.Path{}
		stepX := r.W / float32(len(pts)-1)
		for i, v := range pts {
			clamped := math.Min(100, math.Max(0, v))
			x := r.X + float32(i)*stepX
			y := r.Y + r.H - float32(clamped/100.0)*(r.H-4) - 2
			poly[i] = [2]float32{x, y}
			if i == 0 {
				area.MoveTo(x, r.Y+r.H)
				area.LineTo(x, y)
			} else {
				area.LineTo(x, y)
			}
		}
		area.LineTo(r.X+r.W, r.Y+r.H)
		area.Close()
		p.FillPathGradient(area, ui.LinearGradient{
			From:  WithAlpha(tint, 0.22),
			To:    WithAlpha(tint, 0.0),
			Angle: 180,
		})
		p.StrokePolyline(poly, 2.0, tint)
	})
}

func DrawHorizonBar(c *ui.Context, pal Palette, progress float64, tint ui.Color, height float32) {
	ratio := float32(math.Min(1, math.Max(0, progress)))
	ui.Box(c).FillWidth().Height(height).Draw(func(p *ui.Painter, r ui.Rect) {
		rad := r.H / 2
		p.Fill(r, pal.SubtleBg, rad)
		if ratio > 0 {
			w := max(r.H, r.W*ratio)
			p.Fill(ui.Rect{X: r.X, Y: r.Y, W: w, H: r.H}, tint, rad)
		}
	})
}

func DrawRingGauge(c *ui.Context, pal Palette, progress float64, tint ui.Color, size float32) {
	prog := float32(math.Min(1, math.Max(0.02, progress)))
	ui.Box(c).Width(size).Height(size).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		rad := r.W/2 - 4
		bgCircle := (&ui.Path{}).Circle(cx, cy, rad)
		p.StrokePath(bgCircle, 4.0, pal.SubtleBg)

		arc := (&ui.Path{}).Circle(cx, cy, rad).Trim(0, prog).Cap(ui.RoundCap)
		p.Transform(ui.Rotation(-90), cx, cy, func() {
			p.StrokePath(arc, 4.0, tint)
		})
		inner := size * 0.42
		p.Fill(ui.Rect{X: cx - inner/2, Y: cy - inner/2, W: inner, H: inner}, tint, inner/2)
		p.Line(cx, cy, cx, cy-inner*0.24, 1.7, pal.BgTop)
		p.Line(cx, cy, cx+inner*0.20, cy, 1.7, pal.BgTop)
	})
}
