package tests

import (
	"bytes"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	vjson "github.com/velox-io/json"
)

// TestDecoder_StreamUTF8_NoCorruption verifies that streaming decode from an
// HTTP request body does not corrupt multi-byte UTF-8 characters.
func TestDecoder_StreamUTF8_NoCorruption(t *testing.T) {
	type Msg struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	body := strings.Repeat("日本語更新テスト ", 320)
	input := `{"title":"utf8 repro","body":"` + body + `"}`

	var (
		mu      sync.Mutex
		decErr  error
		gotBody string
	)

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var msg Msg
			if e := vjson.NewDecoder(r.Body).Decode(&msg); e != nil {
				mu.Lock()
				decErr = e
				mu.Unlock()
				return
			}
			mu.Lock()
			gotBody = msg.Body
			mu.Unlock()
		}),
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = srv.Serve(ln)
	}()

	time.Sleep(50 * time.Millisecond)

	resp, err := http.Post("http://"+ln.Addr().String(), "application/json", bytes.NewBufferString(input))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	srv.Close()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	if decErr != nil {
		t.Fatal("decode error:", decErr)
	}

	if strings.Contains(gotBody, "\uFFFD") {
		t.Error("decoded body contains U+FFFD (replacement character): multi-byte UTF-8 corrupted during stream decode")
	}
	if gotBody != body {
		t.Errorf("decoded body mismatch\nwant len=%d\n got len=%d", len(body), len(gotBody))
	}
}
