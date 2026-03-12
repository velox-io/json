package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	title := flag.String("title", "", "chart title (auto-detected if empty)")
	format := flag.String("format", "svg", "output format: svg or html")
	config := flag.String("config", "", "path to library config file (auto-discovered if empty)")
	flag.Parse()

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
	}

	switch *format {
	case "svg":
		fmt.Print(RenderSVG(data))
	case "html":
		fmt.Print(RenderHTML(data))
	default:
		fmt.Fprintf(os.Stderr, "benchviz: unknown format %q (use svg or html)\n", *format)
		os.Exit(1)
	}
}
