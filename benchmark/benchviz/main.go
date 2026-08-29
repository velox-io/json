package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
)

func main() {
	title := flag.String("title", "", "chart title (auto-detected if empty)")
	format := flag.String("format", "svg", "output format: svg")
	config := flag.String("config", "", "path to library config file (auto-discovered if empty)")
	baseline := flag.String("baseline", "JSONv2", "baseline library for ratio normalization")
	exclude := flag.String("exclude", "", "regexp of group names to exclude from the chart")
	flag.Parse()

	var excludeRe *regexp.Regexp
	if *exclude != "" {
		var err error
		excludeRe, err = regexp.Compile(*exclude)
		if err != nil {
			fmt.Fprintf(os.Stderr, "benchviz: -exclude: %v\n", err)
			os.Exit(1)
		}
	}

	// Determine the input source: positional arg or stdin.
	var inputFile string
	input := os.Stdin
	if flag.NArg() > 0 {
		inputFile = flag.Arg(0)
		f, err := os.Open(inputFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "benchviz: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		input = f
	}

	// Auto-discover or use explicit config path.
	cfgPath := DiscoverConfig(*config, inputFile)
	if err := LoadConfig(cfgPath); err != nil {
		fmt.Fprintf(os.Stderr, "benchviz: load config: %v\n", err)
		os.Exit(1)
	}

	data, err := ParseBenchOutput(input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "benchviz: %v\n", err)
		os.Exit(1)
	}

	if *title != "" {
		data.Title = *title
	} else if len(data.Sections) == 1 && data.Sections[0].Name != "" {
		data.Title = fmt.Sprintf("Benchmark (%s) Results", data.Sections[0].Name)
	}

	if *format != "svg" {
		fmt.Fprintf(os.Stderr, "benchviz: unknown format %q (use svg)\n", *format)
		os.Exit(1)
	}
	fmt.Print(RenderSVG(data, *baseline, excludeRe))
}
