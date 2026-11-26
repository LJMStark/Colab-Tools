package uiapp

import (
	"math"

	"github.com/egoist/mygo/ui"
)

var (
	svgScenarioFlash    *ui.SVG
	svgScenarioOriginal *ui.SVG
	svgScenarioLLM      *ui.SVG
	svgScenarioMore     *ui.SVG
	svgToolbarRefresh   *ui.SVG
	svgToolbarSettings  *ui.SVG
	svgToolbarPlay      *ui.SVG
)

func init() {
	svgScenarioFlash = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round">
  <rect x="2.5" y="5" width="19" height="14" rx="3"/>
  <path d="M13 8.5L9.5 12.5H12.5L11 15.5L14.5 11.5H11.5L13 8.5Z"/>
</svg>`))

	svgScenarioOriginal = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round">
  <circle cx="12" cy="12" r="9"/>
  <circle cx="12" cy="12" r="3.2"/>
  <line x1="12" y1="3" x2="14.6" y2="9.6"/>
  <line x1="19.8" y1="7.5" x2="14.8" y2="12.2"/>
  <line x1="19.8" y1="16.5" x2="13.2" y2="14.9"/>
  <line x1="12" y1="21" x2="9.4" y2="14.4"/>
  <line x1="4.2" y1="16.5" x2="9.2" y2="11.8"/>
  <line x1="4.2" y1="7.5" x2="10.8" y2="9.1"/>
</svg>`))

	svgScenarioLLM = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round">
  <rect x="3" y="4.5" width="18" height="15" rx="3"/>
  <path d="M7.5 10L10.5 12.5L7.5 15"/>
  <line x1="12" y1="15" x2="16.5" y2="15"/>
  <circle cx="16.5" cy="8.5" r="0.9" fill="currentColor" stroke="none"/>
  <circle cx="13.5" cy="8.5" r="0.9" fill="currentColor" stroke="none"/>
</svg>`))

	svgScenarioMore = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round">
  <rect x="4" y="4" width="6.5" height="6.5" rx="1.5"/>
  <rect x="4" y="13.5" width="6.5" height="6.5" rx="1.5"/>
  <rect x="13.5" y="13.5" width="6.5" height="6.5" rx="1.5"/>
  <g transform="rotate(15 16.75 7.25)">
    <rect x="13.5" y="4" width="6.5" height="6.5" rx="1.5"/>
  </g>
</svg>`))

	svgToolbarRefresh = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.85" stroke-linecap="round" stroke-linejoin="round">
  <path d="M20 11A8.1 8.1 0 0 0 6.5 5.3L4 7.8"/>
  <path d="M4 3.5V7.8H8.3"/>
  <path d="M4 13A8.1 8.1 0 0 0 17.5 18.7L20 16.2"/>
  <path d="M20 20.5V16.2H15.7"/>
</svg>`))

	svgToolbarSettings = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
  <circle cx="12" cy="12" r="3.2"/>
  <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09a1.65 1.65 0 0 0-1.08-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09a1.65 1.65 0 0 0 1.51-1.08 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9c.26.604.852.997 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z"/>
</svg>`))

	svgToolbarPlay = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" stroke="none">
  <path d="M7.5 5.4C7.5 4.55 8.43 4.03 9.15 4.48L19.45 10.98C20.12 11.4 20.12 12.38 19.45 12.8L9.15 19.3C8.43 19.75 7.5 19.23 7.5 18.38V5.4Z"/>
