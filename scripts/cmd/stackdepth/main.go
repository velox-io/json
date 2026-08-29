// stackdepth computes the max nosplit stack depth reachable from a set of
// entry symbols in a native shared library.
//
// It walks the call graph starting at each entry, sums per-function stack
// frame sizes along every path, and reports the deepest chain and its total
// bytes. Fails if any chain exceeds the platform's nosplit budget.
//
// Usage:
//
//	stackdepth [-budget N] [-json] <syso|dylib|so|dll> <entry> [entry...]
//
// Flags:
//
//	-budget N   Fail if any chain exceeds N bytes. Default matches Go's
//	            abi.StackNosplitBase (800) minus the trampoline frame (32).
//	-json       Emit machine-readable output.
//	-v          Print the deepest chain per entry regardless of budget.
package main

import (
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Frame arithmetic: we count the bytes SP moves down on function entry,
// including saved callee-registers. Every direct call also consumes 8 bytes
// (return address on stack). Indirect calls are ignored; the native library
// use only direct calls, so this is fine in practice.

type callEdge struct {
	tgt  uint64
	tail bool // true = B/branch (reuses caller SP, no return address pushed)
}

type funcInfo struct {
	name  string
	addr  uint64
	size  uint64
	frame int        // bytes SP drops on entry (locals + saved regs)
	calls []callEdge // direct call targets
}

// returnAddrCost is the per-call-edge stack overhead for the return address.
// On amd64, `call` pushes 8B onto the stack (not counted in the callee's
// `sub rsp, N` frame). On arm64, `bl` puts the return address in x30 (LR
// register) and the callee saves it in its own frame (already counted by
// the `sub sp, sp, #N` prologue), so the overhead is 0.
var returnAddrCost = 8

func main() {
	budget := flag.Int("budget", 800,
		"fail if any chain exceeds this many bytes (Go's abi.StackNosplitBase)")
	asJSON := flag.Bool("json", false, "machine-readable output")
	verbose := flag.Bool("v", false, "always print deepest chain")
	tree := flag.Bool("tree", false, "print the full call graph as a tree from each entry")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [flags] <binary> <entry> [entry...]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() < 2 {
		flag.Usage()
		os.Exit(2)
	}
	path := flag.Arg(0)
	entries := flag.Args()[1:]

	arch, funcs, err := loadBinary(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stackdepth: %v\n", err)
		os.Exit(1)
	}

	// On arm64, the return address (x30/LR) is saved inside the callee's
	// frame (already counted by the prologue scan); no separate stack push.
	if arch == "arm64" {
		returnAddrCost = 0
	}

	byAddr := make(map[uint64]*funcInfo, len(funcs))
	byName := make(map[string]*funcInfo, len(funcs))
	for _, f := range funcs {
		byAddr[f.addr] = f
		byName[f.name] = f
	}

	// Resolve entry names (accept both bare and leading-underscore forms).
	var resolved []*funcInfo
	for _, e := range entries {
		if f, ok := byName[e]; ok {
			resolved = append(resolved, f)
			continue
		}
		if f, ok := byName["_"+e]; ok {
			resolved = append(resolved, f)
			continue
		}
		fmt.Fprintf(os.Stderr, "stackdepth: entry symbol %q not found in %s\n", e, path)
		os.Exit(1)
	}

	if *tree {
		fail := false
		for _, e := range resolved {
			depth, _, _ := deepestChain(e, byAddr)
			marker := "OK"
			if depth > *budget {
				marker = "FAIL"
				fail = true
			}
			fmt.Printf("%s: %s max_depth=%d budget=%d\n", marker, e.name, depth, *budget)
			printTree(e, byAddr)
		}
		if fail {
			os.Exit(3)
		}
		return
	}

	type result struct {
		Entry  string   `json:"entry"`
		Depth  int      `json:"depth"`
		Budget int      `json:"budget"`
		Chain  []string `json:"chain"`
		Frames []int    `json:"frames"`
	}
	var results []result
	fail := false

	for _, e := range resolved {
		depth, chain, frames := deepestChain(e, byAddr)
		r := result{Entry: e.name, Depth: depth, Budget: *budget, Chain: chain, Frames: frames}
		results = append(results, r)
		if depth > *budget {
			fail = true
		}
	}

	if *asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"binary":  path,
			"arch":    arch,
			"budget":  *budget,
			"results": results,
		})
	} else {
		for _, r := range results {
			marker := "OK"
			if r.Depth > r.Budget {
				marker = "FAIL"
			}
			fmt.Printf("%s: %s max_depth=%d budget=%d\n", marker, r.Entry, r.Depth, r.Budget)
			if fail || *verbose {
				for i, name := range r.Chain {
					fmt.Printf("  %s%s frame=%d\n", indent(i), name, r.Frames[i])
				}
			}
		}
	}
	if fail {
		os.Exit(3)
	}
}

