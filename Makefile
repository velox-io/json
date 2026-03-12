lint:
	golangci-lint run --fix

lint-ci:
	golangci-lint run --timeout 10m

fmt:
	gofmt -w -s .
	goimports -w .

test:
	go test -race -cover ./...

test-coverage:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

bench:
	cd benchmark && go test -bench=. -benchmem .
	go test -bench=. -benchmem .

clean:
	go clean
	rm -f coverage.out coverage.html cpu.out mem.out

.PHONY: lint lint-ci fmt test test-coverage bench clean