</svg>`))
}

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

// DrawComputeDial renders the Dieter Rams anodized compute readiness dial
// with recessed plate, 60-tick radial scale, amber conic arc, tabular readout,
// and bottom double-ring infinity emblem.
func DrawComputeDial(c *ui.Context, pal Palette, size float32, balanceText, unitText string, ratio float64, idleNeutral bool) {
	if size <= 0 {
		size = 176
	}
	prog := float32(math.Min(1.0, math.Max(0.0, ratio)))
	if prog > 0 && prog < 0.03 {
		prog = 0.03
	}

	ui.Column(c).Width(size).Height(size).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Draw(func(p *ui.Painter, r ui.Rect) {
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		outerR := r.W / 2

		// 1. L2 recessed dial base + subtle anodized chamfer sheen at 35°/215°
		p.Fill(r, pal.RecessedBg, outerR)
		sheenHi := ui.RGBA(255, 255, 255, 0.09)
		if !pal.Dark {
			sheenHi = ui.RGBA(255, 255, 255, 0.36)
		}
		p.FillConicGradient(r, ui.ConicGradient{
			Center: ui.Point{X: 0.5, Y: 0.5},
			Angle:  35,
			Stops: []ui.GradientStop{
				{Color: sheenHi, At: 0.0},
				{Color: ui.RGBA(255, 255, 255, 0), At: 0.22},
				{Color: ui.RGBA(255, 255, 255, 0), At: 0.38},
				{Color: sheenHi, At: 0.50},
				{Color: ui.RGBA(255, 255, 255, 0), At: 0.72},
				{Color: ui.RGBA(255, 255, 255, 0), At: 0.88},
				{Color: sheenHi, At: 1.0},
			},
		}, outerR)
		p.Stroke(r, pal.RecessedBorder, outerR, 1)

		// 2. 60-tick radial scale via rotation transforms (never slanted Painter.Line)
		rTickOuter := outerR - 6
		tickMajor := WithAlpha(pal.TextPrimary, 0.36)
		tickMinor := WithAlpha(pal.TextPrimary, 0.14)
		for i := 0; i < 60; i++ {
			isMajor := i%5 == 0
			tickLen := float32(4.0)
			tickW := float32(1.0)
			tickCol := tickMinor
			if isMajor {
				tickLen = 7.0
				tickW = 1.5
				tickCol = tickMajor
			}
			p.Transform(ui.Rotation(float32(i*6)), cx, cy, func() {
				p.Line(cx, cy-rTickOuter, cx, cy-rTickOuter+tickLen, tickW, tickCol)
			})
		}

		// 3. Track ring + amber conic energy arc
		rArc := outerR - 18.5
		trackCol := WithAlpha(pal.TextPrimary, 0.08)
		if pal.Dark {
			trackCol = WithAlpha(pal.TextPrimary, 0.07)
		}
		trackPath := (&ui.Path{}).Circle(cx, cy, rArc)
		p.StrokePath(trackPath, 5.5, trackCol)

		if prog > 0 {
			arc := (&ui.Path{}).Circle(cx, cy, rArc).Trim(0, prog).Cap(ui.RoundCap)
			p.Transform(ui.Rotation(-90), cx, cy, func() {
				if idleNeutral {
					p.StrokePath(arc, 5.5, WithAlpha(pal.TextMuted, 0.45))
					return
				}
				amberStart := ui.RGB(245, 158, 11)
				amberBright := ui.RGB(255, 209, 120)
				p.StrokePathConicGradient(arc, 5.5, ui.ConicGradient{
					Center: ui.Point{X: 0.5, Y: 0.5},
					Angle:  0,
					Oklab:  true,
					Stops: []ui.GradientStop{
						{Color: amberStart, At: 0.0},
						{Color: amberBright, At: 0.55},
						{Color: amberStart, At: 1.0},
					},
				})
			})
		}

		// 4. Bottom 18x10 double-ring infinity (∞) emblem
		infY := cy + rArc*0.64
		ringR := float32(4.2)
		offset := float32(4.2)
		leftRing := (&ui.Path{}).Circle(cx-offset, infY, ringR)
		rightRing := (&ui.Path{}).Circle(cx+offset, infY, ringR)
		rightCol := pal.TextMuted
		leftCol := pal.Amber
		if idleNeutral {
			leftCol = WithAlpha(pal.TextMuted, 0.72)
		}
		p.StrokePath(rightRing, 1.6, rightCol)
		p.StrokePath(leftRing, 1.6, leftCol)
	}).Children(func() {
		fontSize := float32(34)
		if len(balanceText) >= 7 {
			fontSize = 28
		}
		ui.Text(c, balanceText).
			FontSize(fontSize).
			FontWeight(700).
			FontFeatures("tnum").
			TextColor(pal.TextPrimary).
			MaxLines(1)
		if unitText != "" {
			ui.Text(c, unitText).
				FontSize(10.5).
				FontWeight(600).
				LetterSpacing(0.8).
				TextColor(pal.TextMuted).
				MaxLines(1)
		}
	})
}

// ScenarioIcon renders one of the 4 precompiled scenario SVGs ("flash", "original",
// "llm", "more") seated inside a 36x36 L2 recessed socket.
func ScenarioIcon(c *ui.Context, pal Palette, kind string, tint ui.Color, active bool) {
	svg := svgScenarioMore
	switch kind {
	case "flash":
		svg = svgScenarioFlash
	case "original":
		svg = svgScenarioOriginal
	case "llm":
		svg = svgScenarioLLM
	case "more":
		svg = svgScenarioMore
	}

	bg := pal.RecessedBg
	border := pal.RecessedBorder
	if active {
		bg = WithAlpha(tint, 0.13)
		border = WithAlpha(tint, 0.36)
	}

	ui.Column(c).Width(36).Height(36).Radius(9).Shrink(0).
		Background(bg).
		Border(1, border).
		InsetShadow(0, 1, 2, 0, pal.RecessedInnerShadow).
		AlignItems(ui.Center).
		Justify(ui.Center).
		Children(func() {
			ui.Icon(c, svg).Width(20).Height(20).TextColor(tint)
		})
}

// ToolbarIcon renders a precompiled vector toolbar/popover icon ("refresh",
// "settings", or "play") at the requested size and tint.
func ToolbarIcon(c *ui.Context, name string, size float32, tint ui.Color) {
	svg := svgToolbarSettings
	switch name {
	case "refresh":
		svg = svgToolbarRefresh
	case "settings":
		svg = svgToolbarSettings
	case "play":
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

// TelemetryPillItem describes one interlocked hardware telemetry cell in ArmedTelemetryStrip.
type TelemetryPillItem struct {
	DotColor ui.Color
	Label    string
	Value    string
	Sub      string
}

// ArmedTelemetryStrip renders the pre-flight 3-channel hardware telemetry interlock bar.
func ArmedTelemetryStrip(c *ui.Context, pal Palette, items []TelemetryPillItem) {
	if len(items) == 0 {
		return
	}
	tracks := make([]ui.Track, 0, len(items)*2-1)
	for i := range items {
		if i > 0 {
			tracks = append(tracks, ui.Fixed(1))
		}
		tracks = append(tracks, ui.Fr(1))
	}

	ui.Column(c).FillWidth().Padding(10, 12).Radius(12).
		Background(pal.RecessedBg).
		Border(1, pal.RecessedBorder).
		InsetShadow(0, 1, 2, 0, pal.RecessedInnerShadow).
		Children(func() {
			ui.Grid(c).FillWidth().ColumnTracks(tracks...).GapX(12).AlignItems(ui.Stretch).Children(func() {
				for i, item := range items {
					if i > 0 {
						ui.Box(c).Width(1).FillHeight().Background(pal.Divider)
					}
					it := item
					ui.Column(c).FillWidth().Gap(3).Children(func() {
						ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
							DrawPrecisionLED(c, it.DotColor, 10)
							ui.Text(c, it.Label).FontSize(11.5).FontWeight(500).TextColor(pal.TextMuted).MaxLines(1)
						})
						ui.Text(c, it.Value).
							FontSize(13).
							FontWeight(600).
							FontFeatures("tnum").
							TextColor(pal.TextPrimary).
							MaxLines(1)
						if it.Sub != "" {
							ui.Text(c, it.Sub).FontSize(11).TextColor(pal.TextFaint).MaxLines(1)
						}
					})
				}
			})
		})
}

// styleAmberMetallicButton applies the Dieter Rams amber metallic bevel style
// shared by the primary launch button and the Bento card 1 ignition button.
func styleAmberMetallicButton(el ui.Element, pal Palette, disabled bool) {
	if disabled {
		el.Background(pal.DisabledBg).Border(1, pal.DisabledBorder)
		return
	}
	hovered := el.Hovered()
	topCol := pal.PrimaryBtnTop
	botCol := pal.PrimaryBtnBottom
	shadowBlur := float32(18)
	shadowAlpha := float32(0.42)
	if hovered {
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

// PrimaryLaunchButton renders the Dieter Rams amber metallic primary CTA button
// with a vector play triangle, beveled border gradient, inner highlight, and hover lift.
func PrimaryLaunchButton(c *ui.Context, pal Palette, label string, disabled bool, onClick func()) {
	el := ui.Row(c).
		MinWidth(216).
		Height(44).
		Padding(0, 22).
		Radius(11).
		Gap(9).
		Shrink(0).
		AlignItems(ui.Center).
		Justify(ui.Center)

	fg := pal.PrimaryBtnFg
	if disabled {
		fg = pal.TextFaint
	} else if onClick != nil {
		el.OnClick(onClick)
	}
	styleAmberMetallicButton(el, pal, disabled)

	el.Children(func() {
		ui.Box(c).Width(10).Height(12).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
			tri := &ui.Path{}
			tri.MoveTo(r.X+1.2, r.Y+1.0)
			tri.LineTo(r.X+r.W-0.5, r.Y+r.H/2)
			tri.LineTo(r.X+1.2, r.Y+r.H-1.0)
			tri.Close()
			p.FillPath(tri, fg)
		})
		ui.Text(c, label).FontSize(14).FontWeight(600).TextColor(fg).MaxLines(1)
	})
}

// CompactAmberLaunchButton renders a compact Dieter Rams amber metallic button for Bento card actions.
func CompactAmberLaunchButton(c *ui.Context, pal Palette, label string, disabled bool, onClick func()) {
	el := ui.Row(c).
		FillWidth().
		Padding(7, 10).
		Radius(8).
		Gap(7).
		AlignItems(ui.Center).
		Justify(ui.Center)

	fg := pal.PrimaryBtnFg
	if disabled {
		fg = pal.TextFaint
	} else if onClick != nil {
		el.OnClick(onClick)
	}
	styleAmberMetallicButton(el, pal, disabled)

	el.Children(func() {
		ui.Box(c).Width(8).Height(10).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
			tri := &ui.Path{}
			tri.MoveTo(r.X+1.0, r.Y+0.8)
			tri.LineTo(r.X+r.W-0.4, r.Y+r.H/2)
			tri.LineTo(r.X+1.0, r.Y+r.H-0.8)
			tri.Close()
			p.FillPath(tri, fg)
		})
		ui.Text(c, label).FontSize(12.5).FontWeight(600).TextColor(fg).MaxLines(1)
	})
}

// DrawPrecisionBackdrop paints a soft warm-amber ambient glow behind the left Hero
// column and a subtle 28px coordinate dot matrix across the console canvas.
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
