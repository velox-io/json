package main

import (
	"fmt"
	"html"
	"math"
	"regexp"
	"sort"
	"strings"
)

// Layout constants. Width is adaptive to the number of X-axis groups;
// height grows with the number of sections (one coordinate system per
// section, plus an allocs/op log strip under each plot).
const (
	hPad      = 24
	minSVGW   = 960
	maxSVGW   = 2000
	padTop    = 24
	headerH   = 108
	secTitleH = 36
	secGap    = 34
	plotH     = 280
	allocH    = 110 // height of the allocs region below the label zone
	allocGap  = 40  // gap between the horizontal axis and the allocs region
	allocCapH = 28  // room under the allocs region for its axis caption
	xAxisH    = 60
	footerH   = 40
	axisPadL  = 64
	axisPadR  = 96
	slotMinW  = 88
	barMaxW   = 8
	barGap    = 5
)

// chartPoint is one library's normalized measurement within a group slot.
type chartPoint struct {
	Lib      string
	Lat      float64 // latency ratio vs baseline (lower is better)
	Mem      float64 // B/op ratio vs baseline; NaN when undefined
	Allocs   float64 // allocs/op ratio vs baseline; NaN when undefined
	NsOp     float64
	BOp      float64
	AllocsOp float64
}

// chartGroup is one payload slot on the X axis.
type chartGroup struct {
	Key     string
	Label   string
	Payload float64
	Points  []chartPoint
}

// secChart is the plotting model for one section: groups share the same
// coordinate system, so the axis extents are section-wide.
type secChart struct {
	Name    string
	Groups  []chartGroup
	Skipped []string
}

// buildSecChart normalizes a section's groups against the baseline library.
// Groups matching the exclude pattern are dropped; groups lacking baseline
// data are skipped and reported. Within each group the baseline library
// always comes first so its bar sits on the left side of the cluster.
func buildSecChart(data *BenchData, sec Section, baseline string, exclude *regexp.Regexp) *secChart {
	sc := &secChart{Name: sec.Name}
	for _, g := range sec.Groups {
		if exclude != nil && exclude.MatchString(g) {
			continue
		}
		gr := data.Results[g]
		base := gr[baseline]
		if base == nil {
			sc.Skipped = append(sc.Skipped, g)
			continue
		}

		_, ds := splitGroupToSection(g)
		for _, p := range datasetPrefixes {
			ds = strings.TrimPrefix(ds, p)
		}
		cg := chartGroup{Key: g, Label: ds}
		for _, r := range gr {
			if r.PayloadBytes > cg.Payload {
				cg.Payload = r.PayloadBytes
			}
		}
		buildPoint := func(lib string) chartPoint {
			r := gr[lib]
			p := chartPoint{
				Lib:      lib,
				Lat:      r.NsOp / base.NsOp,
				Mem:      math.NaN(),
				Allocs:   math.NaN(),
				NsOp:     r.NsOp,
				BOp:      r.BOp,
				AllocsOp: r.AllocsOp,
			}
			if base.BOp > 0 {
				p.Mem = r.BOp / base.BOp
			}
			if base.AllocsOp > 0 {
				p.Allocs = r.AllocsOp / base.AllocsOp
			}
			return p
		}
		cg.Points = append(cg.Points, buildPoint(baseline))
		for _, lib := range data.Libs {
			if lib == baseline {
				continue
			}
			if _, ok := gr[lib]; !ok {
				continue
			}
			cg.Points = append(cg.Points, buildPoint(lib))
		}
		sc.Groups = append(sc.Groups, cg)
	}

	if len(sc.Groups) == 0 {
		return nil
	}
	return sc
}

// RenderSVG generates the complete SVG string from parsed benchmark data.
// Every section gets one coordinate system: bars encode the latency ratio
// against the baseline (left axis), circles encode the B/op ratio (right
// axis, inverted so larger sits lower). Groups matching exclude (nil
// disables) are dropped.
func RenderSVG(data *BenchData, baseline string, exclude *regexp.Regexp) string {
	sections := data.Sections
	hasSections := len(sections) > 1
	if len(sections) == 0 {
		sections = []Section{{Name: "", Groups: data.Groups}}
	}

	var charts []*secChart
	maxGroups := 0
	for _, sec := range sections {
		sc := buildSecChart(data, sec, baseline, exclude)
		if sc == nil {
			continue
		}
		charts = append(charts, sc)
		if n := len(sc.Groups); n > maxGroups {
			maxGroups = n
		}
	}

	svgW := min(max(2*hPad+axisPadL+axisPadR+maxGroups*slotMinW, minSVGW), maxSVGW)

	bodyH := 0
	for _, sc := range charts {
		if bodyH > 0 {
			bodyH += secGap
		}
		bodyH += secTitleH + plotH + xAxisH
		if secHasAllocs(sc) {
			bodyH += allocGap + allocH + allocCapH - xAxisH
		}
	}
	if len(charts) == 0 {
		bodyH = 140
	}
	var skipped []string
	for _, sc := range charts {
		skipped = append(skipped, sc.Skipped...)
	}
	footH := 0
	if len(skipped) > 0 {
		footH = footerH
	}
	totalH := padTop + headerH + bodyH + footH

	var b strings.Builder
	fmt.Fprintf(&b, `<svg id="bv" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" font-family="%s" font-size="13">`+"\n",
		svgW, totalH, FontMono)
	fmt.Fprintf(&b, `  <defs><linearGradient id="cut-fade" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="%s"/><stop offset="1" stop-color="%s" stop-opacity="0"/></linearGradient></defs>`+"\n",
		ColorPaper, ColorPaper)
	b.WriteString(svgStyles())
	fmt.Fprintf(&b, `  <rect width="%d" height="%d" fill="%s"/>`+"\n", svgW, totalH, ColorPaper)

	// === Header ===
	title := data.Title
	if title == "" {
		title = "Benchmark Results"
	}
	y := padTop + 24
	fmt.Fprintf(&b, `  <text x="%d" y="%d" text-anchor="middle" class="title">%s</text>`+"\n",
		svgW/2, y, esc(title))
	if data.Subtitle != "" {
		y += 20
		fmt.Fprintf(&b, `  <text x="%d" y="%d" text-anchor="middle" class="subtitle">%s</text>`+"\n",
			svgW/2, y, esc(data.Subtitle))
	}
	y += 26
	renderLegend(&b, data.Libs, baseline, svgW/2, y)
	y += 18
	renderMarkLegend(&b, svgW/2, y)

	// === Charts ===
	x0 := hPad + axisPadL
	x1 := svgW - hPad - axisPadR
	curY := padTop + headerH
	echo := &echoStore{}
	for i, sc := range charts {
		if i > 0 {
			curY += secGap
		}
		if hasSections && sc.Name != "" {
			renderSectionHeader(&b, sc.Name, curY, x1-hPad)
		}
		xl := newXGeometry(sc, x0, x1)
		plotTop := curY + secTitleH
		renderChart(&b, xl, plotTop, baseline, echo)
		plotBottom := plotTop + plotH
		// The dataset labels sit right under the horizontal axis; the
		// allocs region shares the X coordinates but hangs below the
		// label zone.
		renderXLabels(&b, xl, plotBottom)
		if secHasAllocs(sc) {
			renderAllocsStrip(&b, xl, plotBottom, echo)
			curY = plotBottom + allocGap + allocH + allocCapH
		} else {
			curY = plotBottom + xAxisH
		}
	}

	// === Footer ===
	if len(charts) == 0 {
		fmt.Fprintf(&b, `  <text x="%d" y="%d" text-anchor="middle" class="foot">baseline library %q not found in results</text>`+"\n",
			svgW/2, padTop+headerH+70, baseline)
	}
	if len(skipped) > 0 {
		fmt.Fprintf(&b, `  <text x="%d" y="%d" text-anchor="middle" class="foot">%s</text>`+"\n",
			svgW/2, totalH-footerH/2+2, esc("skipped (no baseline): "+strings.Join(skipped, ", ")))
	}

	echo.flush(&b)

	b.WriteString("</svg>\n")
	return b.String()
}

