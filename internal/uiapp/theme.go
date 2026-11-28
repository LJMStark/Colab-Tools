package uiapp

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"math"

	"github.com/LJMStark/Colab-Tools/assets"
	"github.com/LJMStark/Colab-Tools/internal/core"
	"github.com/egoist/mygo/ui"
)

var (
	appIconBitmap     *ui.Bitmap
	svgToolbarRefresh *ui.SVG
	svgToolbarPlay    *ui.SVG
)

func init() {
	if img, err := png.Decode(bytes.NewReader(assets.AppIconPNG)); err == nil {
		appIconBitmap = ui.NewBitmap(img)
	}
	svgToolbarRefresh = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.85" stroke-linecap="round" stroke-linejoin="round">
  <path d="M20 11A8.1 8.1 0 0 0 6.5 5.3L4 7.8"/>
  <path d="M4 3.5V7.8H8.3"/>
  <path d="M4 13A8.1 8.1 0 0 0 17.5 18.7L20 16.2"/>
  <path d="M20 20.5V16.2H15.7"/>
</svg>`))
	svgToolbarPlay = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" stroke="none">
  <path d="M7.5 5.4C7.5 4.55 8.43 4.03 9.15 4.48L19.45 10.98C20.12 11.4 20.12 12.38 19.45 12.8L9.15 19.3C8.43 19.75 7.5 19.23 7.5 18.38V5.4Z"/>
</svg>`))
}

// TrayIconPNG generates a crisp 36x36 monochrome template PNG for the macOS menu bar.
func TrayIconPNG(running bool) []byte {
	const sz = 36
	img := image.NewRGBA(image.Rect(0, 0, sz, sz))
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
	default:
		return pal.TextMuted
	}
}

// AccountDisplayStatus returns the shared human-readable status label and semantic color
// for an account across MainUI and PopoverUI.
func AccountDisplayStatus(acc core.AccountProfile, rt core.AccountRuntimeState, pal Palette) (string, ui.Color) {
	if rt.Phase.IsRunning() {
		if rt.DeployRunning {
			return "部署中", pal.Amber
		}
		if rt.DeployFailed {
			return "部署中断", pal.Coral
		}
		if !acc.EnableLLMModule && acc.DeployRecipe.IsVideoComfy() && !rt.ServerHealthy {
			return "未部署", pal.Amber
		}
		return "运行良好", pal.Emerald
	}
	if rt.Phase.IsQueuing() {
		if rt.Phase.QueuePersistent {
			return fmt.Sprintf("守候 第%d轮", max(1, rt.Phase.QueueAttempt)), pal.Amber
		}
		return fmt.Sprintf("排队 %d/%d", max(1, rt.Phase.QueueAttempt), max(1, rt.Phase.QueueMax)), pal.Amber
	}
	if rt.Phase.Kind == core.PhaseStarting {
		return "正在开机", pal.Amber
	}
	if rt.Phase.Kind == core.PhaseStopping {
		return "正在关机", pal.Coral
	}
	return "待机中", pal.TextMuted
}

// Core UI Primitives (consolidated from legacy scattered button/pill helpers)

// PrimaryBtn renders an amber metallic or coral danger primary action button.
func PrimaryBtn(c *ui.Context, pal Palette, label, iconName string, danger, disabled bool, onClick func()) {
	el := ui.Row(c).
		Height(35).
		Padding(0, 16).
		Radius(9).
		Gap(6).
		Shrink(0).
		AlignItems(ui.Center).
		Justify(ui.Center)

	fg := pal.PrimaryBtnFg
	if disabled {
		fg = pal.TextFaint
		el.Background(pal.DisabledBg).Border(1, pal.DisabledBorder)
	} else if danger {
		topCol := pal.Coral
		botCol := pal.Coral.Mix(ui.RGB(0, 0, 0), 0.14)
		shadowBlur := float32(14)
		shadowAlpha := float32(0.34)
		if el.Hovered() {
			topCol = topCol.Mix(ui.RGB(255, 255, 255), 0.08)
			botCol = botCol.Mix(ui.RGB(255, 255, 255), 0.06)
			shadowBlur = 18
			shadowAlpha = 0.44
		}
		el.LinearGradient(ui.LinearGradient{From: topCol, To: botCol, Angle: 180}).
			Border(1, WithAlpha(pal.Coral, 0.85)).
			InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.28)).
			Shadow(0, 5, shadowBlur, -3, WithAlpha(pal.Coral, shadowAlpha)).
			Cursor(ui.CursorPointer)
		fg = ui.RGB(255, 255, 255)
		if onClick != nil {
			el.OnClick(onClick)
		}
	} else {
		styleAmberMetallicButton(el, pal, false)
		if onClick != nil {
			el.OnClick(onClick)
		}
	}

	el.Children(func() {
		if iconName != "" {
			ToolbarIcon(c, iconName, 11, fg)
		}
		ui.Text(c, label).FontSize(13).FontWeight(600).TextColor(fg).MaxLines(1)
	})
}

