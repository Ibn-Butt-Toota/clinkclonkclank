package session

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
)

var keyword = "chicken"

// this is dum
type asrOutBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (a *asrOutBuffer) Write(p []byte) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	slog.Info(string(p))

	str := strings.ToLower(string(p))
	idx := strings.LastIndex(str, keyword) // search from right to left because the keyword will be near the end

	if idx == -1 {
		return a.buf.Write(p)
	}
	slog.Info("found", "keyword", keyword)
	return a.buf.Write(p[:idx])
}

func (a *asrOutBuffer) String() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.buf.String()
}

type KeywordWatcher struct {
	tail      string
	lowertail string
	onKeyword func()
}

// if the keyword is present, writes all text minus the keyword and calls onKeyword().
func (k *KeywordWatcher) Write(p []byte) (int, error) {
	chunk := k.tail + string(p)
	lowerchunk := k.lowertail + strings.ToLower(string(p))

	if strings.Contains(lowerchunk, keyword) {
		k.onKeyword()
	}

	k.tail = chunk
	if len(chunk) > 6 {
		k.tail = chunk[len(chunk)-6:]
	}

	return len(p), nil
}
