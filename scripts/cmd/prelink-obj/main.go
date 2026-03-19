// prelink-obj converts a shared library (ELF .so or Mach-O dylib) into a
// relocatable object with zero relocations.
//
// This unified tool replaces both so-to-obj (ELF) and dylib_to_obj.py (Mach-O).
// Format is auto-detected by magic bytes.
//
// Usage:
//
//	prelink-obj [flags] -o <output> <input>
//
// Flags:
//
//	-o <file>           Output file (required)
//	-export-prefix <s>  Demote non-matching symbols to local (ELF only)
//	-q                  Quiet mode
package main

import (
	"fmt"
	"os"
	"strings"
)

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: prelink-obj [flags] -o <output> <input>

Converts a shared library (.so or .dylib) into a relocatable object
with zero relocations. Format is auto-detected by magic bytes.

Flags:
  -o <file>           Output file (required)
  -export-prefix <s>  Demote non-matching symbols to local (ELF only)
  -q                  Quiet mode
`)
	os.Exit(2)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "prelink-obj: "+format+"\n", args...)
	os.Exit(1)
}

// detectFormat reads the first 4 bytes to determine the input format.
func detectFormat(path string) string {
	f, err := os.Open(path)
	if err != nil {
		fatalf("cannot open input: %v", err)
	}
	defer f.Close()

	var magic [4]byte
	if _, err := f.Read(magic[:]); err != nil {
		fatalf("cannot read magic bytes: %v", err)
	}

	// ELF: \x7fELF
	if magic[0] == 0x7F && magic[1] == 'E' && magic[2] == 'L' && magic[3] == 'F' {
		return "elf"
	}
	// Mach-O 64-bit: 0xFEEDFACF (little-endian)
	if magic[0] == 0xCF && magic[1] == 0xFA && magic[2] == 0xED && magic[3] == 0xFE {
		return "macho"
	}

	fatalf("unrecognized file format (magic: %02X %02X %02X %02X)", magic[0], magic[1], magic[2], magic[3])
	return ""
}

func main() {
	var output string
	var input string
	var exportPrefix string
	quiet := false

	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-o":
			i++
			if i >= len(args) {
				fatalf("-o requires an argument")
			}
			output = args[i]
		case "-export-prefix":
			i++
			if i >= len(args) {
				fatalf("-export-prefix requires an argument")
			}
			exportPrefix = args[i]
		case "-q":
			quiet = true
		case "-h", "--help":
			usage()
		default:
			if args[i][0] == '-' {
				fatalf("unknown option: %s", args[i])
			}
			if input != "" {
				fatalf("only one input file allowed, got: %s and %s", input, args[i])
			}
			input = args[i]
		}
	}

	if output == "" {
		fatalf("missing -o <output>")
	}
	if input == "" {
		fatalf("missing input file")
	}
	if _, err := os.Stat(input); err != nil {
		fatalf("input file not found: %s", input)
	}

	// Step 1: detect format by magic bytes
	format := detectFormat(input)

	// Step 2: extract blob + symbols
	var result *ExtractResult
	var err error

	switch format {
	case "elf":
		result, err = extractFromELF(input)
	case "macho":
		result, err = extractFromMachO(input)
	}
	if err != nil {
		fatalf("%v", err)
	}

	if len(result.Blob) == 0 {
		fatalf("empty blob in input")
	}
	if len(result.Syms) == 0 {
		fatalf("no symbols found in input")
	}

	// Step 3: ADRP patching (ARM64 only)
	if result.IsARM64 {
		patched, err := patchADRPtoADR(result.Blob, result.CodeExtent, result.BlobExtent, quiet)
		if err != nil {
			fatalf("ADRP patching failed: %v", err)
		}
		result.Blob = patched
	}

	// Step 4: export-prefix demote (ELF only)
	if exportPrefix != "" && format == "elf" {
		for i := range result.Syms {
			if !result.Syms[i].Local && !strings.HasPrefix(result.Syms[i].Name, exportPrefix) {
				result.Syms[i].Local = true
			}
		}
	}

	// Verify at least one global symbol exists
	hasGlobal := false
	for _, s := range result.Syms {
		if !s.Local {
			hasGlobal = true
			break
		}
	}
	if !hasGlobal {
		fatalf("no global symbols found after filtering")
	}

	// Step 5: write output
	switch format {
	case "elf":
		if err := writeRelocatableELF(output, result.Blob, result.Syms, result.ELFMachine); err != nil {
			fatalf("writing output: %v", err)
		}
	case "macho":
		if err := writeMachOObject(output, result.Blob, result.Syms, result.BuildVer); err != nil {
			fatalf("writing output: %v", err)
		}
	}

	// Step 6: verify (ELF only — Mach-O verification would need debug/macho which lacks needed features)
	if format == "elf" {
		if err := verifyELFOutput(output); err != nil {
			fatalf("verification failed: %v", err)
		}
	}

	if !quiet {
		fmt.Printf("prelink-obj: %s (%s, %d bytes, %d symbols, 0 relocations)\n",
			output, format, len(result.Blob), len(result.Syms))
	}
}
