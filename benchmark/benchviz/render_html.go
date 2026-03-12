package main

import (
	"fmt"
	"math"
	"strings"
	"text/template"
)

// --- view model types ---

type htmlMetric struct {
	WidthPct  float64 // bar width as percentage of max (0–100)
	Value     string  // formatted display value
	IsFastest bool    // lowest (best) in group
	Ratio     string  // e.g. "2.1x" or "" if fastest/not applicable
}

type htmlRow struct {
	Library  string
	Color    string
	NsOp    htmlMetric
	BOp     htmlMetric
	AllocsOp htmlMetric
}

type htmlGroup struct {
	Name string // display name (underscores replaced with spaces)
	Rows []htmlRow
}

type htmlSection struct {
	Name   string
	Groups []htmlGroup
}

type htmlData struct {
	Title      string
	Subtitle   string
	Libs       []htmlLib
	Sections   []htmlSection
	Collapsible bool // true when >1 section
	RunCount   int
}

type htmlLib struct {
	Name  string
	Color string
}

// --- view model builder ---

func buildHTMLData(data *BenchData) *htmlData {
	sections := data.Sections
	hasSections := len(sections) > 1
	if len(sections) == 0 {
		sections = []Section{{Name: "", Groups: data.Groups}}
		hasSections = false
	}

	var htmlSections []htmlSection
	for _, sec := range sections {
		hs := htmlSection{Name: sec.Name}
		for _, group := range sec.Groups {
			displayName := group
			if hasSections {
				_, ds := splitGroupToSection(group)
				displayName = ds
			}
			displayName = strings.ReplaceAll(displayName, "_", " ")
			hs.Groups = append(hs.Groups, buildHTMLGroup(data, group, displayName))
		}
		htmlSections = append(htmlSections, hs)
	}

	libs := make([]htmlLib, len(data.Libs))
	for i, lib := range data.Libs {
		libs[i] = htmlLib{Name: lib, Color: LibraryColor(lib)}
	}

	return &htmlData{
		Title:       data.Title,
		Subtitle:    data.Subtitle,
		Libs:        libs,
		Sections:    htmlSections,
		Collapsible: hasSections,
		RunCount:    data.RunCount,
	}
}

func buildHTMLGroup(data *BenchData, group, displayName string) htmlGroup {
	gr := data.Results[group]

	// Compute min/max per metric across libraries in this group.
	minNs, maxNs := math.MaxFloat64, 0.0
	minB, maxB := math.MaxFloat64, 0.0
	minA, maxA := math.MaxFloat64, 0.0
	for _, lib := range data.Libs {
		r, ok := gr[lib]
		if !ok {
			continue
		}
		minNs = math.Min(minNs, r.NsOp)
		maxNs = math.Max(maxNs, r.NsOp)
		minB = math.Min(minB, r.BOp)
		maxB = math.Max(maxB, r.BOp)
		minA = math.Min(minA, r.AllocsOp)
		maxA = math.Max(maxA, r.AllocsOp)
	}

	var rows []htmlRow
	for _, lib := range data.Libs {
		r, ok := gr[lib]
		if !ok {
			continue
		}
		rows = append(rows, htmlRow{
			Library:  lib,
			Color:    LibraryColor(lib),
			NsOp:     buildMetric(r.NsOp, maxNs, minNs, FormatNsOp(r.NsOp), true),
			BOp:      buildMetric(r.BOp, maxB, minB, FormatBytes(r.BOp), false),
			AllocsOp: buildMetric(r.AllocsOp, maxA, minA, FormatAllocs(r.AllocsOp), false),
		})
	}
	return htmlGroup{Name: displayName, Rows: rows}
}

func buildMetric(val, maxVal, minVal float64, formatted string, showRatio bool) htmlMetric {
	// Cap bar at 60% of the cell width so the annotation text always fits beside it.
	const maxBarPct = 60.0
	pct := 0.0
	if maxVal > 0 {
		pct = val / maxVal * maxBarPct
		if pct < 1.5 {
			pct = 1.5
		}
	}
	isFastest := minVal > 0 && val <= minVal*1.03
	ratio := ""
	if showRatio && !isFastest && minVal > 0 {
		ratio = fmt.Sprintf("%.1fx", val/minVal)
	}
	// For non-ratio columns, mark lowest with star if there's meaningful difference.
	if !showRatio && minVal >= 0 && val <= minVal*1.005 && maxVal > minVal*1.01 {
		isFastest = true
	}
	return htmlMetric{
		WidthPct:  pct,
		Value:     formatted,
		IsFastest: isFastest,
		Ratio:     ratio,
	}
}

