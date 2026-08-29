package main

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// libraryDef defines a known library with its display order, bar color, etc.
type libraryDef struct {
	Name  string `yaml:"name"`  // benchmark suffix (e.g. "Sonic", "GoJSON")
	Color string `yaml:"color"` // bar fill color
}

type configFile struct {
	Libraries          []libraryDef `yaml:"libraries"`
	StripDatasetPrefix []string     `yaml:"strip_dataset_prefix"`
}

// datasetPrefixes lists corpus-family prefixes stripped from dataset
// names for display labels (e.g. "JSONBenchStringUnicode" -> "StringUnicode").
var datasetPrefixes []string

const configFileName = ".benchviz.yaml"

// findUp searches for configFileName in dir and each of its parents,
// returning the first match or "".
func findUp(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	for {
		candidate := filepath.Join(dir, configFileName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// DiscoverConfig finds the config file using the following priority:
//  1. explicit (non-empty) path from -config flag: use as-is, error if unreadable
//  2. .benchviz.yaml in the input file's directory or any of its parents
//  3. .benchviz.yaml in the current working directory or any of its parents
//
// Returns "" if no config file is found in the auto-discovery cases.
func DiscoverConfig(explicit string, inputFile string) string {
	if explicit != "" {
		return explicit
	}
	if inputFile != "" {
		if p := findUp(filepath.Dir(inputFile)); p != "" {
			return p
		}
	}
	return findUp(".")
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
	datasetPrefixes = cfg.StripDatasetPrefix
	return nil
}

// knownLibraries lists the recognized library suffixes in display order.
// This is the single source of truth for library ordering and colors.
var knownLibraries = []libraryDef{
	{Name: "Sonic", Color: "#3498db"},  // blue
	{Name: "GoJSON", Color: "#8e44ad"}, // purple
	{Name: "JSONv2", Color: "#e67e22"}, // orange
	{Name: "Velox", Color: "#27ae60"},  // green, this project (last = protagonist)
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

// SVG style constants. Titles and both axes share one dark ink; a
// shared axis color cannot be mistaken for a library bar color.
const (
	ColorTitle     = "#2c3e50"
	ColorSubtitle  = "#7f8c8d"
	ColorText      = "#555"
	ColorDim       = "#95a5a6"
	ColorGrid      = "#dee2e6"
	ColorPaper     = "#fdf6e3"
	ColorDeltaUp   = "#27ae60" // improvement delta
	ColorDeltaDown = "#e74c3c"
	ColorAxisMem   = "#2c3e50"

	FontMono = "Menlo, Consolas, 'Liberation Mono', monospace"
	FontSans = "-apple-system, 'Helvetica Neue', Helvetica, Arial, sans-serif"
)