// ActionBtn renders a unified secondary/accent action button for cards, toolbars, and drawers.
func ActionBtn(c *ui.Context, pal Palette, label string, tint *ui.Color, iconName string, grow, disabled bool, onClick func()) {
	bg := pal.SubtleBg
	border := pal.SubtleBorder
	fg := pal.TextPrimary
	icCol := pal.TextMuted
	if disabled {
		bg = pal.DisabledBg
		border = pal.DisabledBorder
		fg = pal.TextFaint
		icCol = pal.TextFaint
	} else if tint != nil {
		bg = WithAlpha(*tint, 0.14)
		border = WithAlpha(*tint, 0.38)
		fg = *tint
		icCol = *tint
	}
	el := ui.Row(c).Padding(6, 11).Radius(8).Gap(5).Background(bg).Border(1, border).
		AlignItems(ui.Center).Justify(ui.Center)
	if grow {
		el.Grow(1)
	} else {
		el.Shrink(0)
	}
	if !disabled && onClick != nil {
		el.Cursor(ui.CursorPointer).OnClick(onClick)
	}
	el.Children(func() {
		if iconName != "" {
			ToolbarIcon(c, iconName, 13, icCol)
		}
		ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(fg).MaxLines(1)
	})
}

// ChipToggle renders a unified selectable chip or segmented option pill.
func ChipToggle(c *ui.Context, pal Palette, label string, active, fill bool, onClick func()) {
	bg := pal.SubtleBg
	border := pal.SubtleBorder
	borderW := float32(1)
	fg := pal.TextMuted
	fw := 500
	if fill {
		fg = pal.TextPrimary
	}
	if active {
		bg = WithAlpha(pal.Amber, 0.15)
		border = WithAlpha(pal.Amber, 0.62)
		borderW = 1.2
		fg = pal.TextPrimary
		fw = 600
	}
	el := ui.Row(c).Padding(6, 9).Radius(8).Gap(5).Background(bg).Border(borderW, border).
		AlignItems(ui.Center).Justify(ui.Center)
	if fill {
		el.FillWidth()
	} else {
		el.Shrink(0)
	}
	if !active && el.Hovered() {
		el.Background(pal.CardTop).Border(1, pal.CardBorderBottom)
		fg = pal.TextPrimary
	}
	if onClick != nil {
		el.Cursor(ui.CursorPointer).OnClick(onClick)
	}
	el.Children(func() {
		ui.Text(c, label).FontSize(11.5).FontWeight(fw).TextColor(fg).MaxLines(1).Ellipsis("…")
	})
}

// StatusPill renders a recessed metadata pill with an optional leading LED dot.
func StatusPill(c *ui.Context, pal Palette, dotCol *ui.Color, label string) {
	ui.Row(c).
		Padding(4, 9).
		Radius(7).
		Background(pal.RecessedBg).
		Border(1, pal.RecessedBorder).
		InsetShadow(0, 1, 1, 0, pal.RecessedInnerShadow).
		Gap(5).
		AlignItems(ui.Center).
		Shrink(0).
		Children(func() {
			if dotCol != nil {
				DrawPrecisionLED(c, *dotCol, 9)
			}
			ui.Text(c, label).FontSize(11.5).FontWeight(600).FontFeatures("tnum").TextColor(pal.TextMuted).MaxLines(1)
		})
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
	if primary {
		PrimaryBtn(c, pal, label, "", false, disabled, onClick)
		return
	}
	bg := pal.SubtleBg
	border := pal.SubtleBorder
	fg := pal.TextPrimary
	if disabled {
		bg = pal.DisabledBg
		border = pal.DisabledBorder
		fg = pal.TextFaint
	}
	el := ui.Row(c).Padding(8, 15).Radius(9).Background(bg).Border(1, border).AlignItems(ui.Center).Justify(ui.Center).Shrink(0)
	if !disabled && onClick != nil {
		el.Cursor(ui.CursorPointer).OnClick(onClick)
	}
	el.Children(func() {
		ui.Text(c, label).FontSize(13).FontWeight(500).TextColor(fg).MaxLines(1)
	})
}

func SmallActionButton(c *ui.Context, pal Palette, label string, tint *ui.Color, disabled bool, onClick func()) {
	ActionBtn(c, pal, label, tint, "", false, disabled, onClick)
}

// Precision Surfaces, Icons & Micro-Charts (merged from components.go)