func indent(i int) string {
	var s strings.Builder
	for range i {
		s.WriteString("  ")
	}
	return s.String()
}

// deepestChain returns the cumulative stack usage from f down its worst
// caller path, plus the ordered list of names and per-function frames.
// Cycles are broken (recursion would already be a native bug in nosplit
// code paths); each cycle re-entry counts nothing beyond the first visit.
//
// Convention: dfs(g) returns the bytes below SP at g's entry point (after the
// caller's call/branch, before g saves its frame), down to the deepest
// descendant. f is reached via a NOSPLIT trampoline tail-jmp, so no return
// address is pushed to reach it; the trampoline's own 32B frame is the
// caller's concern, not counted here.
//
// Per edge: a non-tail call may push a return address onto the stack
// (returnAddrCost: 8B on amd64 via `call`, 0B on arm64 where `bl` uses x30
// and the callee saves it in its own frame, already counted). A tail call
// (B/jmp) reuses the caller's return-address slot, adding nothing. The callee's own frame and
// subchain are always counted via dfs(child).
func deepestChain(f *funcInfo, byAddr map[uint64]*funcInfo) (int, []string, []int) {
	memo := map[uint64]int{}
	pathTo := map[uint64][]string{}
	framesTo := map[uint64][]int{}
	onPath := map[uint64]bool{}

	var dfs func(g *funcInfo) (int, []string, []int)
	dfs = func(g *funcInfo) (int, []string, []int) {
		if v, ok := memo[g.addr]; ok {
			return v, pathTo[g.addr], framesTo[g.addr]
		}
		if onPath[g.addr] {
			// Cycle re-entry: count the back-edge's return address (treated as
			// a non-tail call for safety) plus g's frame, then stop.
			return g.frame + returnAddrCost, []string{g.name}, []int{g.frame}
		}
		onPath[g.addr] = true
		defer func() { onPath[g.addr] = false }()

		bestSub := 0
		var bestPath []string
		var bestFrames []int
		for _, e := range g.calls {
			child, ok := byAddr[e.tgt]
			if !ok {
				continue
			}
			d, p, fr := dfs(child)
			if !e.tail {
				d += returnAddrCost
			}
			if d > bestSub {
				bestSub = d
				bestPath = p
				bestFrames = fr
			}
		}
		total := g.frame + bestSub
		names := append([]string{g.name}, bestPath...)
		frs := append([]int{g.frame}, bestFrames...)
		memo[g.addr] = total
		pathTo[g.addr] = names
		framesTo[g.addr] = frs
		return total, names, frs
	}
	return dfs(f)
}

// printTree prints the full call graph from entry as an ASCII tree. Every
// reachable direct call is expanded; cycles are marked with "(cycle)" and not
// re-expanded. Each node shows its own frame size and the cumulative stack
// depth from entry through that node. Depth convention matches deepestChain:
// entry is reached via a tail-jmp (no return address), each non-tail call edge
// adds 8B for the return address the caller pushes, tail edges add 0.
//
// Children are sorted by max subchain depth (deepest first) so the heaviest
// paths are visible at the top of each node's subtree.
func printTree(entry *funcInfo, byAddr map[uint64]*funcInfo) {
	fmt.Printf("%s frame=%d depth=%d\n", entry.name, entry.frame, entry.frame)
	type stackItem struct {
		f      *funcInfo
		tail   bool
		prefix string
		depth  int
		isLast bool
	}
	visited := map[uint64]bool{entry.addr: true}
	// Root already printed; children render with no leading connector.
	// Push children in reverse so the first child pops first (top of tree).
	var stk []stackItem
	children := sortedChildren(entry, byAddr)
	for i := len(children) - 1; i >= 0; i-- {
		c := children[i]
		stk = append(stk, stackItem{
			f:      c.f,
			tail:   c.tail,
			prefix: "",
			depth:  entry.frame,
			isLast: i == len(children)-1,
		})
	}
	for len(stk) > 0 {
		n := len(stk) - 1
		item := stk[n]
		stk = stk[:n]

		connector := "├── "
		if item.isLast {
			connector = "└── "
		}
		edgeCost := item.f.frame + returnAddrCost
		if item.tail {
			edgeCost = item.f.frame
		}
		nodeDepth := item.depth + edgeCost
		suffix := ""
		if visited[item.f.addr] {
			suffix = " (cycle)"
		} else {
			visited[item.f.addr] = true
		}
		tailMark := ""
		if item.tail {
			tailMark = " (tail)"
		}
		fmt.Printf("%s%s%s%s%s frame=%d depth=%d\n", item.prefix, connector, item.f.name, tailMark, suffix, item.f.frame, nodeDepth)

		if suffix == " (cycle)" {
			continue
		}
		cont := "│   "
		if item.isLast {
			cont = "    "
		}
		kids := sortedChildren(item.f, byAddr)
		for i := len(kids) - 1; i >= 0; i-- {
			c := kids[i]
			stk = append(stk, stackItem{
				f:      c.f,
				tail:   c.tail,
				prefix: item.prefix + cont,
				depth:  nodeDepth,
				isLast: i == len(kids)-1,
			})
		}
	}
}

