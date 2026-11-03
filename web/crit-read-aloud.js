// crit-read-aloud.js — read the reviewed markdown documents aloud with the
// browser's speech synthesis (Web Speech API). No audio leaves the machine and
// nothing is fetched: the voices are the OS ones the browser exposes.
//
// Two parts:
//   segmentsFromBlocks — pure: Crit line blocks → spoken segments, one per
//                        sentence (long sentences split at clause breaks).
//   createReader       — speaks segments one utterance each, so the page can
//                        highlight the block being read, with pause/resume at
//                        the current word, seek, speed and volume.
//
// Exports onto window.crit.readAloud. No dependencies on other crit modules.

(function () {
  'use strict';

  // Block HTML is already sanitized, but parse it in an inert document anyway
  // so images in it never load while we only want the text.
  function defaultHtmlToText(html) {
    if (typeof DOMParser !== 'undefined') {
      return new DOMParser().parseFromString(html, 'text/html').body.textContent || '';
    }
    return html.replace(/<[^>]*>/g, '');
  }

  // Closing tags that separate words in the rendered text (table cells, list
  // items, headings) — textContent would otherwise run them together.
  var BREAKING_TAGS = /<\/(td|th|li|p|h[1-6]|div|tr|blockquote)>|<br\s*\/?>/gi;

  function hasClass(block, name) {
    return (' ' + (block.cssClass || '') + ' ').indexOf(' ' + name + ' ') !== -1;
  }

  // Chunks longer than this are split at clause breaks (, ; : —) so seeking
  // and resuming stay fine-grained; no piece is cut shorter than MIN_CHUNK.
  var MAX_CHUNK = 160;
  var MIN_CHUNK = 40;

  // A sentence ends at . ! ? or … (plus closing quotes or brackets) followed
  // by whitespace and anything but a lowercase letter, so "e.g. this" stays
  // whole.
  var SENTENCE_END = /[.!?…]+["'”’)\]]*\s+(?![a-z])/g;
  var CLAUSE_BREAK = /[,;:—–]\s+/g;

  // Split `text` at the ends of the regex's matches. Returns [{ text, offset }]
  // with offset the index of the piece in `text`.
  function cutAt(text, re, minLength) {
    var pieces = [];
    var start = 0;
    var match;
    re.lastIndex = 0;
    while ((match = re.exec(text))) {
      var end = match.index + match[0].length;
      if (end - start >= minLength && end < text.length) {
        pieces.push({ text: text.slice(start, end).trim(), offset: start });
        start = end;
      }
    }
    pieces.push({ text: text.slice(start).trim(), offset: start });
    return pieces;
  }

  // Sentences of `text`, each over MAX_CHUNK cut further at clause breaks.
  function splitChunks(text) {
    var chunks = [];
    var sentences = cutAt(text, SENTENCE_END, 1);
    for (var i = 0; i < sentences.length; i++) {
      var sentence = sentences[i];
      if (sentence.text.length <= MAX_CHUNK) {
        chunks.push(sentence);
        continue;
      }
      var clauses = cutAt(sentence.text, CLAUSE_BREAK, MIN_CHUNK);
      for (var j = 0; j < clauses.length; j++) {
        chunks.push({ text: clauses[j].text, offset: sentence.offset + clauses[j].offset });
      }
    }
    return chunks.filter(function (c) { return c.text; });
  }

  // Normalize text the way segments are built (collapse whitespace, trim the
  // start) so a DOM text prefix can be compared with segment offsets.
  function normalizeText(text) {
    return String(text || '').replace(/\s+/g, ' ').replace(/^ /, '');
  }

  // What a code block or diagram is announced as, per document language.
  var ANNOUNCEMENTS = {
    en: { diagram: 'Diagram.', code: 'Code block.' },
    he: { diagram: 'תרשים.', code: 'בלוק קוד.' },
  };

  function announcementsFor(lang) {
    return ANNOUNCEMENTS[String(lang || '').slice(0, 2).toLowerCase()] || ANNOUNCEMENTS.en;
  }

  // Turn one file's line blocks into the segments to speak: a block's text is
  // split into sentences, each its own segment with `offset` = its position
  // in the block's text. Code is announced once ("Code block.") rather than
  // read line by line, and diagrams likewise, in the document's language.
  function segmentsFromBlocks(filePath, blocks, htmlToText, lang) {
    var toText = htmlToText || defaultHtmlToText;
    var announce = announcementsFor(lang);
    var segments = [];
    for (var i = 0; i < (blocks || []).length; i++) {
      var block = blocks[i];
      if (block.isEmpty || hasClass(block, 'table-separator')) continue;
      var chunks;
      if (hasClass(block, 'render-block')) {
        chunks = [{ text: announce.diagram, offset: 0 }];
      } else if (hasClass(block, 'code-line')) {
        if (!hasClass(block, 'code-first')) continue;
        chunks = [{ text: announce.code, offset: 0 }];
      } else {
        chunks = splitChunks(normalizeText(toText(String(block.html || '').replace(BREAKING_TAGS, '$& '))).trim());
      }
      for (var j = 0; j < chunks.length; j++) {
        segments.push({
          filePath: filePath, startLine: block.startLine, endLine: block.endLine,
          text: chunks[j].text, offset: chunks[j].offset,
        });
      }
    }
    return segments;
  }

  // Index of the segment to start from for a block at (filePath, line): the
  // first segment of that file ending at or after the line, else 0.
  function startIndexFor(segments, filePath, line) {
    for (var i = 0; i < segments.length; i++) {
      if (segments[i].filePath === filePath && segments[i].endLine >= line) return i;
    }
    return 0;
  }

  // Where to read from for a click at character `offset` of the block at
  // (filePath, line): { index, charIndex } — the segment holding that offset
  // and the start of the word in it. A line inside a code block lands on its
  // "Code block." announcement. Falls back to startIndexFor.
  function positionAt(segments, filePath, line, offset) {
    var blockLine = null;
    for (var i = 0; i < segments.length; i++) {
      if (segments[i].filePath !== filePath) continue;
      if (segments[i].startLine > line) break;
      blockLine = segments[i].startLine;
    }
    if (blockLine === null) return { index: startIndexFor(segments, filePath, line), charIndex: 0 };
    var at = Math.max(0, offset || 0);
    var found = -1;
    for (var k = 0; k < segments.length; k++) {
      var s = segments[k];
      if (s.filePath !== filePath || s.startLine !== blockLine) continue;
      if (found === -1 || (s.offset || 0) <= at) found = k;
    }
    var segment = segments[found];
    var within = at - (segment.offset || 0);
    if (within <= 0 || within >= segment.text.length) return { index: found, charIndex: 0 };
    return { index: found, charIndex: wordStart(segment.text, within) };
  }

  // Start of the word at `index` in `text`.
  function wordStart(text, index) {
    var i = Math.min(index, text.length);
    while (i > 0 && !/\s/.test(text.charAt(i - 1))) i--;
    return i;
  }

  // Speeds offered by the player, slowest first.
  var RATES = [0.75, 1, 1.25, 1.5, 1.75, 2];

  function clamp(value, min, max) {
    return Math.max(min, Math.min(max, value));
  }

  // Quality rank of a browser voice from its name: the neural voices Edge
  // and Chrome OS expose ("Online (Natural)"), then macOS's downloadable
  // Premium and Enhanced voices, then Google's, then the rest.
  function voiceQuality(voice) {
    var name = String(voice.name || '').toLowerCase();
    if (/natural|neural|online/.test(name)) return 4;
    if (/premium/.test(name)) return 3;
    if (/enhanced/.test(name)) return 2;
    if (/google/.test(name)) return 1;
    return 0;
  }

  // The best voice for a BCP 47 language tag among `voices`, or null when
  // none speaks that language. An exact tag match ("he-IL") outranks a
  // language-only match ("he"); within a tier, the higher quality wins and
  // the earlier voice breaks ties.
  function pickVoice(voices, lang) {
    var want = String(lang || '').toLowerCase();
    if (!want) return null;
    var language = want.split('-')[0];
    var best = null;
    var bestScore = -1;
    for (var i = 0; i < (voices || []).length; i++) {
      var tag = String(voices[i].lang || '').toLowerCase().replace('_', '-');
      if (tag !== want && tag.split('-')[0] !== language) continue;
      var score = (tag === want ? 10 : 0) + voiceQuality(voices[i]);
      if (score > bestScore) {
        best = voices[i];
        bestScore = score;
      }
    }
    return best;
  }

  function resolve(value) {
    return typeof value === 'function' ? value() : value;
  }

  // Speaks with the browser's speech synthesis. opts: { synth, Utterance,
  // lang, voice } — `lang` (a tag, or a function returning one) is set on
  // each utterance; `voice` (a SpeechSynthesisVoice, or a function returning
  // one or null) is used when given, else the best voice for `lang`.
  function createSynthSpeaker(opts) {
    var synth = opts.synth;
    var Utterance = opts.Utterance;
    return {
      speak: function (text, o) {
        var utterance = new Utterance(text);
        utterance.rate = o.rate;
        utterance.volume = o.volume;
        var lang = resolve(opts.lang);
        var voice = resolve(opts.voice) || (lang && synth.getVoices ? pickVoice(synth.getVoices(), lang) : null);
        if (lang) utterance.lang = lang;
        if (voice) utterance.voice = voice;
        utterance.onboundary = function (e) {
          if (e && e.name && e.name !== 'word') return;
          if (e && typeof e.charIndex === 'number') o.onBoundary(e.charIndex);
        };
        utterance.onend = function () { o.onEnd(); };
        utterance.onerror = function () { o.onError(); };
        synth.speak(utterance);
      },
      cancel: function () { synth.cancel(); },
    };
  }

  // Speaks with audio fetched from the server (POST `url`, JSON { text },
  // an audio body back). opts: { fetch, Audio, url, createObjectURL,
  // revokeObjectURL }. Server audio has no word boundaries, so a pause
  // resumes from the start of the sentence. A cancel abandons the request
  // in flight and the audio playing.
  function createAudioSpeaker(opts) {
    var current = null;

    function abandon() {
      var old = current;
      current = null;
      if (!old) return;
      if (old.controller) old.controller.abort();
      if (old.audio) {
        old.audio.onended = null;
        old.audio.onerror = null;
        old.audio.pause();
      }
      if (old.src && opts.revokeObjectURL) opts.revokeObjectURL(old.src);
    }

    return {
      speak: function (text, o) {
        abandon();
        var playback = { controller: typeof AbortController === 'function' ? new AbortController() : null };
        current = playback;
        opts.fetch(opts.url, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ text: text }),
          signal: playback.controller ? playback.controller.signal : undefined,
        }).then(function (res) {
          if (!res.ok) throw new Error('speech request failed: ' + res.status);
          return res.blob();
        }).then(function (blob) {
          if (current !== playback) return;
          playback.src = opts.createObjectURL(blob);
          var audio = new opts.Audio(playback.src);
          playback.audio = audio;
          audio.playbackRate = o.rate;
          audio.volume = o.volume;
          audio.onended = function () {
            if (current !== playback) return;
            abandon();
            o.onEnd();
          };
          audio.onerror = function () {
            if (current !== playback) return;
            abandon();
            o.onError();
          };
          return audio.play();
        }).catch(function () {
          if (current !== playback) return;
          abandon();
          o.onError();
        });
      },
      cancel: abandon,
    };
  }

  // opts: { synth, Utterance, lang, voice, speaker, rate, volume,
  //         onSegment(segment, index), onStateChange(state) } — state is
  //         'stopped' | 'playing' | 'paused'. `speaker` (a synth or audio
  //         speaker above) replaces the default synth speaker.
  //
  // Pause is cancel-and-remember rather than synth.pause(): Chrome's pause is
  // unreliable (it resumes nothing for some voices). The reader follows the
  // word being spoken through the utterance's boundary events, and resuming
  // speaks the segment again from that word. Rate and volume changes restart
  // from the current word the same way, so they apply at once. Voices that
  // fire no boundary events resume from the start of the segment, which is a
  // sentence or less.
  function createReader(opts) {
    var speaker = opts.speaker || createSynthSpeaker(opts);
    var segments = [];
    var index = 0;
    // Character in the current segment to speak from next, and the start of
    // the word being spoken (updated by boundary events).
    var resumeAt = 0;
    var position = 0;
    var state = 'stopped';
    var rate = clamp(Number(opts.rate) || 1, 0.5, 3);
    var volume = clamp(opts.volume == null ? 1 : Number(opts.volume), 0, 1);
    // Bumped whenever the current utterance is abandoned, so the end, error
    // or boundary a browser fires for a cancelled utterance is ignored.
    var generation = 0;

    function setState(value) {
      if (state === value) return;
      state = value;
      if (opts.onStateChange) opts.onStateChange(state);
    }

    function next(gen) {
      index++;
      resumeAt = 0;
      position = 0;
      speakCurrent(gen);
    }

    function speakCurrent(gen) {
      if (gen !== generation) return;
      if (index >= segments.length) {
        index = 0;
        resumeAt = 0;
        position = 0;
        setState('stopped');
        return;
      }
      var segment = segments[index];
      var base = resumeAt;
      var text = segment.text.slice(base);
      position = base;
      if (opts.onSegment) opts.onSegment(segment, index);
      if (!text.trim()) {
        next(gen);
        return;
      }
      speaker.speak(text, {
        rate: rate,
        volume: volume,
        onBoundary: function (charIndex) {
          if (gen !== generation) return;
          position = base + charIndex;
        },
        onEnd: function () {
          if (gen !== generation) return;
          next(gen);
        },
        onError: function () {
          if (gen !== generation) return;
          generation++;
          setState('stopped');
        },
      });
    }

    // Abandon the current utterance and, when playing, speak from `resumeAt`.
    function restart() {
      generation++;
      speaker.cancel();
      if (state === 'playing') speakCurrent(generation);
    }

    function start(list, startIndex) {
      segments = list || [];
      index = clamp(startIndex || 0, 0, Math.max(0, segments.length - 1));
      resumeAt = 0;
      setState(segments.length ? 'playing' : 'stopped');
      restart();
    }

    function stop() {
      setState('stopped');
      resumeAt = 0;
      restart();
    }

    function pause() {
      if (state !== 'playing') return;
      resumeAt = position;
      setState('paused');
      restart();
    }

    function resume() {
      if (state !== 'paused') return;
      setState('playing');
      restart();
    }

    // Jump to a segment, optionally at a character in it (a clicked word).
    // While paused, the segment is shown but not spoken.
    function seek(to, charIndex) {
      if (state === 'stopped' || !segments.length) return;
      index = clamp(to, 0, segments.length - 1);
      resumeAt = clamp(charIndex || 0, 0, segments[index].text.length);
      position = resumeAt;
      if (state === 'paused' && opts.onSegment) opts.onSegment(segments[index], index);
      restart();
    }

    function setRate(value) {
      rate = clamp(Number(value) || 1, 0.5, 3);
      if (state === 'playing') resumeAt = position;
      restart();
    }

    function setVolume(value) {
      volume = clamp(Number(value), 0, 1);
      if (state === 'playing') resumeAt = position;
      restart();
    }

    return {
      start: start,
      stop: stop,
      pause: pause,
      resume: resume,
      seek: seek,
      setRate: setRate,
      setVolume: setVolume,
      state: function () { return state; },
      index: function () { return index; },
      // Character in the current segment where reading is (or resumes).
      charIndex: function () { return state === 'paused' ? resumeAt : position; },
      length: function () { return segments.length; },
    };
  }

  function isSupported(root) {
    return !!(root && root.speechSynthesis && root.SpeechSynthesisUtterance);
  }

  var api = {
    segmentsFromBlocks: segmentsFromBlocks,
    startIndexFor: startIndexFor,
    positionAt: positionAt,
    normalizeText: normalizeText,
    createReader: createReader,
    createSynthSpeaker: createSynthSpeaker,
    createAudioSpeaker: createAudioSpeaker,
    pickVoice: pickVoice,
    RATES: RATES,
    isSupported: isSupported,
  };
  if (typeof window !== 'undefined') {
    window.crit = window.crit || {};
    window.crit.readAloud = api;
  }
  if (typeof module === 'object' && module.exports) {
    module.exports = api;
  }
})();
