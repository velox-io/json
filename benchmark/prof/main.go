package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"

	"dev.local/benchmark"

	"github.com/urfave/cli/v3"
	vjson "github.com/velox-io/json"
)

type EscapeHeavyPayload = benchmark.EscapeHeavyPayload

const N = 5_000_000

var localDir = "local"

func profPath(name string) string {
	return filepath.Join(localDir, name)
}

func cpuprof() {
	escapeHeavyJSON := benchmark.EscapeHeavyJSON

	// Warm up decoder cache
	var warmup EscapeHeavyPayload
	_ = vjson.Unmarshal(escapeHeavyJSON, &warmup)

	// CPU profile
	f, err := os.Create(profPath("cpu.prof"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	pprof.StartCPUProfile(f)
	defer pprof.StopCPUProfile()

	// Hot loop
	for range N {
		var s EscapeHeavyPayload
		if err := vjson.Unmarshal(escapeHeavyJSON, &s); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	fmt.Printf("done: %d iterations, wrote %s\n", N, profPath("cpu.prof"))
}

func memprof() {
	escapeHeavyJSON := benchmark.EscapeHeavyJSON

	// Warm up decoder cache
	var warmup EscapeHeavyPayload
	_ = vjson.Unmarshal(escapeHeavyJSON, &warmup)

	// Force GC to get a clean baseline
	runtime.GC()

	// Set MemProfileRate to 1 to record every allocation
	runtime.MemProfileRate = 1

	// Hot loop
	for range N {
		var s EscapeHeavyPayload
		if err := vjson.Unmarshal(escapeHeavyJSON, &s); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	// Write heap profile
	f, err := os.Create(profPath("mem.prof"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()

	runtime.GC() // materialize all stats
	if err := pprof.WriteHeapProfile(f); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Printf("done: %d iterations, wrote %s\n", N, profPath("mem.prof"))
}

func main() {
	app := &cli.Command{
		Name:  "jsonprof",
		Usage: "JSON unmarshal profiling tool",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "dir",
				Value: "local",
				Usage: "output directory for profile files",
			},
		},
		Commands: []*cli.Command{
			{
				Name:  "cpu",
				Usage: "run CPU profile",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					localDir = cmd.Root().String("dir")
					os.MkdirAll(localDir, 0o755)
					cpuprof()
					return nil
				},
			},
			{
				Name:  "mem",
				Usage: "run memory profile",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					localDir = cmd.Root().String("dir")
					os.MkdirAll(localDir, 0o755)
					memprof()
					return nil
				},
			},
		},
	}
	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
