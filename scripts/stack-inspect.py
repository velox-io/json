#!/usr/bin/env python3
"""Analyze the call graph and stack frame usage of a .syso binary. Supports x86/amd64 and arm64.

Usage:
  python3 checkss.py                                    # default binary, print top N frames and deepest call chain
  python3 checkss.py <root_function_name>               # print ASCII call tree from the given function
  python3 checkss.py <root> --depth 8                   # adjust tree depth
  python3 checkss.py <root> --png out                   # also render a graphviz PNG
  python3 checkss.py --list                             # list named functions
  python3 checkss.py --target native/.../arm64.syso     # specify the binary
  python3 checkss.py --target <path> <root> --top 20    # combine flags

Frame size is defined as the sum of all explicit stack allocations in the function entry block prologue.
  x86/amd64: push reg (8B each) + sub rsp/esp/sp, imm
  arm64:     sub sp, sp, #imm + pre-index stp/str/stnp [sp, #-N]!
A control flow instruction (call/ret/jmp/branch) ends the prologue and stops accumulation.
and rsp, -N (stack alignment) is not counted as an explicit alloc. nosplit/leaf functions may be 0.

arch limitation: the cle Mach-O backend does not support MH_OBJECT (darwin .syso), so only linux ELF .syso can be analyzed.
"""
import argparse
import logging

# FCP dirty-helper noise fires during CFGFast; set this after importing angr to silence it.
logging.getLogger("angr.analyses.fcp.fcp.SimEngineFCPVEX").setLevel(logging.CRITICAL)

import angr
import capstone

DEFAULT_TARGET = "native/encvm/encvm_full_avx2_linux_amd64.syso"


def extract_frame_x86(func, max_ins=25):
    """amd64/x86 prologue: accumulate push reg (8B each) + sub rsp/esp/sp, imm.
    A control flow instruction (call/ret/jmp/jcc/loop) ends the prologue.
    and rsp, -N is stack alignment and is not counted as alloc. Matches the make stack-check definition.
    """
    try:
        blocks = list(func.blocks)
        if not blocks:
            return 0
        b0 = blocks[0]
    except Exception:
        return 0
    total = 0
    for i, ins in enumerate(b0.capstone.insns):
        if i >= max_ins:
            break
        mnem = ins.mnemonic.lower()
        # control flow instruction ends the prologue scan
        if mnem in ("call", "ret", "retf", "iret") or mnem.startswith("j") or mnem.startswith("loop"):
            break
        # push reg: +8 (push imm does not count, rarely used in a prologue)
        if mnem == "push" and len(ins.operands) == 1:
            op = ins.operands[0]
            if op.type == capstone.x86.X86_OP_REG:
                total += 8
                continue
        # sub rsp/esp/sp, imm: +imm
        if mnem == "sub" and len(ins.operands) == 2:
            op0, op1 = ins.operands
            if (
                op0.type == capstone.x86.X86_OP_REG
                and ins.reg_name(op0.reg) in ("rsp", "esp", "sp")
                and op1.type == capstone.x86.X86_OP_IMM
            ):
                total += op1.imm
                continue
    return total


def extract_frame_arm64(func, max_ins=25):
    """arm64 prologue: accumulate sub sp, sp, #imm + pre-index stp/str/stnp [sp, #-N]!.
    A branch instruction (b/bl/blr/br/ret/cbz/tbz etc.) ends the prologue.
    An offset store [sp, #N] is not counted as alloc (already covered by sub sp).
    """
    try:
        blocks = list(func.blocks)
        if not blocks:
            return 0
        b0 = blocks[0]
    except Exception:
        return 0
    total = 0
    branch_mnems = {"b", "bl", "blr", "br", "ret", "cbz", "cbnz", "tbz", "tbnz"}
    for i, ins in enumerate(b0.capstone.insns):
        if i >= max_ins:
            break
        mnem = ins.mnemonic.lower()
        # branch instruction ends the prologue scan (b.<cond> form)
        if mnem in branch_mnems or mnem.startswith("b."):
            break
        # sub sp, sp, #imm
        if mnem == "sub" and len(ins.operands) == 3:
            op0, op1, op2 = ins.operands
            if (
                op0.type == capstone.arm64.ARM64_OP_REG
                and op1.type == capstone.arm64.ARM64_OP_REG
                and op2.type == capstone.arm64.ARM64_OP_IMM
                and ins.reg_name(op0.reg) == "sp"
                and ins.reg_name(op1.reg) == "sp"
            ):
                total += op2.imm
                continue
        # pre-index stp/str/stnp ... [sp, #-N]!: take |disp|
        if mnem in ("stp", "str", "stnp") and len(ins.operands) >= 2:
            last = ins.operands[-1]
            if (
                last.type == capstone.arm64.ARM64_OP_MEM
                and last.mem.disp < 0
                and last.mem.base != 0
                and ins.reg_name(last.mem.base) == "sp"
            ):
                total += -last.mem.disp
                continue
    return total


def make_frame_extractor(arch):
    """Pick a frame extractor by arch name. Unknown archs return a stub that always yields 0."""
    name = arch.name.lower()
    if name in ("amd64", "x86", "i386"):
        return extract_frame_x86
    if name in ("aarch64", "arm64"):
        return extract_frame_arm64
    return lambda f, **kw: 0


def find_func(cfg, name):
    for f in cfg.functions.values():
        if f.name == name:
            return f
    return None


def list_named(cfg):
    out = []
    for f in cfg.functions.values():
        if f.name and not f.name.startswith("sub_"):
            out.append((f.addr, f.name))
    out.sort()
    return out