// svgStyles fills the stylesheet's named placeholders in place: each
// CSS rule names its colors inline instead of mapping them through a
// positional argument list where values silently swap between rules.
func svgStyles() string {
	return strings.NewReplacer(
		"{{ColorTitle}}", ColorTitle,
		"{{ColorSubtitle}}", ColorSubtitle,
		"{{ColorDim}}", ColorDim,
		"{{ColorText}}", ColorText,
		"{{ColorPaper}}", ColorPaper,
		"{{ColorAxisMem}}", ColorAxisMem,
		"{{FontSans}}", FontSans,
	).Replace(`  <style>
    .title { font-size: 22px; font-weight: bold; fill: {{ColorTitle}}; font-family: {{FontSans}}; }
    .subtitle { font-size: 13px; fill: {{ColorSubtitle}}; font-family: {{FontSans}}; }
    .legend-label { font-size: 11px; font-weight: bold; font-family: {{FontSans}}; }
    .legend-note { font-size: 11px; fill: {{ColorDim}}; font-family: {{FontSans}}; }
    .sec-title { font-size: 15px; font-weight: bold; fill: {{ColorTitle}}; font-family: {{FontSans}}; letter-spacing: 0.5px; }
    .axis-cap { font-size: 10px; font-weight: 600; fill: {{ColorSubtitle}}; font-family: {{FontSans}}; }
    .grp-label { font-size: 10.5px; font-weight: 600; fill: {{ColorTitle}}; font-family: {{FontSans}}; }
    .dim { fill: {{ColorDim}}; }
    .bar-val { font-size: 7px; fill: {{ColorText}}; font-family: {{FontSans}}; font-variant-numeric: tabular-nums; }
    .val-g { cursor: pointer; }
    .val-g text { pointer-events: none; }
    .echo-g { display: none; pointer-events: none; }
    .val-g:target .bar-val, .echo-g .bar-val { font-size: 11px; fill: {{ColorText}}; font-weight: bold; stroke: {{ColorPaper}}; stroke-width: 3px; paint-order: stroke; }
    .bar-val-delta { display: none; font-size: 11px; font-weight: bold; stroke: {{ColorPaper}}; stroke-width: 3px; paint-order: stroke; }
    .val-g:target .bar-val-delta, .echo-g .bar-val-delta { display: inline; }
    .val-bg { display: none; fill: {{ColorPaper}}; }
    .val-g:target .val-bg, .echo-g .val-bg { display: inline; }
    .circ-val { display: none; font-size: 10px; font-weight: 600; fill: {{ColorAxisMem}}; stroke: {{ColorPaper}}; stroke-width: 3px; paint-order: stroke; font-family: {{FontSans}}; font-variant-numeric: tabular-nums; }
    .val-g:target .circ-val, .echo-g .circ-val { display: inline; }
    a.val-off { display: none; }
    .val-g:target a.val-off { display: inline; }
    .tick-lat { font-size: 10px; fill: {{ColorAxisMem}}; font-weight: 600; }
    .tick-mem { font-size: 10px; fill: {{ColorAxisMem}}; }
    .foot { font-size: 11px; fill: {{ColorDim}}; font-family: {{FontSans}}; }
  </style>
`)
}

// renderLegend draws the library color legend; the baseline library
// carries a suffix so the ratio semantics are anchored where the
// library colors are introduced.
func renderLegend(b *strings.Builder, libs []string, baseline string, cx, y int) {
	labels := make([]string, len(libs))
	totalW := 0
	for i, lib := range libs {
		labels[i] = lib
		if lib == baseline {
			labels[i] += " (baseline)"
		}
		totalW += 14 + 5 + len(labels[i])*7 + 24
	}
	x := cx - totalW/2
	for i, lib := range libs {
		c := LibraryColor(lib)
		fmt.Fprintf(b, `  <rect x="%d" y="%d" width="14" height="14" rx="3" fill="%s" opacity="0.85"/>`+"\n",
			x, y-11, c)
		fmt.Fprintf(b, `  <text x="%d" y="%d" class="legend-label" fill="%s">%s</text>`+"\n",
			x+19, y, c, esc(labels[i]))
		x += 14 + 5 + len(labels[i])*7 + 24
	}
}

