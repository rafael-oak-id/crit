// Package translate renders a markdown document in another language with
// Claude, so the reviewed document can be shown and read aloud in that
// language. Translations are cached on disk by content, so reopening a
// document never pays for the same translation twice.
package translate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Model is the Claude model every backend translates with.
const Model = "claude-opus-5-5"

// promptVersion is part of the cache key: bumping it invalidates every cached
// translation made with an older prompt.
const promptVersion = "1"

// ErrNoCredentials is returned by New when no way to reach Claude exists.
var ErrNoCredentials = errors.New("no Claude credentials: export ANTHROPIC_API_KEY, or install Claude Code and run `claude` then /login")

// backend is one way of asking Claude for a translation.
type backend interface {
	translate(ctx context.Context, system, markdown string) (string, error)
}

// Translator translates markdown documents and caches the results.
type Translator struct {
	backend  backend
	name     string
	cacheDir string
}

// New picks the first available way to reach Claude: the API when
// ANTHROPIC_API_KEY is set, else the signed-in Claude Code CLI.
func New() (*Translator, error) {
	userCache, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("translate: locate cache dir: %w", err)
	}
	cacheDir := filepath.Join(userCache, "crit", "translations")
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return &Translator{backend: newAPIBackend(), name: "claude-api", cacheDir: cacheDir}, nil
	}
	if path, err := exec.LookPath("claude"); err == nil {
		return &Translator{backend: &cliBackend{path: path}, name: "claude-code", cacheDir: cacheDir}, nil
	}
	return nil, ErrNoCredentials
}

// newWithBackend is for tests: a translator over a fake backend and cache dir.
func newWithBackend(b backend, cacheDir string) *Translator {
	return &Translator{backend: b, name: "test", cacheDir: cacheDir}
}

// Backend names how Claude is reached ("claude-api" or "claude-code").
func (t *Translator) Backend() string {
	return t.name
}

// languages maps the language codes the UI offers to the name used in the
// prompt.
var languages = map[string]string{
	"he": "Hebrew",
}

// Translate returns `markdown` rendered in the language `lang` ("he"),
// serving a cached translation of the same document when one exists.
func (t *Translator) Translate(ctx context.Context, markdown, lang string) (string, error) {
	languageName, ok := languages[lang]
	if !ok {
		return "", fmt.Errorf("translate: unsupported language %q", lang)
	}
	key := cacheKey(markdown, lang)
	if cached, ok := t.readCache(key); ok {
		return cached, nil
	}
	out, err := t.backend.translate(ctx, systemPrompt(languageName), markdown)
	if err != nil {
		return "", fmt.Errorf("translate with %s: %w", t.name, err)
	}
	out = unwrapFence(out)
	if strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("translate with %s: empty translation", t.name)
	}
	t.writeCache(key, out)
	return out, nil
}

func cacheKey(markdown, lang string) string {
	sum := sha256.Sum256([]byte(promptVersion + "\n" + lang + "\n" + markdown))
	return hex.EncodeToString(sum[:])
}

func (t *Translator) cachePath(key string) string {
	return filepath.Join(t.cacheDir, key+".md")
}

func (t *Translator) readCache(key string) (string, bool) {
	data, err := os.ReadFile(t.cachePath(key))
	if err != nil {
		return "", false
	}
	return string(data), true
}

// writeCache stores a translation; a cache that cannot be written only costs
// a repeat translation, so the failure is not surfaced.
func (t *Translator) writeCache(key, content string) {
	if err := os.MkdirAll(t.cacheDir, 0o755); err != nil {
		return
	}
	tmp := t.cachePath(key) + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, t.cachePath(key)); err != nil {
		if removeErr := os.Remove(tmp); removeErr != nil {
			return
		}
	}
}

// unwrapFence strips a single code fence a model may wrap the whole document
// in despite the prompt, returning any other text unchanged.
func unwrapFence(out string) string {
	trimmed := strings.TrimSpace(out)
	if !strings.HasPrefix(trimmed, "```") || !strings.HasSuffix(trimmed, "```") {
		return out
	}
	firstBreak := strings.Index(trimmed, "\n")
	if firstBreak == -1 {
		return out
	}
	body := trimmed[firstBreak+1 : len(trimmed)-3]
	if strings.Contains(body, "```") {
		// The fence closes an inner code block, not the whole document.
		return out
	}
	return body
}

func systemPrompt(languageName string) string {
	return strings.ReplaceAll(`You are a professional translator rendering technical documentation from English into LANGUAGE for software engineers who read LANGUAGE natively.

Translate the user's Markdown document into LANGUAGE.

Output: only the translated document. No preamble, no notes, no code fence around the document.

Structure: preserve the Markdown exactly: headings and their levels, list nesting and markers, tables with the same rows and columns, block quotes, horizontal rules, blank lines, emphasis and inline formatting. Translate link text and image alt text; never change a URL.

Code: leave every code span and fenced code block byte for byte unchanged, including the fence info string and comments inside code.

Terms: keep identifiers, file paths, commands, flags, environment variables, product names, API names and acronyms in their original Latin spelling (for example crit, GitHub, JSON, API, pull request numbers). Use the standard LANGUAGE terminology of professional software documentation. When a LANGUAGE term is established, use it; when the English term is what practitioners actually say, keep the English term. Add the English term in parentheses only on first use and only when the LANGUAGE term alone would be unclear.

Quality: write natural, fluent, grammatically correct modern LANGUAGE as a senior technical writer would, with correct gender and number agreement and correct construct forms. Translate meaning rather than words, restructuring a sentence where LANGUAGE word order or idiom requires it, but never add, omit or alter information. Keep numbers, dates, versions and units as written. Translate each term the same way throughout the document.

The translation will be read aloud by a screen reader sentence by sentence, so end every sentence with a period, question mark or exclamation mark.`, "LANGUAGE", languageName)
}