// --- template rendering ---

// RenderHTML generates a self-contained HTML page from parsed benchmark data.
func RenderHTML(data *BenchData) string {
	hd := buildHTMLData(data)
	var b strings.Builder
	if err := htmlTpl.Execute(&b, hd); err != nil {
		panic(fmt.Sprintf("benchviz: template error: %v", err))
	}
	return b.String()
}

var htmlTpl = template.Must(template.New("bench").Parse(htmlTemplate))

const htmlTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
  :root {
    --font-sans: ` + FontSans + `;
    --font-mono: ` + FontMono + `;
    --color-title: ` + ColorTitle + `;
    --color-subtitle: ` + ColorSubtitle + `;
    --color-text: ` + ColorText + `;
    --color-dim: ` + ColorDim + `;
    --color-card-bg: ` + ColorCardBg + `;
    --color-card-bdr: ` + ColorCardBdr + `;
    --color-fastest: ` + ColorFastest + `;
    --color-slowest: ` + ColorSlowest + `;
  }
  *, *::before, *::after { box-sizing: border-box; }
  body {
    margin: 0; padding: 24px;
    font-family: var(--font-mono);
    font-size: 13px;
    color: var(--color-text);
    background: #fff;
    max-width: 1200px;
    margin: 0 auto;
    padding: 24px 16px;
  }
  h1 {
    font-family: var(--font-sans);
    font-size: 22px; font-weight: bold;
    color: var(--color-title);
    text-align: center; margin: 0 0 4px;
  }
  .subtitle {
    font-family: var(--font-sans);
    font-size: 13px; color: var(--color-subtitle);
    text-align: center; margin: 0 0 16px;
  }
  /* Legend */
  .legend {
    display: flex; justify-content: center; gap: 18px;
    margin-bottom: 20px; flex-wrap: wrap;
  }
  .legend-item {
    display: flex; align-items: center; gap: 5px;
    cursor: pointer; user-select: none;
    transition: opacity 0.15s;
  }
  .legend-item.dimmed { opacity: 0.35; text-decoration: line-through; }
  .legend-swatch {
    width: 14px; height: 14px; border-radius: 3px; flex-shrink: 0;
  }
  .legend-label {
    font-size: 12px; font-weight: bold;
  }
  /* Section */
  details { margin-bottom: 8px; }
  summary {
    font-family: var(--font-sans);
    font-size: 16px; font-weight: bold;
    color: var(--color-title);
    cursor: pointer; padding: 6px 0;
    border-bottom: 1px solid var(--color-card-bdr);
    margin-bottom: 8px;
    letter-spacing: 0.5px;
  }
  summary::-webkit-details-marker { margin-right: 6px; }
  .section-title {
    font-family: var(--font-sans);
    font-size: 16px; font-weight: bold;
    color: var(--color-title);
    padding: 6px 0;
    border-bottom: 1px solid var(--color-card-bdr);
    margin-bottom: 8px;
    letter-spacing: 0.5px;
  }
  /* Group card */
  .group-card {
    background: var(--color-card-bg);
    border: 1px solid var(--color-card-bdr);
    border-radius: 8px;
    padding: 12px 14px 8px;
    margin-bottom: 12px;
  }
  .group-title {
    font-family: var(--font-sans);
    font-size: 14px; font-weight: bold;
    color: var(--color-title);
    margin: 0 0 8px;
  }
  /* Grid */
  .metric-grid {
    display: grid;
    grid-template-columns: 80px 1fr 0.6fr 0.4fr;
    column-gap: 24px;
  }
  .col-hdr {
    font-family: var(--font-sans);
    font-size: 10px; font-weight: 600;
    color: var(--color-dim);
    letter-spacing: 0.3px;
    padding-bottom: 4px;
  }
  .col-hdr-hint {
    font-style: italic;
  }
  /* Row */
  .metric-row {
    display: contents;
  }
  .metric-row.hidden { display: none; }
  .lib-label {
    font-size: 11px; font-weight: bold;
    display: flex; align-items: center;
    padding: 2px 0;
    white-space: nowrap;
  }
  .metric-cell {
    display: flex; align-items: center; gap: 5px;
    padding: 2px 0;
    min-height: 24px;
  }
  .bar {
    height: 20px; border-radius: 3px;
    opacity: 0.80; flex-shrink: 0;
  }
  .bar-annot {
    flex-shrink: 0;
    display: flex; align-items: center; gap: 3px;
    white-space: nowrap;
  }
  .bar-val {
    font-size: 10.5px; color: var(--color-text);
    white-space: nowrap;
  }
  .badge-fast {
    font-size: 9.5px; font-weight: bold;
    color: var(--color-fastest);
  }
  .badge-ratio {
    font-size: 9.5px; font-weight: bold;
    color: var(--color-slowest);
  }
  /* Footer */
  .footer {
    text-align: center; font-size: 11px;
    color: var(--color-dim); margin-top: 16px;
  }