// renderMarkLegend draws the mark legend under the library legend: a
// bar, a circle and a wedge glyph, each bound to the metric it plots
// and how its axis reads. Glyphs carry the neutral ink of the axes so
// no mark reads as a library color.
func renderMarkLegend(b *strings.Builder, cx, y int) {
	const glyphW, gap, itemGap = 6, 5, 24
	items := []struct {
		glyph func(x float64)
		label string
	}{
		{func(x float64) {
			fmt.Fprintf(b, `  <rect x="%.1f" y="%d" width="%d" height="12" rx="1" fill="%s" opacity="0.85"/>`+"\n",
				x, y-10, glyphW, ColorAxisMem)
		}, "latency × (left axis, lower = better)"},
		{func(x float64) {
			// Dot riding a trend segment, mirroring the plot's memory
			// marks; the gray fill stands in for the library color.
			fmt.Fprintf(b, `  <line x1="%.1f" y1="%d" x2="%.1f" y2="%d" stroke="%s" stroke-width="1.5" opacity="0.5"/>`+"\n",
				x-4, y-4, x+glyphW+4, y-4, ColorAxisMem)
			fmt.Fprintf(b, `  <circle cx="%.1f" cy="%d" r="3.2" fill="%s" stroke="%s" stroke-width="1"/>`+"\n",
				x+glyphW/2, y-4, ColorDim, ColorAxisMem)
		}, "B/op × (right axis, lower position = larger)"},
		{func(x float64) {
			fmt.Fprintf(b, `  <polygon points="%.1f,%d %.1f,%d %.1f,%.1f" fill="%s" opacity="0.85"/>`+"\n",
				x, y-9, x+glyphW, y-9, x+glyphW/2, float64(y)+2, ColorAxisMem)
			fmt.Fprintf(b, `  <circle cx="%.1f" cy="%.1f" r="2" fill="%s"/>`+"\n",
				x+glyphW/2, float64(y), ColorAxisMem)
		}, "allocs/op × (log strip, deeper = worse)"},
	}
	totalW := 0
	for _, it := range items {
		totalW += glyphW + gap + len(it.label)*6 + itemGap
	}
	x := float64(cx) - float64(totalW)/2
	for _, it := range items {
		it.glyph(x)
		fmt.Fprintf(b, `  <text x="%.1f" y="%d" class="legend-note">%s</text>`+"\n",
			x+glyphW+gap, y, esc(it.label))
		x += glyphW + gap + float64(len(it.label)*6) + itemGap
	}
}

func renderSectionHeader(b *strings.Builder, name string, y, rightX int) {
	fmt.Fprintf(b, "\n  <!-- Section: %s -->\n", esc(name))
	fmt.Fprintf(b, `  <text x="%d" y="%d" class="sec-title">%s</text>`+"\n", hPad, y+20, esc(name))
	fmt.Fprintf(b, `  <line x1="%d" y1="%d" x2="%d" y2="%d" stroke="%s" stroke-width="1" opacity="0.5"/>`+"\n",
		hPad, y+secTitleH-6, rightX, y+secTitleH-6, ColorGrid)
}

// xGeometry is the shared X layout of a section's plots: the main
// coordinate system and the allocs strip place every mark at the same
// slot and bar positions.
type xGeometry struct {
	sc     *secChart
	x0, x1 int
	slotW  float64
}

func newXGeometry(sc *secChart, x0, x1 int) xGeometry {
	return xGeometry{sc: sc, x0: x0, x1: x1, slotW: float64(x1-x0) / float64(len(sc.Groups))}
}

func (g xGeometry) slotCenter(gi int) float64 {
	return float64(g.x0) + (float64(gi)+0.5)*g.slotW
}

// barWidth returns the bar width for a group holding np libraries.
func (g xGeometry) barWidth(np int) float64 {
	if np == 0 {
		return 0
	}
	bw := (g.slotW-10)/float64(np) - barGap
	if bw > barMaxW {
		bw = barMaxW
	}
	if bw < 2 {
		bw = 2
	}
	return bw
}

// barCenter returns the x center of the bar for point pi of group gi.
// Circles and trend lines reuse the same x so every mark of a library
// stacks above that library's own bar.
func (g xGeometry) barCenter(gi, pi int) float64 {
	np := len(g.sc.Groups[gi].Points)
	if np == 0 {
		return g.slotCenter(gi)
	}
	bw := g.barWidth(np)
	total := float64(np)*bw + float64(np-1)*barGap
	return g.slotCenter(gi) - total/2 + float64(pi)*(bw+barGap) + bw/2
}

