package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	title := flag.String("title", "", "chart title (auto-detected if empty)")
	flag.Parse()

	data, err := ParseBenchOutput(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "benchviz: %v\n", err)
		os.Exit(1)
	}

	if *title != "" {
		data.Title = *title
	}

	svg := RenderSVG(data)
	fmt.Print(svg)
}
