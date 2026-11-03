// Package tts turns document text into speech with ElevenLabs, for languages
// where the browser's own voices fall short. Audio is cached on disk by text,
// so pausing, seeking and rereading a sentence never pays for it twice.
package tts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	// model is the ElevenLabs model that speaks Hebrew (and 70 other
	// languages) natively; the older multilingual models do not.
	model = "eleven_v3"
	// defaultVoice is ElevenLabs' premade "Rachel" voice, which any account can use.
	defaultVoice   = "21m00Tcm4TlvDq8ikWAM"
	defaultBaseURL = "https://api.elevenlabs.io"
	outputFormat   = "mp3_44100_128"
	requestTimeout = 60 * time.Second
	// maxResponseBytes bounds one sentence of audio; a sentence is at most a
	// few hundred characters.
	maxResponseBytes = 8 << 20
)

// Synth speaks text through ElevenLabs.
type Synth struct {
	apiKey   string
	voice    string
	baseURL  string
	cacheDir string
	client   *http.Client
}

// New reads ELEVENLABS_API_KEY and the optional CRIT_TTS_VOICE voice id from
// the environment. The second result is false when no key is set.
func New() (*Synth, bool) {
	apiKey := os.Getenv("ELEVENLABS_API_KEY")
	if apiKey == "" {
		return nil, false
	}
	userCache, err := os.UserCacheDir()
	if err != nil {
		return nil, false
	}
	voice := os.Getenv("CRIT_TTS_VOICE")
	if voice == "" {
		voice = defaultVoice
	}
	return newSynth(apiKey, voice, defaultBaseURL, filepath.Join(userCache, "crit", "tts")), true
}

func newSynth(apiKey, voice, baseURL, cacheDir string) *Synth {
	return &Synth{
		apiKey:   apiKey,
		voice:    voice,
		baseURL:  baseURL,
		cacheDir: cacheDir,
		client:   &http.Client{Timeout: requestTimeout},
	}
}

// Name identifies the provider to the UI.
func (s *Synth) Name() string {
	return "elevenlabs"
}

type speechRequest struct {
	Text    string `json:"text"`
	ModelID string `json:"model_id"`
}

// Speak returns MP3 audio of `text`, from the cache when the same text was
// spoken before.
func (s *Synth) Speak(ctx context.Context, text string) ([]byte, error) {
	key := s.cacheKey(text)
	if audio, err := os.ReadFile(s.cachePath(key)); err == nil {
		return audio, nil
	}
	audio, err := s.fetch(ctx, text)
	if err != nil {
		return nil, err
	}
	s.writeCache(key, audio)
	return audio, nil
}

func (s *Synth) fetch(ctx context.Context, text string) ([]byte, error) {
	body, err := json.Marshal(speechRequest{Text: text, ModelID: model})
	if err != nil {
		return nil, fmt.Errorf("tts: encode request: %w", err)
	}
	url := s.baseURL + "/v1/text-to-speech/" + s.voice + "?output_format=" + outputFormat
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("tts: build request: %w", err)
	}
	req.Header.Set("xi-api-key", s.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/mpeg")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tts: elevenlabs request: %w", err)
	}
	defer resp.Body.Close()
	audio, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("tts: read elevenlabs response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tts: elevenlabs returned %d: %s", resp.StatusCode, bytes.TrimSpace(audio))
	}
	if len(audio) == 0 {
		return nil, fmt.Errorf("tts: elevenlabs returned no audio")
	}
	return audio, nil
}

func (s *Synth) cacheKey(text string) string {
	sum := sha256.Sum256([]byte(model + "\n" + s.voice + "\n" + text))
	return hex.EncodeToString(sum[:])
}

func (s *Synth) cachePath(key string) string {
	return filepath.Join(s.cacheDir, key+".mp3")
}

// writeCache stores audio; a cache that cannot be written only costs a
// repeat request, so the failure is not surfaced.
func (s *Synth) writeCache(key string, audio []byte) {
	if err := os.MkdirAll(s.cacheDir, 0o755); err != nil {
		return
	}
	tmp := s.cachePath(key) + ".tmp"
	if err := os.WriteFile(tmp, audio, 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, s.cachePath(key)); err != nil {
		if removeErr := os.Remove(tmp); removeErr != nil {
			return
		}
	}
}