</style>
</head>
<body>

<h1>{{if .Title}}{{.Title}}{{else}}Benchmark Results{{end}}</h1>
{{if .Subtitle}}<p class="subtitle">{{.Subtitle}}</p>{{end}}

<div class="legend" id="legend">
{{range .Libs}}  <span class="legend-item" data-lib="{{.Name}}" onclick="toggleLib(this)">
    <span class="legend-swatch" style="background:{{.Color}}"></span>
    <span class="legend-label" style="color:{{.Color}}">{{.Name}}</span>
  </span>
{{end}}</div>

{{range .Sections}}
{{if $.Collapsible}}<details open>
<summary>{{.Name}}</summary>
{{else}}{{if .Name}}<div class="section-title">{{.Name}}</div>{{end}}
{{end}}
{{range .Groups}}
<div class="group-card">
  <div class="group-title">{{.Name}}</div>
  <div class="metric-grid">
    <div class="col-hdr"></div>
    <div class="col-hdr">ns/op <span class="col-hdr-hint">lower is better ↓</span></div>
    <div class="col-hdr">B/op</div>
    <div class="col-hdr">allocs/op</div>
    {{range .Rows}}
    <div class="metric-row" data-lib="{{.Library}}">
      <div class="lib-label" style="color:{{.Color}}">{{.Library}}</div>
      <div class="metric-cell">
        <div class="bar" style="width:{{printf "%.1f" .NsOp.WidthPct}}%;background:{{.Color}}"></div>
        <span class="bar-annot"><span class="bar-val">{{.NsOp.Value}}</span>
        {{if .NsOp.IsFastest}}<span class="badge-fast">★ fastest</span>
        {{else if .NsOp.Ratio}}<span class="badge-ratio">{{.NsOp.Ratio}}</span>{{end}}</span>
      </div>
      <div class="metric-cell">
        <div class="bar" style="width:{{printf "%.1f" .BOp.WidthPct}}%;background:{{.Color}}"></div>
        <span class="bar-annot"><span class="bar-val">{{.BOp.Value}}</span>
        {{if .BOp.IsFastest}}<span class="badge-fast">★</span>{{end}}</span>
      </div>
      <div class="metric-cell">
        <div class="bar" style="width:{{printf "%.1f" .AllocsOp.WidthPct}}%;background:{{.Color}}"></div>
        <span class="bar-annot"><span class="bar-val">{{.AllocsOp.Value}}</span>
        {{if .AllocsOp.IsFastest}}<span class="badge-fast">★</span>{{end}}</span>
      </div>
    </div>
    {{end}}
  </div>
</div>
{{end}}
{{if $.Collapsible}}</details>{{end}}
{{end}}

<div class="footer">Generated by benchviz{{if .RunCount}}  |  count={{.RunCount}} (median){{end}}</div>

<script>
function toggleLib(el) {
  var lib = el.getAttribute("data-lib");
  el.classList.toggle("dimmed");
  var hidden = el.classList.contains("dimmed");
  var rows = document.querySelectorAll('.metric-row[data-lib="'+lib+'"]');
  for (var i = 0; i < rows.length; i++) {
    rows[i].classList.toggle("hidden", hidden);
  }
}
</script>
</body>
</html>
`
