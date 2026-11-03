package server

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeTranslator struct {
	gotMarkdown string
	gotLang     string
	err         error
}

func (f *fakeTranslator) Translate(_ context.Context, markdown, lang string) (string, error) {
	f.gotMarkdown = markdown
	f.gotLang = lang
	if f.err != nil {
		return "", f.err
	}
	return "# תורגם\n", nil
}

func (f *fakeTranslator) Backend() string { return "fake" }

type fakeSynth struct {
	gotText string
	err     error
}

func (f *fakeSynth) Speak(_ context.Context, text string) ([]byte, error) {
	f.gotText = text
	if f.err != nil {
		return nil, f.err
	}
	return []byte("MP3"), nil
}

func (f *fakeSynth) Name() string { return "fake" }

func TestHandleFileTranslate(t *testing.T) {
	srv, _ := NewTestServer(t)
	translator := &fakeTranslator{}
	srv.translator = translator

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/file/translate?path=test.md&lang=he", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if translator.gotMarkdown != "line1\nline2\nline3\n" || translator.gotLang != "he" {
		t.Fatalf("translator got %q / %q", translator.gotMarkdown, translator.gotLang)
	}
	if !strings.Contains(rec.Body.String(), `"content":"# תורגם\n"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestHandleFileTranslateErrors(t *testing.T) {
	tests := []struct {
		name       string
		translator documentTranslator
		url        string
		wantStatus int
	}{
		{name: "no translator configured", url: "/api/file/translate?path=test.md&lang=he", wantStatus: http.StatusServiceUnavailable},
		{name: "missing lang", translator: &fakeTranslator{}, url: "/api/file/translate?path=test.md", wantStatus: http.StatusBadRequest},
		{name: "unknown file", translator: &fakeTranslator{}, url: "/api/file/translate?path=missing.md&lang=he", wantStatus: http.StatusNotFound},
		{name: "translator failure", translator: &fakeTranslator{err: errors.New("boom")}, url: "/api/file/translate?path=test.md&lang=he", wantStatus: http.StatusBadGateway},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := NewTestServer(t)
			// NewServer wires the real translator when the machine has Claude
			// credentials; the test decides what the server has.
			srv.translator = nil
			srv.translateErr = errors.New("no credentials")
			if tt.translator != nil {
				srv.translator = tt.translator
			}
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.url, nil))
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestHandleTTS(t *testing.T) {
	srv, _ := NewTestServer(t)
	synth := &fakeSynth{}
	srv.synth = synth

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/tts", bytes.NewBufferString(`{"text":"שלום."}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "audio/mpeg" || rec.Body.String() != "MP3" {
		t.Fatalf("content-type = %q, body = %q", rec.Header().Get("Content-Type"), rec.Body.String())
	}
	if synth.gotText != "שלום." {
		t.Fatalf("synth got %q", synth.gotText)
	}
}

func TestHandleTTSErrors(t *testing.T) {
	tests := []struct {
		name       string
		synth      speechSynthesizer
		body       string
		wantStatus int
	}{
		{name: "no synth configured", body: `{"text":"x"}`, wantStatus: http.StatusNotFound},
		{name: "empty text", synth: &fakeSynth{}, body: `{"text":""}`, wantStatus: http.StatusBadRequest},
		{name: "invalid json", synth: &fakeSynth{}, body: `{`, wantStatus: http.StatusBadRequest},
		{name: "provider failure", synth: &fakeSynth{err: errors.New("boom")}, body: `{"text":"x"}`, wantStatus: http.StatusBadGateway},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := NewTestServer(t)
			if tt.synth != nil {
				srv.synth = tt.synth
			}
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/tts", bytes.NewBufferString(tt.body)))
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}