// childRef is a child function paired with the edge kind that reached it.
type childRef struct {
	f    *funcInfo
	tail bool
}

// sortedChildren returns the direct call targets of f that exist in byAddr,
// sorted by their worst-case cumulative subchain depth (deepest first).
// Targets not in byAddr (external/indirect calls) are skipped, matching
// deepestChain's behavior.
func sortedChildren(f *funcInfo, byAddr map[uint64]*funcInfo) []childRef {
	type entry struct {
		f     *funcInfo
		tail  bool
		depth int
	}
	seen := map[uint64]bool{}
	var out []entry
	for _, e := range f.calls {
		child, ok := byAddr[e.tgt]
		if !ok || seen[child.addr] {
			continue
		}
		seen[child.addr] = true
		d, _, _ := deepestChain(child, byAddr)
		if !e.tail {
			d += returnAddrCost
		}
		out = append(out, entry{f: child, tail: e.tail, depth: d})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].depth > out[j].depth })
	res := make([]childRef, len(out))
	for i, e := range out {
		res[i] = childRef{f: e.f, tail: e.tail}
	}
	return res
}

// isMappingSymbol reports whether name is an ARM/AArch64 mapping symbol
// ($x, $d, $t, optionally suffixed) marking a code/data transition. These
// carry no function semantics and must not split function extents.
func isMappingSymbol(name string) bool {
	return len(name) >= 2 && name[0] == '$' &&
		(name[1] == 'x' || name[1] == 'd' || name[1] == 't') &&
		(len(name) == 2 || name[2] == '.')
}

// loadBinary loads the binary and returns its arch and discovered functions.
func loadBinary(path string) (arch string, funcs []*funcInfo, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	if len(data) < 4 {
		return "", nil, fmt.Errorf("file too small")
	}

	// Magic detection.
	m32 := binary.LittleEndian.Uint32(data[:4])
	m32be := binary.BigEndian.Uint32(data[:4])
	switch {
	case m32 == 0xfeedfacf || m32be == 0xfeedfacf: // Mach-O 64
		return loadMachO(path)
	case m32 == 0x464c457f: // ELF (0x7f 'E' 'L' 'F')
		return loadELF(path)
	case data[0] == 'M' && data[1] == 'Z': // MS-DOS/PE
		return loadPE(path)
	}
	return "", nil, fmt.Errorf("unknown binary format")
}

// Mach-O

