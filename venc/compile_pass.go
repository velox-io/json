package venc

type CompilerPass interface {
	Name() string
	Run(insts []IRInst, alloc *labelAllocator) []IRInst
}

type labelAllocator struct{ next Label }

func (a *labelAllocator) Alloc() Label {
	a.next++
	return a.next
}

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

type eliminateTrampolinesPass struct{}

func (p *eliminateTrampolinesPass) Name() string { return "eliminate-trampolines" }

func (p *eliminateTrampolinesPass) Run(insts []IRInst, _ *labelAllocator) []IRInst {
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

	for i := range insts {
		if insts[i].Op == opCall {
			if final, ok := redirect[insts[i].Target]; ok {
				insts[i].Target = final
			}
		}
	}

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
