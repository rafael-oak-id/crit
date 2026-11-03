package tts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSpeakRequestsElevenLabsAndCaches(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/text-to-speech/voice123" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("output_format") != "mp3_44100_128" {
			t.Errorf("output_format = %s", r.URL.Query().Get("output_format"))
		}
		if r.Header.Get("xi-api-key") != "key" {
			t.Errorf("xi-api-key header = %q", r.Header.Get("xi-api-key"))
		}
		var req speechRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if req.Text != "שלום עולם." || req.ModelID != "eleven_v3" {
			t.Errorf("request = %+v", req)
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		if _, err := w.Write([]byte("MP3DATA")); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	defer server.Close()

	synth := newSynth("key", "voice123", server.URL, t.TempDir())
	first, err := synth.Speak(context.Background(), "שלום עולם.")
	if err != nil {
		t.Fatalf("first speak: %v", err)
	}
	second, err := synth.Speak(context.Background(), "שלום עולם.")
	if err != nil {
		t.Fatalf("second speak: %v", err)
	}
	if string(first) != "MP3DATA" || string(second) != "MP3DATA" {
		t.Fatalf("audio = %q, %q", first, second)
	}
	if calls != 1 {
		t.Fatalf("elevenlabs called %d times, want 1 (second call should hit the cache)", calls)
	}
}

func TestSpeakSurfacesProviderErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"detail":"quota exceeded"}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	synth := newSynth("key", "voice123", server.URL, t.TempDir())
	_, err := synth.Speak(context.Background(), "שלום.")
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("got %v, want an error naming the status and detail", err)
	}
}

func TestSpeakRejectsEmptyAudio(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	synth := newSynth("key", "voice123", server.URL, t.TempDir())
	if _, err := synth.Speak(context.Background(), "שלום."); err == nil {
		t.Fatal("expected an error for an empty audio response")
	}
}
