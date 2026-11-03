package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tomasz-tomczyk/crit/internal/translate"
	"github.com/tomasz-tomczyk/crit/internal/tts"
)

// documentTranslator renders a markdown document in another language.
type documentTranslator interface {
	Translate(ctx context.Context, markdown, lang string) (string, error)
	Backend() string
}

// speechSynthesizer turns one sentence into audio.
type speechSynthesizer interface {
	Speak(ctx context.Context, text string) ([]byte, error)
	Name() string
}

// maxSpeechRequestBytes bounds a /api/tts body: one sentence or clause of a
// document, never a whole document.
const maxSpeechRequestBytes = 16 << 10

// readAloudServices wires the optional translation and speech providers from
// the environment. A missing provider leaves its field nil and, for
// translation, records why, so /api/config can tell the UI.
func (s *Server) readAloudServices() {
	translator, err := translate.New()
	if err != nil {
		s.translateErr = err
	} else {
		s.translator = translator
	}
	if synth, ok := tts.New(); ok {
		s.synth = synth
	}
}

func (s *Server) translateBackend() string {
	if s.translator == nil {
		return ""
	}
	return s.translator.Backend()
}

func (s *Server) translateError() string {
	if s.translateErr == nil {
		return ""
	}
	return s.translateErr.Error()
}

func (s *Server) ttsBackend() string {
	if s.synth == nil {
		return ""
	}
	return s.synth.Name()
}

// handleFileTranslate returns a session file's content translated into the
// language `lang`: GET /api/file/translate?path=<path>&lang=he.
func (s *Server) handleFileTranslate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.translator == nil {
		http.Error(w, "Translation unavailable: "+s.translateError(), http.StatusServiceUnavailable)
		return
	}
	path := r.URL.Query().Get("path")
	lang := r.URL.Query().Get("lang")
	if path == "" || lang == "" {
		http.Error(w, "path and lang query parameters required", http.StatusBadRequest)
		return
	}
	content, ok := s.session.Load().GetFileContent(path)
	if !ok {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	translated, err := s.translator.Translate(r.Context(), content, lang)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]string{"content": translated, "lang": lang})
}

type speechRequest struct {
	Text string `json:"text"`
}

// handleTTS speaks one sentence: POST /api/tts {"text": "..."} returns MP3 audio.
func (s *Server) handleTTS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.synth == nil {
		http.Error(w, "Speech synthesis unavailable: set ELEVENLABS_API_KEY", http.StatusNotFound)
		return
	}
	var req speechRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSpeechRequestBytes)).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Text == "" {
		http.Error(w, "text required", http.StatusBadRequest)
		return
	}
	audio, err := s.synth.Speak(r.Context(), req.Text)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(audio); err != nil {
		return
	}
}