def ascii_tree(cfg, root_func, frame_map, depth):
    """ASCII call tree from root_func; children sorted by frame size descending; cycles are marked."""
    cg = cfg.functions.callgraph
    lines = []
    on_stack = set()

    def recurse(addr, prefix, is_last, d):
        f = cfg.functions[addr]
        name = f.name or f"sub_{addr:x}"
        frame = frame_map.get(addr, 0)
        branch = "└── " if is_last else "├── "
        marker = " *" if addr in on_stack else ""
        lines.append(f"{prefix}{branch}{name} [{hex(addr)}] frame={frame}B{marker}")
        if addr in on_stack or d >= depth:
            return
        on_stack.add(addr)
        children = sorted(cg.successors(addr), key=lambda a: -frame_map.get(a, 0))
        new_prefix = prefix + ("    " if is_last else "│   ")
        for i, c in enumerate(children):
            recurse(c, new_prefix, i == len(children) - 1, d + 1)
        on_stack.discard(addr)

    recurse(root_func.addr, "", True, 0)
    return "\n".join(lines)


def deepest_chain(cfg, frame_map):
    """Deepest call chain by cumulative frame. Uses DFS with memoization; on_stack detects cycles to avoid infinite recursion."""
    cg = cfg.functions.callgraph
    memo = {}
    on_stack = set()

    def best(addr):
        if addr in memo:
            return memo[addr]
        if addr in on_stack:
            return (frame_map.get(addr, 0), [addr])
        on_stack.add(addr)
        best_child = (0, [])
        for s in cg.successors(addr):
            v, path = best(s)
            if v > best_child[0]:
                best_child = (v, path)
        on_stack.discard(addr)
        result = (frame_map.get(addr, 0) + best_child[0], [addr] + best_child[1])
        memo[addr] = result
        return result

    roots = [n for n in cg.nodes() if cg.in_degree(n) == 0]
    best_overall = (0, [])
    for r in roots:
        v, path = best(r)
        if v > best_overall[0]:
            best_overall = (v, path)
    return best_overall


def render_png(cfg, frame_map, path):
    """Render the whole call graph to a PNG via graphviz. For large graphs, prefer dot -Gsize to limit it."""
    import graphviz

    dot = graphviz.Digraph()
    dot.attr(rankdir="LR", size="30,20", dpi="100")
    cg = cfg.functions.callgraph
    for addr in cg.nodes():
        f = cfg.functions[addr]
        name = f.name or f"sub_{addr:x}"
        frame = frame_map.get(addr, 0)
        # large-frame functions highlighted in red
        color = "red" if frame >= 256 else ("orange" if frame >= 128 else "black")
        dot.node(str(addr), f"{name}\n{frame}B", color=color)
    for u, v in cg.edges():
        dot.edge(str(u), str(v))
    out = dot.render(path, format="png", cleanup=True)
    return out


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("root", nargs="?", help="根函数名, 不传则打印 top N 和最深调用链")
    p.add_argument("--target", default=DEFAULT_TARGET, help=f"目标 .syso 路径 (默认 {DEFAULT_TARGET})")
    p.add_argument("--depth", type=int, default=5, help="ASCII 树最大深度 (默认 5)")
    p.add_argument("--top", type=int, default=15, help="栈帧 top N (默认 15)")
    p.add_argument("--png", help="渲染整张调用图 PNG 到该路径 (无后缀)")
    p.add_argument("--list", action="store_true", help="列出有名字的函数")
    args = p.parse_args()

    proj = angr.Project(args.target, auto_load_libs=False)
    cfg = proj.analyses.CFGFast()
    cg = cfg.functions.callgraph
    print(
        f"target: {args.target}\n"
        f"arch: {proj.arch.name}  functions: {len(cfg.functions)}  "
        f"callgraph edges: {cg.number_of_edges()}  entry: {hex(proj.entry)}"
    )

    extract = make_frame_extractor(proj.arch)
    frame_map = {f.addr: extract(f) for f in cfg.functions.values()}

    if args.list:
        print("\n=== named functions ===")
        for addr, name in list_named(cfg):
            print(f"  {hex(addr):>10}  {name}")
        return

    if args.root:
        f = find_func(cfg, args.root)
        if not f:
            print(f"function not found: {args.root}")
            print("named functions (first 30):")
            for addr, name in list_named(cfg)[:30]:
                print(f"  {hex(addr):>10}  {name}")
            return
        print(f"\n=== call tree from {args.root} (depth={args.depth}) ===")
        print(ascii_tree(cfg, f, frame_map, args.depth))
    else:
        print(f"\n=== top {args.top} frame sizes ===")
        ranked = sorted(cfg.functions.values(), key=lambda f: -frame_map.get(f.addr, 0))
        for f in ranked[: args.top]:
            name = f.name or f"sub_{f.addr:x}"
            print(f"  {frame_map[f.addr]:>6}B  {hex(f.addr):>10}  {name}")

        print("\n=== deepest call chain (cumulative frame) ===")
        total, path = deepest_chain(cfg, frame_map)
        cum = 0
        for i, addr in enumerate(path):
            f = cfg.functions[addr]
            name = f.name or f"sub_{addr:x}"
            print(
                f"  {'  ' * i}{name} [{hex(addr)}] +{frame_map.get(addr,0)}B (cum {cum + frame_map.get(addr,0)}B)"
            )
            cum += frame_map.get(addr, 0)
        print(f"  total cumulative frame: {total}B  chain length: {len(path)}")

    if args.png:
        out = render_png(cfg, frame_map, args.png)
        print(f"\nPNG written to {out}")


if __name__ == "__main__":
    main()