func loadMachO(path string) (string, []*funcInfo, error) {
	f, err := macho.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()

	arch := "unknown"
	switch f.Cpu {
	case macho.CpuArm64:
		arch = "arm64"
	case macho.CpuAmd64:
		arch = "amd64"
	default:
		return "", nil, fmt.Errorf("unsupported Mach-O cpu %v", f.Cpu)
	}

	text := f.Section("__text")
	if text == nil {
		return arch, nil, fmt.Errorf("no __text section")
	}
	textBytes, err := text.Data()
	if err != nil {
		return arch, nil, err
	}

	// Collect symbols in __text, sort by address to derive function extents.
	var syms []macho.Symbol
	if f.Symtab != nil {
		for _, s := range f.Symtab.Syms {
			if s.Sect == 1 && s.Name != "" { // sect==1 is __text on our produced Mach-O
				syms = append(syms, s)
			}
		}
	}
	sort.Slice(syms, func(i, j int) bool { return syms[i].Value < syms[j].Value })

	textStart := text.Addr
	textEnd := textStart + text.Size
	funcs := make([]*funcInfo, 0, len(syms))
	for i, s := range syms {
		if s.Value < textStart || s.Value >= textEnd {
			continue
		}
		end := textEnd
		if i+1 < len(syms) && syms[i+1].Value < textEnd {
			end = syms[i+1].Value
		}
		body := textBytes[s.Value-textStart : end-textStart]
		frame, calls := scan(body, s.Value, arch)
		funcs = append(funcs, &funcInfo{
			name:  s.Name,
			addr:  s.Value,
			size:  end - s.Value,
			frame: frame,
			calls: calls,
		})
	}
	return arch, funcs, nil
}

// ELF

func loadELF(path string) (string, []*funcInfo, error) {
	f, err := elf.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()

	arch := "unknown"
	switch f.Machine {
	case elf.EM_AARCH64:
		arch = "arm64"
	case elf.EM_X86_64:
		arch = "amd64"
	default:
		return "", nil, fmt.Errorf("unsupported ELF machine %v", f.Machine)
	}

	text := f.Section(".text")
	if text == nil {
		return arch, nil, fmt.Errorf("no .text section")
	}
	textBytes, err := text.Data()
	if err != nil {
		return arch, nil, err
	}

	symsAll, err := f.Symbols()
	if err != nil {
		// Fall back to dynamic symbols (prelink-obj strips .symtab in some paths).
		symsAll, err = f.DynamicSymbols()
		if err != nil {
			return arch, nil, err
		}
	}
	var syms []elf.Symbol
	for _, s := range symsAll {
		if s.Section == elf.SHN_UNDEF {
			continue
		}
		if elf.ST_TYPE(s.Info) != elf.STT_FUNC && elf.ST_TYPE(s.Info) != elf.STT_NOTYPE {
			continue
		}
		// AArch64 mapping symbols ($x code / $d data markers) are layout
		// annotations, not functions. They sit at function starts (aliasing
		// the real entry to a zero-length body) and inside .text between
		// literal pools, truncating the previous function's extent.
		if isMappingSymbol(s.Name) {
			continue
		}
		if s.Value < text.Addr || s.Value >= text.Addr+text.Size {
			continue
		}
		syms = append(syms, s)
	}
	// Stable sort keeps symtab order for any remaining equal-address pair.
	sort.SliceStable(syms, func(i, j int) bool { return syms[i].Value < syms[j].Value })
	// Same-address aliases would zero out one of the two bodies.
	deduped := syms[:0]
	for i, s := range syms {
		if i > 0 && syms[i-1].Value == s.Value {
			continue
		}
		deduped = append(deduped, s)
	}
	syms = deduped

	funcs := make([]*funcInfo, 0, len(syms))
	for i, s := range syms {
		end := text.Addr + text.Size
		if i+1 < len(syms) && syms[i+1].Value < end {
			end = syms[i+1].Value
		}
		body := textBytes[s.Value-text.Addr : end-text.Addr]
		frame, calls := scan(body, s.Value, arch)
		funcs = append(funcs, &funcInfo{
			name:  s.Name,
			addr:  s.Value,
			size:  end - s.Value,
			frame: frame,
			calls: calls,
		})
	}
	return arch, funcs, nil
}

// PE

// symEntry is a minimal (address, name) pair used to build funcInfos. Both the
// COFF symbol table path and the /MAP file path produce these, letting the
// downstream extent/scan logic stay shared.
type symEntry struct {
	addr uint64
	name string
}