// PrecisionCard renders a CNC anodized metallic container with gradient fill,
// bevel border gradient, 1px inner top highlight, and subtle contact shadow.
func PrecisionCard(c *ui.Context, pal Palette, radius, pad float32, fn func()) ui.Element {
	el := ui.Column(c).FillWidth().Padding(pad).Gap(10).Radius(radius).
		LinearGradient(ui.LinearGradient{
			From: pal.CardTop, To: pal.CardBottom, Angle: 180,
		}).
		Border(1, ui.Color{}).
		BorderLinearGradient(ui.LinearGradient{
			From: pal.CardBorderTop, To: pal.CardBorderBottom, Angle: 180,
		}).
		InsetShadow(0, 1, 0, 0, pal.CardInnerTopLight).
		Shadow(0, 8, 24, -6, pal.CardShadow)
	if fn != nil {
		el.Children(fn)
	}
	return el
}

// ToolbarIcon renders a precompiled vector toolbar/popover icon ("refresh" or "play").
func ToolbarIcon(c *ui.Context, name string, size float32, tint ui.Color) {
	svg := svgToolbarRefresh
	if name == "play" {
		svg = svgToolbarPlay
	}
	if size <= 0 {
		size = 16
	}
	ui.Icon(c, svg).Width(size).Height(size).TextColor(tint)
}

// DrawPrecisionLED renders a hardware status LED with radial glow halo,
// solid core, and top-left specular highlight.
func DrawPrecisionLED(c *ui.Context, col ui.Color, size float32) {
	if size <= 0 {
		size = 14
	}
	ui.Box(c).Width(size).Height(size).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		p.FillRadialGradient(r, ui.RadialGradient{
			Center: ui.Point{X: 0.5, Y: 0.5},
			Radius: r.W * 0.5,
			Stops: []ui.GradientStop{
				{Color: WithAlpha(col, 0.38), At: 0.0},
				{Color: WithAlpha(col, 0.12), At: 0.55},
				{Color: WithAlpha(col, 0.0), At: 1.0},
			},
		}, r.W/2)

		core := max(float32(4.0), size*0.43)
		p.Fill(ui.Rect{
			X: cx - core/2,
			Y: cy - core/2,
			W: core,
			H: core,
		}, col, core/2)

		spec := max(float32(1.2), size*0.11)
		p.Fill(ui.Rect{
			X: cx - core*0.26,
			Y: cy - core*0.26,
			W: spec,
			H: spec,
		}, ui.RGBA(255, 255, 255, 0.65), spec/2)
	})
}

func styleAmberMetallicButton(el ui.Element, pal Palette, disabled bool) {
	if disabled {
		el.Background(pal.DisabledBg).Border(1, pal.DisabledBorder)
		return
	}
	topCol := pal.PrimaryBtnTop
	botCol := pal.PrimaryBtnBottom
	shadowBlur := float32(18)
	shadowAlpha := float32(0.42)
	if el.Hovered() {
		topCol = topCol.Mix(ui.RGB(255, 255, 255), 0.08)
		botCol = botCol.Mix(ui.RGB(255, 255, 255), 0.06)
		shadowBlur = 22
		shadowAlpha = 0.52
	}
	el.LinearGradient(ui.LinearGradient{
		From: topCol, To: botCol, Angle: 180,
	}).
		Border(1, ui.Color{}).
		BorderLinearGradient(ui.LinearGradient{
			From: pal.PrimaryBtnBorderTop, To: pal.PrimaryBtnBorderBottom, Angle: 180,
		}).
		InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.48)).
		Shadow(0, 6, shadowBlur, -3, ui.RGBA(235, 148, 14, shadowAlpha)).
		Cursor(ui.CursorPointer)
}

// DrawPrecisionBackdrop paints a soft warm-amber ambient glow behind the console
// and a subtle 28px coordinate dot matrix across the canvas.
func DrawPrecisionBackdrop(p *ui.Painter, r ui.Rect, pal Palette) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	glowAlpha := float32(0.055)
	dotCol := ui.RGBA(24, 22, 18, 0.035)
	if pal.Dark {
		glowAlpha = 0.075
		dotCol = ui.RGBA(255, 255, 255, 0.035)
	}
	p.FillRadialGradient(r, ui.RadialGradient{
		Center: ui.Point{X: 0.22, Y: 0.44},
		Radius: max(r.W, r.H) * 0.55,
		Stops: []ui.GradientStop{
			{Color: WithAlpha(pal.Amber, glowAlpha), At: 0.0},
			{Color: WithAlpha(pal.Amber, glowAlpha*0.35), At: 0.5},
			{Color: WithAlpha(pal.Amber, 0.0), At: 1.0},
		},
	}, 0)

	const step = float32(28)
	for y := r.Y + 14; y < r.Y+r.H-8; y += step {
		for x := r.X + 14; x < r.X+r.W-8; x += step {
			p.Fill(ui.Rect{X: x, Y: y, W: 1.2, H: 1.2}, dotCol, 0.6)
		}
	}
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