// renderChart draws one coordinate system: shared X axis of payload slots,
// left Y axis for latency ratios (bars), right Y axis for B/op ratios
// (circles, inverted with 0× at the top).
func renderChart(b *strings.Builder, xl xGeometry, plotTop int, baseline string, echo *echoStore) {
	sc := xl.sc
	x0, x1 := xl.x0, xl.x1
	plotBottom := plotTop + plotH

	// Axis extents are computed across every group in the chart so all
	// slots share one coordinate system. The latency top follows the
	// bulk of the non-baseline ratios instead of the maximum, floored
	// at 1× so the baseline bar fills the plot height; when some bar
	// actually regresses past the baseline the floor rises to 1.5× so
	// ordinary regressions stay on scale. Bars past the top are cut at
	// the plot edge and fade out; their value labels carry the true
	// ratio.
	memMax := 1.0
	latMax := 0.0
	var latRatios []float64
	for _, g := range sc.Groups {
		for _, p := range g.Points {
			if p.Lib != baseline {
				latRatios = append(latRatios, p.Lat)
				latMax = math.Max(latMax, p.Lat)
			}
			if !math.IsNaN(p.Mem) {
				memMax = math.Max(memMax, p.Mem)
			}
		}
	}
	latFloor := 1.0
	if latMax > 1.0 {
		latFloor = 1.5
	}
	latStep, latTop := axisScale(math.Max(latBulkCap(latRatios), latFloor))
	// A value label occupies the 11px band above its bar. When the
	// tallest on-scale labeled bar reaches the plot top, extend the
	// axis by one step; a step is at least a sixth of the top value,
	// so a single extension frees well over 11px for every bar at
	// once. Bars entering the extended band join the check, so the
	// extension repeats until every on-scale label has room.
	for {
		latLabeledMax := 0.0
		for _, g := range sc.Groups {
			for _, p := range g.Points {
				if p.Lib != baseline && p.Lat <= latTop {
					latLabeledMax = math.Max(latLabeledMax, p.Lat)
				}
			}
		}
		if latLabeledMax == 0 || float64(plotH)*(1-latLabeledMax/latTop) > 11 {
			break
		}
		latTop += latStep
	}
	memStep, memTop := axisScale(memMax * 1.1)
	yLat := func(v float64) float64 { return float64(plotBottom) - v/latTop*float64(plotH) }
	// The memory axis runs inverted, 0× at the top: latency and memory
	// ratios are correlated, so with both axes anchored at the bottom
	// every circle would land at the height of its own bar top. Inversion
	// pushes correlated marks to opposite corners of the plot and matches
	// the allocs strip, where deeper also means worse.
	yMem := func(v float64) float64 { return float64(plotTop) + v/memTop*float64(plotH) }

	// Axis captions. Both axes share the ink ColorAxisMem so neither
	// side reads as a library color; circle strokes stay ink to bind
	// them to the B/op axis, trend lines carry library colors. The ×
	// suffix marks the values as ratios against the baseline rather
	// than absolutes.
	fmt.Fprintf(b, `  <text x="%d" y="%d" class="axis-cap" fill="%s" text-anchor="start">latency ×</text>`+"\n",
		x0, plotTop-8, ColorAxisMem)
	fmt.Fprintf(b, `  <text x="%d" y="%d" class="axis-cap" fill="%s" text-anchor="end">B/op × (0 top)</text>`+"\n",
		x1, plotTop-8, ColorAxisMem)

	// Gridlines + left ticks (latency scale)
	for i := 0; float64(i)*latStep <= latTop+latStep*1e-9; i++ {
		v := float64(i) * latStep
		y := yLat(v)
		if i > 0 {
			fmt.Fprintf(b, `  <line x1="%d" y1="%.1f" x2="%d" y2="%.1f" stroke="%s" stroke-width="1" opacity="0.35"/>`+"\n",
				x0, y, x1, y, ColorGrid)
		}
		fmt.Fprintf(b, `  <text x="%d" y="%.1f" class="tick-lat" text-anchor="end">%s</text>`+"\n",
			x0-8, y+3.5, fmtRatio(v))
	}
	// Right ticks (memory scale). The 1× tick yields its spot to the
	// baseline annotation drawn next to the dashed baseline line.
	baselineHasMem := false
	for _, g := range sc.Groups {
		for _, p := range g.Points {
			if p.Lib == baseline && !math.IsNaN(p.Mem) {
				baselineHasMem = true
			}
		}
	}
	for i := 0; float64(i)*memStep <= memTop+memStep*1e-9; i++ {
		v := float64(i) * memStep
		if baselineHasMem && math.Abs(v-1) < 1e-6 {
			continue
		}
		fmt.Fprintf(b, `  <text x="%d" y="%.1f" class="tick-mem" text-anchor="start">%s</text>`+"\n",
			x1+8, yMem(v)+3.5, fmtRatio(v))
	}

	// Axis frame, ink on both sides
	fmt.Fprintf(b, `  <line x1="%d" y1="%d" x2="%d" y2="%d" stroke="%s" stroke-width="1.5"/>`+"\n",
		x0, plotTop, x0, plotBottom, ColorAxisMem)
	fmt.Fprintf(b, `  <line x1="%d" y1="%d" x2="%d" y2="%d" stroke="%s" stroke-width="1.5"/>`+"\n",
		x1, plotTop, x1, plotBottom, ColorAxisMem)
	fmt.Fprintf(b, `  <line x1="%d" y1="%d" x2="%d" y2="%d" stroke="%s" stroke-width="1.5"/>`+"\n",
		x0, plotBottom, x1, plotBottom, ColorDim)

	// Memory trend lines per library across payload slots, each in its
	// own library color. The baseline's circles all sit at exactly 1×;
	// the dashed line through them doubles as the memory baseline and
	// paints first so the other lines stay clear of it.
	var libs []string
	seen := make(map[string]bool)
	for _, g := range sc.Groups {
		for _, p := range g.Points {
			if !seen[p.Lib] {
				seen[p.Lib] = true
				libs = append(libs, p.Lib)
			}
		}
	}
	memCoords := func(lib string) []string {
		var coords []string
		for gi, g := range sc.Groups {
			for pi, p := range g.Points {
				if p.Lib == lib && !math.IsNaN(p.Mem) {
					coords = append(coords, fmt.Sprintf("%.1f,%.1f", xl.barCenter(gi, pi), yMem(p.Mem)))
				}
			}
		}
		return coords
	}
	if coords := memCoords(baseline); len(coords) >= 1 {
		// The line pokes past the right axis and carries a compact
		// annotation there identifying the memory baseline. It replaces
		// the plain 1× tick at the same height.
		coords = append(coords, fmt.Sprintf("%.1f,%.1f", float64(x1)+6, yMem(1)))
		fmt.Fprintf(b, `  <polyline points="%s" fill="none" stroke="%s" stroke-width="1.2" stroke-dasharray="4 3" opacity="0.7"/>`+"\n",
			strings.Join(coords, " "), LibraryColor(baseline))
		fmt.Fprintf(b, `  <text x="%d" y="%.1f" font-size="9.5" font-weight="600" fill="%s" text-anchor="start">1× B/op</text>`+"\n",
			x1+10, yMem(1)+3.5, ColorAxisMem)
	}
	for _, lib := range libs {
		if lib == baseline {
			continue
		}
		if coords := memCoords(lib); len(coords) >= 2 {
			fmt.Fprintf(b, `  <polyline points="%s" fill="none" stroke="%s" stroke-width="1.5" opacity="0.5"/>`+"\n",
				strings.Join(coords, " "), LibraryColor(lib))
		}
	}

	// Latency bars with their value labels. Each non-baseline bar is the
	// click target for its own label: the toggle anchors wrap the bar
	// itself, and every other element of the group carries no pointer
	// events, so clicking the number or the space around it never
	// toggles anything. Labels are placed from the lowest bar top
	// upward: each takes the shortest lift clearing the memory circles
	// and every label already placed in the cluster, so label order
	// follows bar order and the I-beams stay as short as the collisions
	// allow. Baseline bars stay unlabeled, their 1× height reading
	// directly against the axis ticks.
	labelOverlapsCircle := func(gi int, yTop, offset, cx, halfW float64) bool {
		g := sc.Groups[gi]
		ry0, ry1 := yTop-offset-7, yTop-offset
		for pi2, p2 := range g.Points {
			if math.IsNaN(p2.Mem) {
				continue
			}
			dcx := xl.barCenter(gi, pi2)
			dcy := yMem(p2.Mem)
			// Squared distance from the circle center to the label bounding box.
			nx := math.Max(cx-halfW, math.Min(dcx, cx+halfW))
			ny := math.Max(ry0, math.Min(dcy, ry1))
			if dx, dy := dcx-nx, dcy-ny; dx*dx+dy*dy <= 3.5*3.5 {
				return true
			}
		}
		return false
	}
	for gi, g := range sc.Groups {
		np := len(g.Points)
		if np == 0 {
			continue
		}
		bw := xl.barWidth(np)

		// Baseline bars carry no value label.
		for pi, p := range g.Points {
			if p.Lib != baseline {
				continue
			}
			yTop := math.Max(yLat(p.Lat), float64(plotTop))
			h := math.Max(float64(plotBottom)-yTop, 1)
			fmt.Fprintf(b, "  <rect x=\"%.1f\" y=\"%.1f\" width=\"%.1f\" height=\"%.1f\" rx=\"2\" fill=\"%s\" opacity=\"0.8\">%s</rect>\n",
				xl.barCenter(gi, pi)-bw/2, yTop, bw, h, LibraryColor(p.Lib), tooltip(p))
		}

		// Labeled bars, lowest bar top first. A bar past the axis top is
		// cut at the plot edge; the paper-colored fade over its stump
		// marks the cut and its label sits at the plot top.
		type latLabel struct {
			pi     int
			p      chartPoint
			rawTop float64 // bar top before clamping, drives placement order
			yTop   float64 // bar top for drawing
			cut    bool
		}
		var ls []latLabel
		for pi, p := range g.Points {
			if p.Lib == baseline {
				continue
			}
			raw := yLat(p.Lat)
			ls = append(ls, latLabel{pi: pi, p: p, rawTop: raw,
				yTop: math.Max(raw, float64(plotTop)), cut: raw < float64(plotTop)})
		}
		sort.SliceStable(ls, func(i, j int) bool { return ls[i].rawTop > ls[j].rawTop })

		type labelBox struct{ x0, x1, y0, y1 float64 }
		var placed []labelBox
		for _, l := range ls {
			p := l.p
			c := LibraryColor(p.Lib)
			cx := xl.barCenter(gi, l.pi)
			bar := fmt.Sprintf(`<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="2" fill="%s" opacity="0.8">%s</rect>`,
				cx-bw/2, l.yTop, bw, math.Max(float64(plotBottom)-l.yTop, 1), c, tooltip(p))
			if l.cut {
				bar += fmt.Sprintf(`<rect x="%.1f" y="%d" width="%.1f" height="14" fill="url(#cut-fade)" pointer-events="none"/>`,
					cx-bw/2, plotTop, bw)
			}
			val := strings.TrimSuffix(fmtRatio(p.Lat), "×")
			halfW := float64(len(val))*2 + 1
			offset := 3.0
			ly := float64(plotTop) - 4
			if !l.cut {
				for {
					circleHit := labelOverlapsCircle(gi, l.yTop, offset, cx, halfW)
					labelHit := false
					for _, bx := range placed {
						if cx-halfW < bx.x1 && bx.x0 < cx+halfW &&
							l.yTop-offset-7 < bx.y1 && bx.y0 < l.yTop-offset {
							labelHit = true
							break
						}
					}
					if !circleHit && !labelHit {
						break
					}
					if l.yTop-offset-8 <= float64(plotTop) {
						break
					}
					offset += 10
				}
				ly = l.yTop - offset
			}
			placed = append(placed, labelBox{cx - halfW, cx + halfW, ly - 7, ly})
			id := fmt.Sprintf("vl-%s-%s", g.Key, p.Lib)
			fmt.Fprintf(b, `  <g class="val-g" id="%s">`+"\n", id)
			valHit(b, id, bar, cx, ly-6)
			// A lifted label drifts from its bar; connect the two with a
			// thin dashed I-beam at the bar center. It carries no pointer
			// events so clicks pass through to the bar anchors.
			if offset > 3 {
				fmt.Fprintf(b, `    <line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="0.6" stroke-dasharray="1.5 2" opacity="0.45" pointer-events="none"/>`+"\n",
					cx, ly+2, cx, l.yTop, ColorText)
				fmt.Fprintf(b, `    <line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="0.6" opacity="0.45" pointer-events="none"/>`+"\n",
					cx-3, ly+2, cx+3, ly+2, ColorText)
				fmt.Fprintf(b, `    <line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="0.6" opacity="0.45" pointer-events="none"/>`+"\n",
					cx-3, l.yTop, cx+3, l.yTop, ColorText)
			}
			// The delta rides inline in the value's text as a colored
			// tspan, hidden until the label is toggled on.
			// The delta rides inline in the value's text as a colored
			// tspan; the opaque backdrop revealed with the enlarged
			// label keeps both legible over gridlines and marks.
			delta := ""
			dTxt := ""
			if d, up, ok := deltaText(p.Lat); ok {
				delta = fmt.Sprintf(`<tspan class="bar-val-delta" fill="%s">%s</tspan>`, deltaColor(up), d)
				dTxt = d
			}
			fmt.Fprintf(b, `    %s`+"\n", valBg(cx, ly, val+dTxt, false))
			fmt.Fprintf(b, `    <text x="%.1f" y="%.1f" class="bar-val" text-anchor="middle">%s%s</text>`+"\n",
				cx, ly, val, delta)
			fmt.Fprintf(b, `  </g>`+"\n")
			echo.add(id, fmt.Sprintf(`%s<text x="%.1f" y="%.1f" class="bar-val" text-anchor="middle">%s%s</text>`,
				valBg(cx, ly, val+dTxt, false), cx, ly, val, delta))
		}
	}

	// Memory circles, each stacked above its own library's bar: library
	// color fill for identity, ink stroke binding them to the B/op axis.
	// A click reveals the absolute B/op figure beside the circle; the
	// invisible larger circle enlarges the hit target. The dot and its
	// revealed figure are echoed into the topmost layer so later marks
	// cannot overpaint them.
	for gi, g := range sc.Groups {
		for pi, p := range g.Points {
			if math.IsNaN(p.Mem) {
				continue
			}
			cx, cy := xl.barCenter(gi, pi), yMem(p.Mem)
			id := fmt.Sprintf("vc-%s-%s", g.Key, p.Lib)
			fmt.Fprintf(b, `  <g class="val-g" id="%s">`+"\n", id)
			// The tooltip rides on the invisible hit circle so the visible
			// dot can carry no pointer events: the dot is painted above the
			// anchors and would otherwise swallow every click aimed at it.
			valHit(b, id,
				fmt.Sprintf(`<circle cx="%.1f" cy="%.1f" r="8" fill="none" pointer-events="all">%s</circle>`, cx, cy, tooltip(p)),
				cx, cy)
			fmt.Fprintf(b, `    <circle cx="%.1f" cy="%.1f" r="2.5" fill="%s" stroke="%s" stroke-width="1" pointer-events="none"/>`+"\n",
				cx, cy, LibraryColor(p.Lib), ColorAxisMem)
			bv := FormatBytes(p.BOp)
			fmt.Fprintf(b, `    %s`+"\n", valBg(cx+6, cy+3.5, bv, true))
			fmt.Fprintf(b, `    <text x="%.1f" y="%.1f" class="circ-val" text-anchor="start">%s</text>`+"\n",
				cx+6, cy+3.5, bv)
			fmt.Fprintf(b, `  </g>`+"\n")
			echo.add(id,
				fmt.Sprintf(`<circle cx="%.1f" cy="%.1f" r="2.5" fill="%s" stroke="%s" stroke-width="1"/>%s<text x="%.1f" y="%.1f" class="circ-val" text-anchor="start">%s</text>`,
					cx, cy, LibraryColor(p.Lib), ColorAxisMem, valBg(cx+6, cy+3.5, bv, true), cx+6, cy+3.5, bv))
		}
	}
}

