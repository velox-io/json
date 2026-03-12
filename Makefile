lint:
	golangci-lint run --fix

lint-ci:
	golangci-lint run --timeout 10m

fmt:
	gofmt -w -s .
	goimports -w .

test:
	go test -race .

test-coverage:
	go test -race -coverprofile=coverage.out .
	go tool cover -html=coverage.out -o coverage.html

bench:
	cd benchmark && go test -bench=. -benchmem .
	go test -bench=. -benchmem .

# Run benchmarks (count=5) and save baseline (root + ./benchmark)
bench-baseline:
	@mkdir -p .benchdata
	cd benchmark && go test -bench=Velox -benchmem -count=5 . >> ../.benchdata/baseline.txt 2>/dev/null || true
	@echo "Baseline saved to .benchdata/baseline.txt"

# Check for regressions against baseline (default threshold: 10%)
bench-check:
	@./scripts/benchcheck.sh

# Check with custom threshold: make bench-check-threshold THRESHOLD=5
bench-check-threshold:
	@./scripts/benchcheck.sh .benchdata/baseline.txt $(or $(THRESHOLD),10)

clean:
	go clean
	rm -f coverage.out coverage.html cpu.out mem.out

FUZZ_TIME ?= 30s

fuzz:
	go test -fuzz=FuzzUnmarshalAny -fuzztime=$(FUZZ_TIME) .
	go test -fuzz=FuzzUnmarshalStruct -fuzztime=$(FUZZ_TIME) .
	go test -fuzz=FuzzUnmarshalNested -fuzztime=$(FUZZ_TIME) .
	go test -fuzz=FuzzNoCrash -fuzztime=$(FUZZ_TIME) .

.PHONY: lint lint-ci fmt test test-coverage bench bench-baseline bench-check bench-check-threshold clean fuzz