func loadPE(path string) (string, []*funcInfo, error) {
	f, err := pe.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()

	arch := "unknown"
	switch f.Machine {
	case pe.IMAGE_FILE_MACHINE_AMD64:
		arch = "amd64"
	case pe.IMAGE_FILE_MACHINE_ARM64:
		arch = "arm64"
	default:
		return "", nil, fmt.Errorf("unsupported PE machine %#x", f.Machine)
	}

	text := f.Section(".text")
	if text == nil {
		return arch, nil, fmt.Errorf("no .text section")
	}
	textBytes, err := text.Data()
	if err != nil {
		return arch, nil, err
	}

	// PE image base can be derived from the optional header; the addresses
	// we get from symbols are relative to it. For our purposes we only
	// need consistent addresses within the .text section.
	imageBase := uint64(0)
	if oh, ok := f.OptionalHeader.(*pe.OptionalHeader64); ok {
		imageBase = oh.ImageBase
	}
	textAddr := imageBase + uint64(text.VirtualAddress)
	textEnd := textAddr + uint64(text.VirtualSize)

	// Collect text-resident symbols from the COFF symbol table. PE symbol
	// values are relative to imageBase for image-resident symbols.
	var entries []symEntry
	for _, s := range f.Symbols {
		if s.SectionNumber <= 0 || int(s.SectionNumber) > len(f.Sections) {
			continue
		}
		sec := f.Sections[s.SectionNumber-1]
		if sec.Name != ".text" {
			continue
		}
		addr := imageBase + uint64(sec.VirtualAddress) + uint64(s.Value)
		if addr < textAddr || addr >= textEnd {
			continue
		}
		entries = append(entries, symEntry{addr: addr, name: s.Name})
	}

	// lld-link strips the COFF symbol table for DLLs (only the export table
	// survives). Fall back to the /MAP file produced alongside the DLL to
	// recover internal function symbols for call-graph analysis.
	if len(entries) == 0 {
		mapEntries, mapErr := parsePEMap(path, textAddr, textEnd)
		if mapErr == nil {
			entries = mapEntries
		}
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].addr < entries[j].addr })

	funcs := make([]*funcInfo, 0, len(entries))
	for i, e := range entries {
		if e.addr < textAddr || e.addr >= textEnd {
			continue
		}
		end := textEnd
		if i+1 < len(entries) && entries[i+1].addr < end {
			end = entries[i+1].addr
		}
		body := textBytes[e.addr-textAddr : end-textAddr]
		frame, calls := scan(body, e.addr, arch)
		funcs = append(funcs, &funcInfo{
			name:  e.name,
			addr:  e.addr,
			size:  end - e.addr,
			frame: frame,
			calls: calls,
		})
	}
	return arch, funcs, nil
}

// parsePEMap reads an lld-link /MAP file to recover function symbols when the
// COFF symbol table has been stripped from a DLL.
//
// The map file lists symbols in two blocks: "Publics by Value" (global) and
// "Static symbols" (local). Each symbol line looks like:
//
//	0001:00006d50       vj_vm_exec_fast_avx2       0000000180007d50     encvm_fast_windows_amd64_avx2.o
//
// Fields: segment:offset, name, rva+base, object file. Segment 0001 is .text.
// The .text CODE header line gives the code-only length (before merged .rdata
// data constants), which we use to exclude data symbols from the call graph.
func parsePEMap(dllPath string, textAddr, textEnd uint64) ([]symEntry, error) {
	mapPath := strings.TrimSuffix(dllPath, filepath.Ext(dllPath)) + ".map"
	data, err := os.ReadFile(mapPath)
	if err != nil {
		return nil, err
	}

	// Parse the .text code length from the section header:
	//   " 0001:00000000 00017ef0H .text                   CODE"
	textCodeEnd := textEnd // fallback: include all of .text
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, ".text") || !strings.Contains(line, "CODE") {
			continue
		}
		fields := strings.Fields(line)
		// fields: ["0001:00000000", "00017ef0H", ".text", "CODE"]
		if len(fields) >= 2 {
			lenStr := strings.TrimSuffix(fields[1], "H")
			if v, perr := strconv.ParseUint(lenStr, 16, 64); perr == nil {
				textCodeEnd = textAddr + v
				break
			}
		}
	}

	var entries []symEntry
	for _, line := range strings.Split(string(data), "\n") {
		// Symbol lines start with " 0001:" (segment 0001 = .text).
		if !strings.HasPrefix(line, " 0001:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		// fields[0] = "0001:OFFSET", fields[1] = name, fields[2] = rva+base
		parts := strings.SplitN(fields[0], ":", 2)
		if len(parts) != 2 {
			continue
		}
		addr, perr := strconv.ParseUint(fields[2], 16, 64)
		if perr != nil {
			continue
		}
		// Keep only code-region symbols (exclude merged .rdata data constants).
		if addr >= textCodeEnd {
			continue
		}
		if addr < textAddr {
			continue
		}
		entries = append(entries, symEntry{addr: addr, name: fields[1]})
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no symbols in map file %s", mapPath)
	}
	return entries, nil
}

