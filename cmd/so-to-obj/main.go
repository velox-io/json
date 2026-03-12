// so-to-obj extracts a relocatable ELF object (ET_REL) from a shared object (ET_DYN).
//
// This tool performs ET_DYN → ET_REL conversion, which is needed because:
//
//   - Go's internal linker requires syso files to be ET_REL (relocatable object)
//   - Go's internal linker cannot handle R_X86_64_PC32 relocations that reference
//     .rodata sections in ET_REL files
//   - To resolve relocations, we must use ld -shared (produces ET_DYN)
//   - But Go needs ET_REL, not ET_DYN
//
// This tool bridges the gap by extracting the resolved .text section from ET_DYN
// and rebuilding it as ET_REL with zero relocations.
//
// Input must be a shared object (.so) with:
//   - All relocations resolved (already linked)
//   - .rodata merged into .text (via linker script)
//   - Global function symbols in .text section
//
// Usage:
//
//	so-to-obj -o <output.o> <input.so>
//
// Example:
//
//	# Step 1: Compile
//	zig cc -target x86_64-linux -c source.c -o input.o
//
//	# Step 2: Link with linker script (merges .rodata into .text)
//	zig cc -target x86_64-linux -shared -Wl,-T,merge.ld input.o -o merged.so
//
//	# Step 3: Convert ET_DYN → ET_REL
//	so-to-obj -o output.o merged.so
package main

import (
	"debug/elf"
	"fmt"
	"os"
	"path/filepath"
)

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: so-to-obj -o <output.o> <input.so>

so-to-obj converts ET_DYN (shared object) to ET_REL (relocatable object).

Why is this needed?

Go's internal linker requires syso files to be ET_REL, but it cannot handle
R_X86_64_PC32 relocations that reference .rodata sections. The workaround:

  1. Link with ld -shared to resolve all relocations (produces ET_DYN)
  2. Use this tool to convert ET_DYN → ET_REL

The key contradiction is:
  - Resolving relocations requires ld -shared (outputs ET_DYN)
  - Go's internal linker requires ET_REL

This tool bridges the gap by extracting resolved .text from ET_DYN
and rebuilding as ET_REL with zero relocations.

Input must be a shared object (.so) with:
  - All relocations resolved (already linked)
  - .rodata merged into .text
  - Global function symbols in .text section
`)
	os.Exit(2)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "so-to-obj: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	var output string
	var input string
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

	// Validate required arguments
	if output == "" {
		fatalf("missing -o <output>")
	}
	if input == "" {
		fatalf("missing input file")
	}

	// Validate input file exists
	if _, err := os.Stat(input); err != nil {
		fatalf("input file not found: %s", input)
	}

	// Validate input is a .so file
	if filepath.Ext(input) != ".so" {
		fatalf("input must be a .so file (shared object), got: %s", input)
	}

	// Validate input is a valid ELF shared object
	f, err := elf.Open(input)
	if err != nil {
		fatalf("cannot open input as ELF: %v", err)
	}
	if f.Type != elf.ET_DYN {
		f.Close()
		fatalf("input must be ET_DYN (shared object), got: %s", f.Type)
	}
	f.Close()

	// ── Extract .text and symbols from shared object ──

	textData, syms, err := extractFromSO(input)
	if err != nil {
		fatalf("%v", err)
	}

	if len(textData) == 0 {
		fatalf("empty .text section in input")
	}
	if len(syms) == 0 {
		fatalf("no global function symbols found in .text section")
	}

	// ── Write ELF relocatable object ──

	if err := writeRelocatableELF(output, textData, syms); err != nil {
		fatalf("writing output: %v", err)
	}

	// ── Sanity check ──

	if err := verifyOutput(output); err != nil {
		fatalf("verification failed: %v", err)
	}

	if !quiet {
		fmt.Printf("so-to-obj: %s  (%d bytes, %d symbols, 0 relocations)\n",
			output, len(textData), len(syms))
	}
}

// symInfo holds a global function symbol's name and offset within .text.
type symInfo struct {
	Name   string
	Offset uint64
}

// extractFromSO reads the shared object and returns the .text section
// bytes and the list of global function symbols with their section-relative
// offsets.
func extractFromSO(path string) ([]byte, []symInfo, error) {
	f, err := elf.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	// Find .text section
	textSec := f.Section(".text")
	if textSec == nil {
		return nil, nil, fmt.Errorf("no .text section in %s", path)
	}

	textData, err := textSec.Data()
	if err != nil {
		return nil, nil, fmt.Errorf("reading .text: %w", err)
	}

	textAddr := textSec.Addr

	// Read symbols
	symbols, err := f.Symbols()
	if err != nil {
		return nil, nil, fmt.Errorf("reading symbols: %w", err)
	}

	var syms []symInfo
	for _, s := range symbols {
		// Filter: global function symbols in .text section
		if elf.ST_BIND(s.Info) != elf.STB_GLOBAL {
			continue
		}
		if elf.ST_TYPE(s.Info) != elf.STT_FUNC {
			continue
		}
		// Verify the symbol belongs to .text section
		if s.Section == elf.SHN_UNDEF || s.Section == elf.SHN_ABS {
			continue
		}
		if int(s.Section) >= len(f.Sections) {
			continue
		}
		sec := f.Sections[s.Section]
		if sec.Name != ".text" {
			continue
		}

		offset := s.Value - textAddr
		syms = append(syms, symInfo{Name: s.Name, Offset: offset})
	}

	return textData, syms, nil
}

// verifyOutput re-opens the generated file and checks it has zero relocations
// and at least one exported symbol.
func verifyOutput(path string) error {
	f, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("opening output: %w", err)
	}
	defer f.Close()

	// Check for relocations
	for _, sec := range f.Sections {
		if sec.Type == elf.SHT_RELA || sec.Type == elf.SHT_REL {
			return fmt.Errorf("output has relocation section %s", sec.Name)
		}
	}

	// Check for symbols
	symbols, err := f.Symbols()
	if err != nil {
		return fmt.Errorf("reading symbols from output: %w", err)
	}

	count := 0
	for _, s := range symbols {
		if elf.ST_BIND(s.Info) == elf.STB_GLOBAL && elf.ST_TYPE(s.Info) == elf.STT_FUNC {
			count++
		}
	}
	if count == 0 {
		return fmt.Errorf("output has no exported function symbols")
	}

	return nil
}
