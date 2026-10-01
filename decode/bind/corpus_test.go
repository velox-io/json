package bind

// Corpus fixtures live in the benchmark module (benchmark/corpus/testdata);
// this module reaches them over the filesystem. The path anchors to this
// source file so it resolves from any working directory the test binary
// runs in.

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

func loadCorpus(name string) ([]byte, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return nil, errors.New("loadCorpus: cannot locate corpus_test.go")
	}
	gz, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "..", "..", "benchmark", "corpus", "testdata", name+".json.gz"))
	if err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}