// secHasAllocs reports whether any point in the section carries a
// defined allocs ratio, i.e. whether the allocs strip should be drawn.
func secHasAllocs(sc *secChart) bool {
	for _, g := range sc.Groups {
		for _, p := range g.Points {
			if !math.IsNaN(p.Allocs) {
				return true
			}
		}
	}
	return false
}

// renderAllocsStrip draws the allocs region below the label zone of the
// main plot: downward-tapering wedges on a log scale. The region shares
// the X coordinates of the main plot; the side axes run through from
// the plot so the two read as one figure. Allocation ratios span
// several orders of magnitude, so each decade takes equal depth and the
// wedge shape marks the scale as compressed relative to the linear
// latency bars.
func renderAllocsStrip(b *strings.Builder, xl xGeometry, plotBottom int, echo *echoStore) {
	sc := xl.sc
	x0, x1 := xl.x0, xl.x1
	stripTop := plotBottom + allocGap
	stripBottom := stripTop + allocH

	// Log decades covering every ratio in the section.
	dMin, dMax := math.MaxInt, math.MinInt
	for _, g := range sc.Groups {
		for _, p := range g.Points {
			if math.IsNaN(p.Allocs) || p.Allocs <= 0 {
				continue
			}
			lg := math.Log10(p.Allocs)
			dMin = min(dMin, int(math.Floor(lg)))
			dMax = max(dMax, int(math.Ceil(lg)))
		}
	}
	if dMax <= dMin {
		dMin, dMax = dMin-1, dMax+1
	}
	span := float64(dMax - dMin)
	// A value label sits 10px below its tip and each stacked neighbour
	// one further 10px dodge step. The decades map onto the full strip
	// height and hug the data; the stacked labels spill past the strip
	// bottom into the empty band reserved by allocCapH.
	yLog := func(v float64) float64 {
		return float64(stripTop) + (math.Log10(v)-float64(dMin))/span*float64(allocH)
	}

	// Gray caption under the lower axis, mirroring the latency caption
	// above the top axis.
	fmt.Fprintf(b, `  <text x="%d" y="%d" class="axis-cap" text-anchor="start">allocs/op × (log)</text>`+"\n",
		x0, stripBottom+20)

	// Gridlines + left ticks at each decade, increasing downward
	for d := dMin; d <= dMax; d++ {
		y := yLog(math.Pow10(d))
		if d > dMin {
			fmt.Fprintf(b, `  <line x1="%d" y1="%.1f" x2="%d" y2="%.1f" stroke="%s" stroke-width="1" opacity="0.35"/>`+"\n",
				x0, y, x1, y, ColorGrid)
		}
		fmt.Fprintf(b, `  <text x="%d" y="%.1f" class="tick-lat" text-anchor="end">%s</text>`+"\n",
			x0-8, y+3.5, fmtDecade(d))
	}

	// The side axes continue from the main plot through the label zone,
	// and the strip hangs from its own horizontal axis.
	fmt.Fprintf(b, `  <line x1="%d" y1="%d" x2="%d" y2="%d" stroke="%s" stroke-width="1.5"/>`+"\n",
		x0, plotBottom, x0, stripBottom, ColorAxisMem)
	fmt.Fprintf(b, `  <line x1="%d" y1="%d" x2="%d" y2="%d" stroke="%s" stroke-width="1.5"/>`+"\n",
		x1, plotBottom, x1, stripBottom, ColorAxisMem)
	fmt.Fprintf(b, `  <line x1="%d" y1="%d" x2="%d" y2="%d" stroke="%s" stroke-width="1.5"/>`+"\n",
		x0, stripTop, x1, stripTop, ColorDim)

	// Wedges with a filled tip, and the raw allocation count below each
	// tip. Labels are placed from the shallowest tip downward, the
	// mirror of the main plot: each takes the shallowest slot clearing
	// every label already placed in the cluster, so label order follows
	// tip order and the I-beams stay as short as the collisions allow.
	for gi, g := range sc.Groups {
		np := len(g.Points)
		if np == 0 {
			continue
		}
		base := xl.barWidth(np) + 4

		type allocLabel struct {
			pi  int
			p   chartPoint
			tip float64
		}
		var ls []allocLabel
		for pi, p := range g.Points {
			if math.IsNaN(p.Allocs) {
				continue
			}
			// Zero maps to negative infinity on the log scale; clamp its
			// tip at the strip top so a zero-alloc library still shows a
			// wedge and a 0 label instead of vanishing.
			tip := float64(stripTop) + 1
			if p.Allocs > 0 {
				tip = yLog(p.Allocs)
				if tip-float64(stripTop) < 1 {
					tip = float64(stripTop) + 1
				}
			}
			ls = append(ls, allocLabel{pi: pi, p: p, tip: tip})
		}
		sort.SliceStable(ls, func(i, j int) bool { return ls[i].tip < ls[j].tip })

		type labelBox struct{ x0, x1, y0, y1 float64 }
		var placed []labelBox
		for _, l := range ls {
			p := l.p
			cx := xl.barCenter(gi, l.pi)
			tip := l.tip
			// Truncated wedge: the flat tip keeps the shape from reading
			// as a needle and meets the tip circle cleanly.
			const tipHalf = 1.5
			fmt.Fprintf(b, `  <polygon points="%.1f,%d %.1f,%d %.1f,%.1f %.1f,%.1f" fill="%s" opacity="0.8">%s</polygon>`+"\n",
				cx-base/2, stripTop, cx+base/2, stripTop, cx+tipHalf, tip, cx-tipHalf, tip,
				LibraryColor(p.Lib), tooltip(p))
			fmt.Fprintf(b, `  <circle cx="%.1f" cy="%.1f" r="2.5" fill="%s"/>`+"\n",
				cx, tip, LibraryColor(p.Lib))

			val := FormatAllocs(p.AllocsOp)
			halfW := float64(len(val))*2 + 1
			offset := 10.0
			for {
				labelHit := false
				for _, bx := range placed {
					if cx-halfW < bx.x1 && bx.x0 < cx+halfW &&
						tip+offset-7 < bx.y1 && bx.y0 < tip+offset {
						labelHit = true
						break
					}
				}
				if !labelHit || tip+offset+8 > float64(stripBottom)+30 {
					break
				}
				offset += 10
			}
			ly := tip + offset
			placed = append(placed, labelBox{cx - halfW, cx + halfW, ly - 7, ly})
			id := fmt.Sprintf("va-%s-%s", g.Key, p.Lib)
			fmt.Fprintf(b, `  <g class="val-g" id="%s">`+"\n", id)
			// A dodged label drifts from its wedge; connect the two with a
			// thin dashed I-beam at the wedge center, the downward mirror
			// of the main plot leaders. It carries no pointer events so
			// clicks pass through to whatever sits beneath it.
			if offset > 10 {
				fmt.Fprintf(b, `    <line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="0.6" stroke-dasharray="1.5 2" opacity="0.45" pointer-events="none"/>`+"\n",
					cx, tip, cx, ly-7, ColorText)
				fmt.Fprintf(b, `    <line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="0.6" opacity="0.45" pointer-events="none"/>`+"\n",
					cx-3, tip, cx+3, tip, ColorText)
				fmt.Fprintf(b, `    <line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="0.6" opacity="0.45" pointer-events="none"/>`+"\n",
					cx-3, ly-7, cx+3, ly-7, ColorText)
			}
			valHit(b, id, fmt.Sprintf(`<rect x="%.1f" y="%.1f" width="24" height="18" fill="none" pointer-events="all"/>`,
				cx-12, ly-9), cx, ly-6)
			// The allocs delta rides inline like the latency one. A zero
			// ratio has no finite multiplier, its label already reads 0.
			delta := ""
			dTxt := ""
			if p.Allocs > 0 {
				if d, up, ok := deltaText(p.Allocs); ok {
					delta = fmt.Sprintf(`<tspan class="bar-val-delta" fill="%s">%s</tspan>`, deltaColor(up), d)
					dTxt = d
				}
			}
			fmt.Fprintf(b, `    %s`+"\n", valBg(cx, ly, val+dTxt, false))
			fmt.Fprintf(b, `    <text x="%.1f" y="%.1f" class="bar-val" text-anchor="middle">%s%s</text>`+"\n",
				cx, ly, val, delta)
			fmt.Fprintf(b, `  </g>`+"\n")
			echo.add(id, fmt.Sprintf(`%s<text x="%.1f" y="%.1f" class="bar-val" text-anchor="middle">%s%s</text>`,
				valBg(cx, ly, val+dTxt, false), cx, ly, val, delta))
		}
	}
}

