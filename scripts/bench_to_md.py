import re
import sys


def parse_bench(raw_text):
    # Regex match: Benchmark name, iteration count, time cost, throughput (optional), memory usage, allocation count
    # Format: Benchmark_{Action}_{Dataset}_{Library}
    # Example: Benchmark_Marshal_Twitter_Velox, Benchmark_ParallelMarshal_KubePods_Sonic
    pattern = re.compile(
        r"Benchmark_([^_]+)_([^_]+)_([^- ]+)-\d+\s+\d+\s+([\d.]+)\s+ns/op(?:\s+([\d.]+)\s+MB/s)?\s+(\d+)\s+B/op\s+(\d+)\s+allocs/op"
    )

    # Accumulate raw samples per (action, dataset, library)
    samples = {}
    group_order = []
    for line in raw_text.strip().split("\n"):
        match = pattern.search(line)
        if match:
            action, dataset, library, ns_op, mb_s, b_op, allocs = match.groups()
            group_key = f"{action}_{dataset}"
            lib_key = (group_key, library)
            if lib_key not in samples:
                samples[lib_key] = {
                    "ns_op": [],
                    "mb_s": [],
                    "b_op": [],
                    "allocs": [],
                }
                if group_key not in [k for k, _ in group_order]:
                    group_order.append((group_key, []))
                # Append library to its group (preserving first-seen order)
                for g, libs in group_order:
                    if g == group_key:
                        libs.append(library)
                        break
            samples[lib_key]["ns_op"].append(float(ns_op))
            samples[lib_key]["mb_s"].append(float(mb_s) if mb_s else None)
            samples[lib_key]["b_op"].append(int(b_op))
            samples[lib_key]["allocs"].append(int(allocs))

    # Take median of samples
    def median(lst):
        s = sorted(lst)
        n = len(s)
        if n % 2 == 1:
            return s[n // 2]
        return (s[n // 2 - 1] + s[n // 2]) / 2

    results = {}
    for group_key, libs in group_order:
        results[group_key] = []
        for library in libs:
            s = samples[(group_key, library)]
            mb_s_vals = [v for v in s["mb_s"] if v is not None]
            results[group_key].append(
                {
                    "library": library,
                    "ns_op": median(s["ns_op"]),
                    "mb_s": f"{median(mb_s_vals):.2f}" if mb_s_vals else "N/A",
                    "b_op": int(median(s["b_op"])),
                    "allocs": int(median(s["allocs"])),
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

        # Use StdJSON or the first non-Velox library as baseline to calculate Speedup
        baseline = next((b for b in benchmarks if "Std" in b["library"]), benchmarks[0])
        velox = next((b for b in benchmarks if "Velox" in b["library"]), None)

        for b in benchmarks:
            speedup = (
                f"{(baseline['ns_op'] / b['ns_op']):.2f}x" if b["ns_op"] > 0 else "-"
            )
            # Emphasize Velox
            name = f"**{b['library']}**" if b["library"] == "Velox" else b["library"]

            md += f"| {name} | {b['ns_op']:,} | {speedup} | {b['b_op']:,} | {b['allocs']} | {b['mb_s']} |\n"
        md += "\n"

    return md


if __name__ == "__main__":
    # Usage: python bench_to_md.py < output.txt
    if not sys.stdin.isatty():
        raw_input = sys.stdin.read()
        print(generate_markdown(parse_bench(raw_input)))
    else:
        print("Usage: go test -bench . | python bench_to_md.py")
