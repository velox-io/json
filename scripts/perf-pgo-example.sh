#!/usr/bin/env bash

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# 0) Switch to repository root
cd "$PROJECT_ROOT"

# 1) Generate external-linker compatible debug syso artifacts
make gen-debug NO_PRELINK=1

# 2) Build benchmark binary (external linker + keep DWARF)
(cd benchmark; go test -c -o ../build/bench.test -ldflags="-linkmode=external -compressdwarf=false" .)

# 3) Collect perf samples
# **Parameter notes:**
# - `-F 999`: Sampling frequency at 999 Hz (avoids integer-multiple clock bias)
# - `-g --call-graph dwarf`: Collect full call stacks and unwind with DWARF (required for C-level attribution)
perf record -o local/perf.data -F 999 -g --call-graph dwarf \
  ./build/bench.test \
  -test.run='^$' \
  -test.bench='Benchmark_Marshal_(KubePods|Twitter.*)_(Velox)' \
  -test.benchmem \
  -test.benchtime=5s \
  -test.count=3


# 3.1) You can run perf report to inspect the sampled data:
# - perf report --stdio -g fractal -i local/perf.data                                   # Inspect call chains and percentages
# - perf report --stdio -g -i local/perf.data                                           # View the sampled report as a call tree
# - perf report --stdio --no-children -g -i local/perf.data                             # View non-children-expanded report
# - perf report -g -i local/perfdata | grep -E "vj_|us_write|vj_encode" | head -20      # Summarize C-function hotspots


# 4) Generate AutoFDO text profile
#    Key point: disable LBR. On AMD machines, the LBR path is often unstable or unavailable.
#    If the output profile is empty, retry with these additional flags:
#    --ignore_build_id=true --profiled_binary_name="$PROJECT_ROOT/bench.test"
create_llvm_prof \
  --profile ./perf.data \
  --binary ./bench.test \
  --use_lbr=false \
  --out ./perf.prof


# 5) Convert to LLVM sample profdata
llvm-profdata merge -sample ./perf.prof -o ./merged.profdata


# 6) Move to the location expected by build scripts
mkdir -p ./local/pgo-data
mv ./merged.profdata ./local/pgo-data/merged.profdata


# 7) Rebuild native artifacts with PGO enabled
make gen-pgo-use NO_PRELINK=1


# 8) Run benchmarks
# (cd benchmark; go test -run='^$' -bench='Benchmark_Marshal_(Tiny|KubePods|Twitter.*|MapAny)_(Velox)' -benchmem -benchtime=5s -count=3)