// -- instruction scanning --------------------------------------------------

func scan(body []byte, base uint64, arch string) (frame int, calls []callEdge) {
	switch arch {
	case "arm64":
		return scanARM64(body, base)
	case "amd64":
		return scanAMD64(body, base)
	}
	return 0, nil
}

// arm64 is fixed-width 4-byte instructions. We recognise:
//   - sub sp, sp, #imm12                    (S=0, sh=0, sf=1, Rn=Rd=31)
//   - stp Xt1, Xt2, [sp, #-imm7*8]!         (pre-index; also SIMD stp variant)
//   - bl imm26                              (imm26 << 2 sign-extended)
//   - b  imm26                              (tail call; no return address)
//
// Not tracked: SUB reg (variable), autoincrement store variants that don't
// map to the standard callee-save block. Missed frame growth would only
// underestimate; the per-function guard still catches those explicitly.
//
// Tail-call detection: a `b imm26` to a function start is treated as a
// tail call. Some `b` are plain branches inside the same function (jumps to
// a local label), not calls. We filter by checking the target is the start
// of a known function in loadBinary. Targets that fall inside the current
// function body (local jumps) are dropped there, so we only emit tail edges
// for genuine cross-function branches.
func scanARM64(body []byte, base uint64) (frame int, calls []callEdge) {
	if len(body) < 4 {
		return 0, nil
	}
	for off := 0; off+4 <= len(body); off += 4 {
		w := binary.LittleEndian.Uint32(body[off:])
		// sub sp, sp, #imm12 (sf=1, S=0, sh=0):
		//   1101 0001 00 imm12 11111 11111
		if w&0xff8003ff == 0xd10003ff {
			imm := int((w >> 10) & 0xfff)
			if imm > frame {
				frame = imm
			}
			continue
		}
		// stp x1, x2, [sp, #-imm7*8]!  (pre-index STP GPR, sf=1, class=101001, op=10):
		//   1010 1001 1? imm7[5:0] xt2 xn(=11111) xt1  -- L=0 store, W=1 pre-index
		//   Fixed bits 31-23 = 101010011 (opc=10, 101, V=0, addr=011).
		//   Bit 22 = imm7[6] (sign bit, 1 for negative = stack allocation).
		//   Mask bits 31-23 only; imm7 is variable.
		if (w & 0xff800000) == 0xa9800000 {
			// bits [21:15] = imm7, bits [9:5] = Rn (must be 31 == sp),
			// and we require pre-index (bit10 set) which the mask above enforces
			// when combined with bit 10.
			rn := (w >> 5) & 0x1f
			if rn == 31 {
				imm7 := int((w >> 15) & 0x7f)
				if imm7 >= 0x40 {
					imm7 -= 0x80 // sign-extend 7 bits
				}
				bytes := -imm7 * 8
				if bytes > frame {
					frame = bytes
				}
			}
			continue
		}
		// Same encoding for stp q, q, [sp, #-imm7*16]! (SIMD): V=1, bits 31-23 = 101011011.
		if (w & 0xff800000) == 0xad800000 {
			rn := (w >> 5) & 0x1f
			if rn == 31 {
				imm7 := int((w >> 15) & 0x7f)
				if imm7 >= 0x40 {
					imm7 -= 0x80
				}
				bytes := -imm7 * 16
				if bytes > frame {
					frame = bytes
				}
			}
			continue
		}
		// bl imm26: opcode 100101 imm26
		if (w & 0xfc000000) == 0x94000000 {
			imm26 := int32(w & 0x03ffffff)
			if imm26&0x02000000 != 0 { // sign-extend 26 bits
				imm26 |= ^0x03ffffff
			}
			target := int64(base) + int64(off) + int64(imm26)*4
			calls = append(calls, callEdge{tgt: uint64(target), tail: false})
			continue
		}
		// b imm26: opcode 000101 imm26 (tail call when target is a function entry)
		if (w & 0xfc000000) == 0x14000000 {
			imm26 := int32(w & 0x03ffffff)
			if imm26&0x02000000 != 0 {
				imm26 |= ^0x03ffffff
			}
			target := int64(base) + int64(off) + int64(imm26)*4
			calls = append(calls, callEdge{tgt: uint64(target), tail: true})
		}
	}
	return frame, calls
}

