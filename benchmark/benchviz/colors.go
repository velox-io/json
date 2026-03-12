package main

// Color palette matching the decoder-buffer-analysis.svg style.

// LibraryColor returns the bar fill color for a given library name.
func LibraryColor(lib string) string {
	switch lib {
	case "Velox":
		return "#27ae60" // green — this project
	case "Sonic":
		return "#3498db" // blue
	case "StdJSON":
		return "#e67e22" // orange
	case "GoJSON":
		return "#8e44ad" // purple
	case "EasyJSON":
		return "#e74c8b" // pink
	default:
		return "#95a5a6" // grey — unknown
	}
}

// LibraryColorLight returns a lighter variant for background/hover.
func LibraryColorLight(lib string) string {
	switch lib {
	case "Velox":
		return "#eafaf1"
	case "Sonic":
		return "#ebf5fb"
	case "StdJSON":
		return "#fef5e7"
	case "GoJSON":
		return "#f5eef8"
	case "EasyJSON":
		return "#fdeef4"
	default:
		return "#eef0f1"
	}
}

// SVG style constants.
const (
	ColorTitle    = "#2c3e50"
	ColorSubtitle = "#7f8c8d"
	ColorText     = "#555"
	ColorTextBold = "#2c3e50"
	ColorDim      = "#95a5a6"
	ColorCardBg   = "#f8f9fa"
	ColorCardBdr  = "#dee2e6"
	ColorFastest  = "#27ae60"
	ColorSlowest  = "#e74c3c"
	ColorWarn     = "#e67e22"

	FontMono = "Menlo, Consolas, 'Liberation Mono', monospace"
	FontSans = "-apple-system, 'Helvetica Neue', Helvetica, Arial, sans-serif"
)
