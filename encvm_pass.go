package vjson

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
	&eliminateTrampolinesPass{},
}

func runPasses(insts []IRInst, nextLabel Label) []IRInst {
	alloc := &labelAllocator{next: nextLabel}
	for _, p := range compilePipeline {
		insts = p.Run(insts, alloc)
	}
	return insts
}

// eliminateTrampolinesPass
// Detects trampoline subroutines of the form:
//
//	label L1: CALL L2, RET
//
// and rewrites all CALL L1 → CALL L2, then removes the dead trampoline.

type eliminateTrampolinesPass struct{}

func (p *eliminateTrampolinesPass) Name() string { return "eliminate-trampolines" }

func (p *eliminateTrampolinesPass) Run(insts []IRInst, _ *labelAllocator) []IRInst {
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
