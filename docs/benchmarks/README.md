# Benchmarks

Charts plus raw `go test -bench` output, one directory per machine. Bars are
normalized against **JSONv2** (baseline = 1×, shorter is faster); the strip under
each group carries B/op and allocs/op. Compared libraries: Velox, Sonic,
GoJSON, JSONv2.

Add or refresh a machine from the repo root:

```bash
make benchviz CPU_SLUG=amd-epyc7k62
```

## linux-amd64-amd-epyc7k62

| suite | chart | raw |
| --- | --- | --- |
| Unmarshal | [unmarshal-1.svg](linux-amd64-amd-epyc7k62/unmarshal-1.svg) | [unmarshal-1.txt](linux-amd64-amd-epyc7k62/unmarshal-1.txt) |
| Marshal | [marshal-1.svg](linux-amd64-amd-epyc7k62/marshal-1.svg) | [marshal-1.txt](linux-amd64-amd-epyc7k62/marshal-1.txt) |

![](linux-amd64-amd-epyc7k62/unmarshal-1.svg)
![](linux-amd64-amd-epyc7k62/marshal-1.svg)

## darwin-arm64-apple-m4-pro

| suite | chart | raw |
| --- | --- | --- |
| Unmarshal | [unmarshal-1.svg](darwin-arm64-apple-m4-pro/unmarshal-1.svg) | [unmarshal-1.txt](darwin-arm64-apple-m4-pro/unmarshal-1.txt) |
| Marshal | [marshal-1.svg](darwin-arm64-apple-m4-pro/marshal-1.svg) | [marshal-1.txt](darwin-arm64-apple-m4-pro/marshal-1.txt) |

![](darwin-arm64-apple-m4-pro/unmarshal-1.svg)
![](darwin-arm64-apple-m4-pro/marshal-1.svg)

