package main

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// libraryDef defines a known library with its display order, bar color, etc.
type libraryDef struct {
	Name  string `yaml:"name"`  // benchmark suffix (e.g. "StdJSON", "Sonic")
	Color string `yaml:"color"` // bar fill color
}

type configFile struct {
	Libraries []libraryDef `yaml:"libraries"`
}

const configFileName = ".benchviz.yaml"

// DiscoverConfig finds the config file using the following priority:
//  1. explicit (non-empty) path from -config flag — use as-is, error if unreadable
//  2. .benchviz.yaml in the input file's directory (when input is a file)
//  3. .benchviz.yaml in the current working directory
//
// Returns "" if no config file is found in the auto-discovery cases.
func DiscoverConfig(explicit string, inputFile string) string {
	if explicit != "" {
		return explicit
	}
	if inputFile != "" {
		candidate := filepath.Join(filepath.Dir(inputFile), configFileName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	if _, err := os.Stat(configFileName); err == nil {
		return configFileName
	}
	return ""
}

// LoadConfig reads a YAML config file and overrides knownLibraries.
// If path is empty, the hardcoded defaults are kept silently.
func LoadConfig(path string) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg configFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return err
	}
	if len(cfg.Libraries) > 0 {
		knownLibraries = cfg.Libraries
	}
	return nil
}

// knownLibraries lists the recognized library suffixes in display order.
// This is the single source of truth for library ordering and colors.
var knownLibraries = []libraryDef{
	{Name: "StdJSON", Color: "#e67e22"},   // orange
	{Name: "Sonic", Color: "#3498db"},     // blue
	{Name: "Segmentio", Color: "#34495e"}, // slate
	{Name: "GoJSON", Color: "#8e44ad"},    // purple
	{Name: "EasyJSON", Color: "#e74c8b"},  // pink
	{Name: "Velox", Color: "#27ae60"},     // green — this project (last = protagonist)
}

// fallbackColors is a palette for libraries not in knownLibraries.
var fallbackColors = []string{
	"#d35400", // pumpkin
	"#2980b9", // belize
	"#8e44ad", // wisteria
	"#c0392b", // pomegranate
	"#16a085", // green-sea
}

// knownLibNames returns just the name strings (used by the parser).
func knownLibNames() []string {
	names := make([]string, len(knownLibraries))
	for i, l := range knownLibraries {
		names[i] = l.Name
	}
	return names
}

// LibraryColor returns the bar fill color for a given library name.
func LibraryColor(lib string) string {
	for _, kl := range knownLibraries {
		if kl.Name == lib {
			return kl.Color
		}
	}
	// Deterministic fallback: hash the name to pick from the palette.
	h := 0
	for _, c := range lib {
		h = h*31 + int(c)
	}
	if h < 0 {
		h = -h
	}
	return fallbackColors[h%len(fallbackColors)]
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
