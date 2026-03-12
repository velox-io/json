package vjson

import "reflect"

// CompilerPass transforms an IR instruction sequence.
type CompilerPass interface {
	Name() string
	Run(insts []IRInst, alloc *labelAllocator) []IRInst
}

// labelAllocator hands out fresh Labels, continuing from the builder's counter.
type labelAllocator struct{ next Label }

func (a *labelAllocator) Alloc() Label {
	a.next++
	return a.next
}

// compilePipeline is the ordered list of IR optimization passes.
var compilePipeline = []CompilerPass{
	&deduplicateRecursivePass{},
	&eliminateTrampolinesPass{},
}

func runPasses(insts []IRInst, nextLabel Label) []IRInst {
	alloc := &labelAllocator{next: nextLabel}
	for _, p := range compilePipeline {
		insts = p.Run(insts, alloc)
	}
	return insts
}

// ── deduplicateRecursivePass ────────────────────────────────────────
// Finds struct bodies (OBJ_OPEN..OBJ_CLOSE) that appear >= 2 times
// (same SourceType, keyless, fieldOff==0) and deduplicates them:
// first occurrence becomes a subroutine, subsequent ones become CALL.

type deduplicateRecursivePass struct{}

func (p *deduplicateRecursivePass) Name() string { return "dedup-recursive" }

func (p *deduplicateRecursivePass) Run(insts []IRInst, alloc *labelAllocator) []IRInst {
	// 1. Count extractable OBJ_OPEN occurrences per SourceType.
	counts := make(map[reflect.Type]int)
	for i := range insts {
		if isExtractable(&insts[i]) {
			counts[insts[i].SourceType]++
		}
	}

	// 2. Filter: only types appearing >= 2 times.
	targets := make(map[reflect.Type]bool)
	for t, c := range counts {
		if c >= 2 {
			targets[t] = true
		}
	}
	if len(targets) == 0 {
		return insts
	}

	// 3. Extract & replace.
	// For each target type, the first occurrence is captured as a subroutine body;
	// all occurrences (including the first) are replaced with CALL.
	type subroutine struct {
		label Label
		body  []IRInst // OBJ_OPEN + inner + OBJ_CLOSE (captured from first occurrence)
	}
	subs := make(map[reflect.Type]*subroutine)

	var out []IRInst
	i := 0
	for i < len(insts) {
		inst := &insts[i]
		if !isExtractable(inst) || !targets[inst.SourceType] {
			out = append(out, insts[i])
			i++
			continue
		}

		typ := inst.SourceType
		// Find matching OBJ_CLOSE (nesting-aware).
		closeIdx := findMatchingClose(insts, i)
		if closeIdx < 0 {
			// Safety: shouldn't happen; emit as-is.
			out = append(out, insts[i])
			i++
			continue
		}

		sub, exists := subs[typ]
		if !exists {
			// First occurrence: capture body as subroutine.
			sub = &subroutine{
				label: alloc.Alloc(),
				body:  make([]IRInst, closeIdx-i+1),
			}
			copy(sub.body, insts[i:closeIdx+1])
			subs[typ] = sub
		}

		// Replace with CALL.
		out = append(out, IRInst{
			Op:         opCall,
			FieldOff:   0,
			Target:     sub.label,
			Annotation: typ.String(),
		})

		i = closeIdx + 1
	}

	// 4. Append subroutines at the end.
	for _, sub := range subs {
		out = append(out, IRInst{Op: irLabel, LabelID: sub.label})
		out = append(out, sub.body...)
		out = append(out, IRInst{Op: opRet})
	}

	return out
}

func isExtractable(inst *IRInst) bool {
	return inst.Op == opObjOpen &&
		inst.SourceType != nil &&
		inst.FieldOff == 0 &&
		inst.KeyLen == 0
}

func findMatchingClose(insts []IRInst, startIdx int) int {
	depth := 0
	for i := startIdx; i < len(insts); i++ {
		switch insts[i].Op {
		case opObjOpen:
			depth++
		case opObjClose:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// ── eliminateTrampolinesPass ────────────────────────────────────────
// Detects trampoline subroutines of the form:
//     label L1: CALL L2, RET
// and rewrites all CALL L1 → CALL L2, then removes the dead trampoline.

type eliminateTrampolinesPass struct{}

func (p *eliminateTrampolinesPass) Name() string { return "eliminate-trampolines" }

func (p *eliminateTrampolinesPass) Run(insts []IRInst, alloc *labelAllocator) []IRInst {
	// 1. Scan for trampoline patterns: irLabel, opCall, opRet
	// Build a redirect map: trampoline label → final target label.
	redirect := make(map[Label]Label)
	for i := 0; i+2 < len(insts); i++ {
		if insts[i].Op == irLabel &&
			insts[i+1].Op == opCall &&
			insts[i+2].Op == opRet {
			redirect[insts[i].LabelID] = insts[i+1].Target
		}
	}
	if len(redirect) == 0 {
		return insts
	}

	// Chase redirect chains (A→B→C becomes A→C).
	for src, dst := range redirect {
		for {
			next, ok := redirect[dst]
			if !ok || next == dst {
				break
			}
			dst = next
		}
		redirect[src] = dst
	}

	// 2. Rewrite all CALL targets that hit a trampoline.
	for i := range insts {
		if insts[i].Op == opCall {
			if final, ok := redirect[insts[i].Target]; ok {
				insts[i].Target = final
			}
		}
	}

	// 3. Remove dead trampolines (irLabel + opCall + opRet sequences).
	trampolineLabels := make(map[Label]bool, len(redirect))
	for l := range redirect {
		trampolineLabels[l] = true
	}

	out := make([]IRInst, 0, len(insts))
	i := 0
	for i < len(insts) {
		if i+2 < len(insts) &&
			insts[i].Op == irLabel &&
			trampolineLabels[insts[i].LabelID] &&
			insts[i+1].Op == opCall &&
			insts[i+2].Op == opRet {
			i += 3 // skip the dead trampoline
			continue
		}
		out = append(out, insts[i])
		i++
	}

	return out
}
