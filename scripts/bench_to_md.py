import re
import sys


def parse_bench(raw_text):
    # 正则匹配：Benchmark名称、迭代次数、耗时、吞吐量(可选)、内存占用、分配次数
    pattern = re.compile(
        r"Benchmark_([^_]+)_([^_]+)_([^- ]+)-\d+\s+\d+\s+([\d.]+)\s+ns/op(?:\s+([\d.]+)\s+MB/s)?\s+(\d+)\s+B/op\s+(\d+)\s+allocs/op"
    )

    results = {}
    for line in raw_text.strip().split("\n"):
        match = pattern.search(line)
        if match:
            action, dataset, library, ns_op, mb_s, b_op, allocs = match.groups()
            key = f"{action}_{dataset}"
            if key not in results:
                results[key] = []

            results[key].append(
                {
                    "library": library,
                    "ns_op": float(ns_op),
                    "mb_s": mb_s if mb_s else "N/A",
                    "b_op": int(b_op),
                    "allocs": int(allocs),
                }
            )
    return results


def generate_markdown(results):
    md = "# 🚀 Velox Performance Report\n\n"

    for key, benchmarks in results.items():
        action, dataset = key.split("_")
        md += f"### {action}: {dataset}\n\n"
        md += (
            "| Library | Latency (ns/op) | Speedup | Memory (B/op) | Allocs | MB/s |\n"
        )
        md += "| :--- | :--- | :--- | :--- | :--- | :--- |\n"

        # 以 StdJSON 或第一个非 Velox 库作为基准计算 Speedup
        baseline = next((b for b in benchmarks if "Std" in b["library"]), benchmarks[0])
        velox = next((b for b in benchmarks if "Velox" in b["library"]), None)

        for b in benchmarks:
            speedup = (
                f"{(baseline['ns_op'] / b['ns_op']):.2f}x" if b["ns_op"] > 0 else "-"
            )
            # 强调 Velox
            name = f"**{b['library']}**" if b["library"] == "Velox" else b["library"]

            md += f"| {name} | {b['ns_op']:,} | {speedup} | {b['b_op']:,} | {b['allocs']} | {b['mb_s']} |\n"
        md += "\n"

    return md


if __name__ == "__main__":
    # 使用方式: python bench_to_md.py < output.txt
    if not sys.stdin.isatty():
        raw_input = sys.stdin.read()
        print(generate_markdown(parse_bench(raw_input)))
    else:
        print("Usage: go test -bench . | python bench_to_md.py")
