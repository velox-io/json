CC      ?= clang
CFLAGS  ?= -O2 -march=native

BUILD ?= build

INCLUDES = -I impl -I cbind -I ../include

BENCH_DATA_DIR ?= test/data
BENCH_FILTER   ?=
BENCH_ITERS    ?= 5000000

# Pin bench to a fixed CPU to avoid migration/frequency jitter (~10ns
# false-regression territory on unpinned runs). Override the core with
# `make bench BENCH_CPU=3`, or disable entirely with `BENCH_CPU=`.
# Auto-disabled on platforms without taskset (e.g. macOS).
BENCH_CPU ?= 2
BENCH_TASKSET := $(if $(BENCH_CPU),$(if $(shell command -v taskset 2>/dev/null),taskset -c $(BENCH_CPU)))

CRITERION_INC    ?= /usr/local/lib/criterion/include
CRITERION_LIB    ?= /usr/local/lib/criterion/lib
CRITERION_LDFLAGS = -L$(CRITERION_LIB) -Wl,-rpath,$(CRITERION_LIB) -lcriterion

TESTS   = test/tests.c
STREAM_TESTS = test/stream_tests.c
BIND_SRC     = cbind/bind.c cbind/field_lookup.c
BIND_TESTS   = cbind/bind_tests.c
JSONFMT_SRC    = examples/jsonfmt.c

.PHONY: test bench jsonfmt debug clean

FILTER  ?=
VERBOSE ?= 2
TEST_FLAGS = $(if $(VERBOSE),--verbose=$(VERBOSE),) $(addprefix --filter=,$(FILTER))

test: $(BUILD)/tests $(BUILD)/stream_tests $(BUILD)/bind_tests
	$(BUILD)/tests $(TEST_FLAGS)
	$(BUILD)/stream_tests $(TEST_FLAGS)
	$(BUILD)/bind_tests $(TEST_FLAGS)

$(BUILD):
	mkdir -p $(BUILD)

bench: $(BUILD)/ndec_bench
	for f in $(BENCH_DATA_DIR)/$(if $(BENCH_FILTER),$(BENCH_FILTER),*.json); do \
		$(if $(BENCH_ITERS),BENCH_ITERS=$(BENCH_ITERS)) nice -20 $(BENCH_TASKSET) $(BUILD)/ndec_bench $$f; \
	done

$(BUILD)/tests: $(TESTS) impl/ndec/core/*.h impl/ndec/*.h | $(BUILD)
	$(CC) -g -O0 $(INCLUDES) -I$(CRITERION_INC) -o $@ $(TESTS) $(CRITERION_LDFLAGS)

$(BUILD)/stream_tests: $(STREAM_TESTS) impl/ndec/core/*.h impl/ndec/*.h | $(BUILD)
	$(CC) -g -O0 $(INCLUDES) -I$(CRITERION_INC) -o $@ $(STREAM_TESTS) $(CRITERION_LDFLAGS)

$(BUILD)/bind_tests: $(BIND_TESTS) $(BIND_SRC) impl/ndec/core/*.h impl/ndec/*.h cbind/*.h | $(BUILD)
	$(CC) -g -O0 $(INCLUDES) -I$(CRITERION_INC) -o $@ $(BIND_TESTS) $(BIND_SRC) $(CRITERION_LDFLAGS)

$(BUILD)/ndec_bench: test/bench/ndec_bench.c test/bench/ndec_bench_base.h test/bench/ndec_bench_parse.h test/bench/payload.h impl/ndec/number.h impl/ndec/core/*.h impl/ndec/*.h | $(BUILD)
	$(CC) $(CFLAGS) $(INCLUDES) -I test -I test/bench -o $@ test/bench/ndec_bench.c

jsonfmt: $(BUILD)/jsonfmt

$(BUILD)/jsonfmt: $(JSONFMT_SRC)  impl/ndec/core/*.h impl/ndec/*.h | $(BUILD)
	$(CC) $(CFLAGS) $(INCLUDES) -o $@ $(JSONFMT_SRC)

debug: $(BUILD)/debug
	$(BUILD)/debug

$(BUILD)/debug: test/debug.c impl/ndec/core/*.h impl/ndec/*.h | $(BUILD)
	$(CC) -g -O0 $(INCLUDES) -o $@ test/debug.c

clean:
	rm -rf $(BUILD)
