package log

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	_ = w.Close()
	os.Stdout = old
	out := <-done
	_ = r.Close()
	return out
}

func TestInit_JSON_WritesJSON(t *testing.T) {
	orig := slog.Default()
	t.Cleanup(func() { slog.SetDefault(orig) })

	out := captureStdout(t, func() {
		Init("json")
		slog.Default().Info("hello", "k", "v")
	})

	if strings.TrimSpace(out) == "" {
		t.Fatalf("empty output")
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Fatalf("want json output starting with '{', got %q", out)
	}
	if !strings.Contains(out, `"msg":"hello"`) {
		t.Fatalf("want msg=hello in json, got %q", out)
	}
	if !strings.Contains(out, `"k":"v"`) {
		t.Fatalf("want k=v in json, got %q", out)
	}
}

func TestInit_Text_WritesText(t *testing.T) {
	orig := slog.Default()
	t.Cleanup(func() { slog.SetDefault(orig) })

	out := captureStdout(t, func() {
		Init("text")
		slog.Default().Info("hello", "k", "v")
	})

	if strings.TrimSpace(out) == "" {
		t.Fatalf("empty output")
	}
	if strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Fatalf("want text output (not json), got %q", out)
	}
	if !strings.Contains(out, "hello") || !strings.Contains(out, "k=v") {
		t.Fatalf("want message and attrs in text output, got %q", out)
	}
}

func TestWith_ReturnsLogger(t *testing.T) {
	orig := slog.Default()
	t.Cleanup(func() { slog.SetDefault(orig) })

	Init("text")
	l := With("a", 1)
	if l == nil {
		t.Fatalf("With returned nil")
	}
	_ = l
}

