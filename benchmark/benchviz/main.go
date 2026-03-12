package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	title := flag.String("title", "", "chart title (auto-detected if empty)")
	format := flag.String("format", "svg", "output format: svg or html")
	flag.Parse()

	data, err := ParseBenchOutput(os.Stdin)
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