// fmtDecade formats a power-of-ten tick value such as 1×, 10× or 0.1×.
func fmtDecade(d int) string {
	if d >= 0 {
		return fmt.Sprintf("%d×", int(math.Pow10(d)))
	}
	return fmt.Sprintf("0.%0*d×", -d, 1)
}

// renderXLabels draws the dataset name and payload size below the
// bottom-most plot of a section.
func renderXLabels(b *strings.Builder, xl xGeometry, bottomY int) {
	for gi, g := range xl.sc.Groups {
		cx := xl.slotCenter(gi)
		fmt.Fprintf(b, `  <text x="%.1f" y="%d" class="grp-label" text-anchor="middle">%s</text>`+"\n",
			cx, bottomY+18, esc(g.Label))
		if g.Payload > 0 {
			fmt.Fprintf(b, `  <text x="%.1f" y="%d" class="dim" font-size="10" text-anchor="middle">%s</text>`+"\n",
				cx, bottomY+32, FormatBytes(g.Payload))
		}
	}
}

// tooltip returns an SVG <title> element with the raw measurement behind a mark.
func tooltip(p chartPoint) string {
	mem := "n/a"
	if !math.IsNaN(p.Mem) {
		mem = fmtRatio(p.Mem)
	}
	allocs := "n/a"
	if !math.IsNaN(p.Allocs) {
		allocs = fmtRatio(p.Allocs)
	}
	return fmt.Sprintf("<title>%s: %s latency (%s), %s B/op (%s mem), %s allocs/op (%s allocs)</title>",
		esc(p.Lib), fmtRatio(p.Lat), FormatNsOp(p.NsOp), FormatBytes(p.BOp), mem,
		FormatAllocs(p.AllocsOp), allocs)
}