// amd64 is variable-length. We only need to recognise:
//   - push reg64:   0x50-0x57  (rax..rdi)  or  0x41 0x50-0x57 (r8..r15)
//   - sub imm32, %rsp:  0x48 0x81 0xec <imm32>
//   - sub imm8,  %rsp:  0x48 0x83 0xec <imm8>
//   - and imm8,  %rsp:  0x48 0x83 0xe4 <imm8>   (stack alignment; counts as 0
//     because the bytes it shaves off are below the saved-RSP boundary we
//     measure from, but it must be skipped without ending the prologue)
//   - and imm32, %rsp:  0x48 0x81 0xe4 <imm32>
//   - mov %rsp, %rbp:   0x48 0x89 0xe5           (frame-pointer setup; skip)
//   - call rel32:   0xe8 <rel32>
//   - jmp  rel32:   0xe9 <rel32>   (tail call)
//
// Prologue scanning continues across push/sub/and-rsp/mov-rsp-rbp sequences
// (System V codegen interleaves them, e.g. push %rbp; mov %rsp,%rbp;
// push %r15..%rbx; and $-32,%rsp; sub $0x320,%rsp). Any other byte ends the
// prologue and we switch to call/jmp scanning only. Misidentification of
// interior bytes as a "push" over-estimates frame size, biasing the check to
// false positives (safe direction).
func scanAMD64(body []byte, base uint64) (frame int, calls []callEdge) {
	i := 0
	// Prologue: walk until we see a byte that isn't part of a known prologue
	// instruction; after that treat the remainder purely for call-target
	// scanning. Callee-saved pushes after `mov %rsp,%rbp` are common and must
	// still be counted, so we don't bail on the mov.
	prologueDone := false
	pushBytes := 0
	subBytes := 0
	for i < len(body) {
		b := body[i]
		// Prologue detection: pushq %reg or sub/and imm, %rsp or mov %rsp,%rbp.
		if !prologueDone {
			if b >= 0x50 && b <= 0x57 { // push rax..rdi
				pushBytes += 8
				i++
				continue
			}
			if b == 0x41 && i+1 < len(body) && body[i+1] >= 0x50 && body[i+1] <= 0x57 {
				pushBytes += 8
				i += 2
				continue
			}
			// REX.W op modrm <imm>: 0x48 0x83 <modrm> <imm8>  or  0x48 0x81 <modrm> <imm32>
			//   modrm 0xec = sub imm, %rsp   (frame allocation)
			//   modrm 0xe4 = and imm, %rsp   (alignment; not frame allocation,
			//                                but must be skipped in prologue)
			if b == 0x48 && i+2 < len(body) && body[i+1] == 0x83 {
				switch body[i+2] {
				case 0xec: // sub imm8, %rsp
					if i+3 < len(body) {
						subBytes += int(int8(body[i+3]))
						i += 4
						continue
					}
				case 0xe4: // and imm8, %rsp (alignment)
					if i+3 < len(body) {
						i += 4
						continue
					}
				}
			}
			if b == 0x48 && i+2 < len(body) && body[i+1] == 0x81 {
				switch body[i+2] {
				case 0xec: // sub imm32, %rsp
					if i+6 < len(body) {
						subBytes += int(int32(binary.LittleEndian.Uint32(body[i+3:])))
						i += 7
						continue
					}
				case 0xe4: // and imm32, %rsp (alignment)
					if i+6 < len(body) {
						i += 7
						continue
					}
				}
			}
			// mov %rsp, %rbp (frame-pointer setup): 0x48 0x89 0xe5
			if b == 0x48 && i+2 < len(body) && body[i+1] == 0x89 && body[i+2] == 0xe5 {
				i += 3
				continue
			}
			prologueDone = true
		}
		// Call: e8 rel32
		if b == 0xe8 && i+5 <= len(body) {
			rel := int32(binary.LittleEndian.Uint32(body[i+1:]))
			target := int64(base) + int64(i+5) + int64(rel)
			calls = append(calls, callEdge{tgt: uint64(target), tail: false})
			i += 5
			continue
		}
		// Tail call: e9 rel32
		if b == 0xe9 && i+5 <= len(body) {
			rel := int32(binary.LittleEndian.Uint32(body[i+1:]))
			target := int64(base) + int64(i+5) + int64(rel)
			calls = append(calls, callEdge{tgt: uint64(target), tail: true})
			i += 5
			continue
		}
		i++
	}
	frame = pushBytes + subBytes
	return frame, calls
}

// silence unused import in trimmed builds
var _ = io.EOF
