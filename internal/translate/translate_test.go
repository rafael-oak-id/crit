package translate

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeBackend struct {
	calls  int
	output string
	err    error
	system string
}

func (f *fakeBackend) translate(_ context.Context, system, _ string) (string, error) {
	f.calls++
	f.system = system
	return f.output, f.err
}

func TestTranslateCachesByContentAndLanguage(t *testing.T) {
	backend := &fakeBackend{output: "# שלום\n"}
	translator := newWithBackend(backend, t.TempDir())

	first, err := translator.Translate(context.Background(), "# Hello\n", "he")
	if err != nil {
		t.Fatalf("first translate: %v", err)
	}
	second, err := translator.Translate(context.Background(), "# Hello\n", "he")
	if err != nil {
		t.Fatalf("second translate: %v", err)
	}
	if first != "# שלום\n" || second != first {
		t.Fatalf("got %q then %q", first, second)
	}
	if backend.calls != 1 {
		t.Fatalf("backend called %d times, want 1 (second call should hit the cache)", backend.calls)
	}
	if _, err := translator.Translate(context.Background(), "# Hello again\n", "he"); err != nil {
		t.Fatalf("third translate: %v", err)
	}
	if backend.calls != 2 {
		t.Fatalf("backend called %d times, want 2 (different content is not cached)", backend.calls)
	}
	if !strings.Contains(backend.system, "into Hebrew") {
		t.Fatalf("system prompt does not name the language: %q", backend.system)
	}
}

func TestTranslateRejectsUnknownLanguage(t *testing.T) {
	backend := &fakeBackend{output: "x"}
	translator := newWithBackend(backend, t.TempDir())
	if _, err := translator.Translate(context.Background(), "# Hello\n", "xx"); err == nil {
		t.Fatal("expected an error for an unsupported language")
	}
	if backend.calls != 0 {
		t.Fatalf("backend called %d times for an unsupported language", backend.calls)
	}
}

func TestTranslateSurfacesBackendFailure(t *testing.T) {
	boom := errors.New("boom")
	translator := newWithBackend(&fakeBackend{err: boom}, t.TempDir())
	_, err := translator.Translate(context.Background(), "# Hello\n", "he")
	if !errors.Is(err, boom) {
		t.Fatalf("got %v, want wrapped boom", err)
	}
}

func TestTranslateRejectsEmptyTranslation(t *testing.T) {
	translator := newWithBackend(&fakeBackend{output: "  \n"}, t.TempDir())
	if _, err := translator.Translate(context.Background(), "# Hello\n", "he"); err == nil {
		t.Fatal("expected an error for an empty translation")
	}
}

func TestUnwrapFence(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain document", in: "# שלום\n\nטקסט.\n", want: "# שלום\n\nטקסט.\n"},
		{name: "whole document fenced", in: "```markdown\n# שלום\n\nטקסט.\n```\n", want: "# שלום\n\nטקסט.\n"},
		{name: "document with its own code block", in: "# שלום\n\n```go\nfmt.Println()\n```\n", want: "# שלום\n\n```go\nfmt.Println()\n```\n"},
		{name: "fenced document containing a code block", in: "```\n# שלום\n\n```go\nx\n```\n```", want: "```\n# שלום\n\n```go\nx\n```\n```"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := unwrapFence(tt.in); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