// latBulkCap estimates the latency axis extent from the bulk of the
// non-baseline ratios: their 85th percentile with headroom. One runaway
// library then overflows the axis instead of stretching it and crushing
// every other bar. With few samples the quantile approaches the plain
// maximum, which the headroom already covers.
func latBulkCap(ratios []float64) float64 {
	if len(ratios) == 0 {
		return 0
	}
	sort.Float64s(ratios)
	return quantile(ratios, 0.85) * 1.15
}

// quantile linearly interpolates a value from a slice sorted ascending.
func quantile(sorted []float64, q float64) float64 {
	pos := q * float64(len(sorted)-1)
	lo := int(pos)
	hi := min(lo+1, len(sorted)-1)
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

// axisScale picks a nice tick step and a top value covering minTop.
func axisScale(minTop float64) (step, top float64) {
	if minTop <= 0 {
		return 1, 1
	}
	step = niceStep(minTop / 5)
	top = math.Ceil(minTop/step-1e-9) * step
	return step, top
}

// niceStep rounds v up to a 1/2/2.5/5×10^k mantissa.
func niceStep(v float64) float64 {
	if v <= 0 {
		return 1
	}
	pow := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if m*pow >= v-1e-12 {
			return m * pow
		}
	}
	return 10 * pow
}

// echoStore collects the topmost paint layer for toggled values. A
// toggled group's enlarged content can be overpainted by marks rendered
// after it (later circles, value labels), and without script there is
// no way to re-parent it to the top. Each group therefore also drops an
// echo, a copy of its visible content appended after everything else,
// hidden by default and revealed by a generated sibling selector
// (#id:target ~ #echo-id). Echoes carry no pointer events, so clicks
// keep reaching the anchors at the value's original position.
type echoStore struct {
	ids  []string
	body strings.Builder
}

func (es *echoStore) add(id, content string) {
	es.ids = append(es.ids, id)
	fmt.Fprintf(&es.body, `  <g class="echo-g" id="echo-%s">%s</g>`+"\n", id, content)
}

// flush emits the per-group reveal rules and the echo bodies, which sit
// after every other element so they always paint on top.
func (es *echoStore) flush(b *strings.Builder) {
	if len(es.ids) == 0 {
		return
	}
	b.WriteString(`  <style>` + "\n")
	for _, id := range es.ids {
		fmt.Fprintf(b, "    #%s:target ~ #echo-%s { display: inline; }\n", id, id)
	}
	b.WriteString(`  </style>` + "\n")
	b.WriteString(es.body.String())
}

// valHit emits the anchor pair that toggles a value group without
// script, so the chart stays interactive where embedded scripts are
// blocked (raw.githubusercontent.com sandboxes the document). The
// activation anchor targets the group's own id and drives the :target
// styles; the closing anchor targets a 1×1 invisible stop element at
// the same position, which deactivates the group without scrolling the
// document the way a jump to the svg root would. The stop element and
// both anchors share the hit shape, which may be an invisible
// pointer-events rect or circle, or the visible mark itself (a bar).
// Every other element of the group carries no pointer events so clicks
// on the enlarged value reach the anchors.
func valHit(b *strings.Builder, id, shape string, x, y float64) {
	fmt.Fprintf(b, `    <a href="#%s">%s</a>`+"\n", id, shape)
	fmt.Fprintf(b, `    <a class="val-off" href="#%sx">%s</a>`+"\n", id, shape)
	fmt.Fprintf(b, `    <rect id="%sx" x="%.1f" y="%.1f" width="1" height="1" fill="none" pointer-events="none"/>`+"\n",
		id, x, y)
}

// valBg returns the opaque backdrop revealed behind a toggled value
// label, keeping the enlarged text legible over gridlines and
// neighbouring marks. cx is the label anchor (middle or start), y its
// baseline, text the revealed string.
func valBg(cx, y float64, text string, anchorStart bool) string {
	w := textW(text) + 6
	x := cx - w/2
	if anchorStart {
		x = cx - 3
	}
	return fmt.Sprintf(`<rect class="val-bg" x="%.1f" y="%.1f" width="%.1f" height="13.5" pointer-events="none"/>`,
		x, y-10, w)
}

// textW estimates the advance of a value label at the 11px sans face.
// Tabular-nums digits advance a fixed 0.6em; every other rune
// backdrop never under-covers the glyphs.
func textW(s string) float64 {
	w := 0.0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			w += 6.6
		case r == '.':
			w += 3.5
		case r == '×':
			w += 6.6
		case r == '↑' || r == '↓':
			w += 7.7
		default:
			w += 7.3
		}
	}
	return w
}

// deltaText renders the click-to-reveal improvement figure for a
// lower-is-better ratio: an up arrow plus the multiplier when the value
// beats the baseline, a down arrow plus the multiplier when it loses.
func deltaText(ratio float64) (text string, up, ok bool) {
	switch {
	case ratio < 1-1e-9:
		return "↑" + fmtRatio(1/ratio), true, true
	case ratio > 1+1e-9:
		return "↓" + fmtRatio(ratio), false, true
	}
	return "", false, false
}

// deltaColor colors an improvement green and a regression red.
func deltaColor(up bool) string {
	if up {
		return ColorDeltaUp
	}
	return ColorDeltaDown
}

// fmtRatio formats a ratio value such as 0.25, 1.8 or 42 with an × suffix.
func fmtRatio(v float64) string {
	var s string
	switch {
	case v >= 100:
		s = fmt.Sprintf("%.0f", v)
	case v >= 10:
		s = fmt.Sprintf("%.1f", v)
	default:
		s = fmt.Sprintf("%.2f", v)
	}
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" {
		s = "0"
	}
	return s + "×"
}

func esc(s string) string {
	return html.EscapeString(s)
}
