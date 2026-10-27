package tokenkit

import (
	"math"
	"unicode"
	"unicode/utf8"
)

type streamWordType int

const (
	streamWordNone streamWordType = iota
	streamWordLatin
	streamWordNumber
)

// StreamingEstimate incrementally estimates completion tokens with
// results identical to Estimate on the concatenated text.
// Feed each Responses->Chat usage-text chunk (output text, reasoning with its
// separator, tool arguments then tool name) via WriteString; call Estimate
// once the stream completes. Incomplete trailing UTF-8 bytes are buffered
// until the next chunk so byte-split multi-byte runes count exactly once.
type StreamingEstimate struct {
	multipliers weights
	count       float64
	wordType    streamWordType
	pending     []byte
	hasInput    bool
}

// NewStreamingEstimate creates an incremental estimator for a model.
// Provider selection mirrors Estimate.
func NewStreamingEstimate(model string) *StreamingEstimate {
	w, ok := familyWeights[ModelFamily(model)]
	if !ok {
		w = familyWeights[FamilyUnknown]
	}
	return &StreamingEstimate{multipliers: w}
}

// WriteString feeds one usage-text chunk. Chunks must be supplied in the same
// order the converter would append them to its usage text.
func (s *StreamingEstimate) WriteString(chunk string) {
	if s == nil || chunk == "" {
		return
	}
	s.hasInput = true
	if len(s.pending) > 0 {
		combined := make([]byte, 0, len(s.pending)+len(chunk))
		combined = append(combined, s.pending...)
		combined = append(combined, chunk...)
		s.pending = s.pending[:0]
		s.writeBytes(combined)
		return
	}
	if utf8.ValidString(chunk) {
		s.writeValidString(chunk)
		return
	}
	s.writeBytes([]byte(chunk))
}

// Write feeds raw bytes, buffering an incomplete trailing rune.
func (s *StreamingEstimate) Write(p []byte) {
	if s == nil || len(p) == 0 {
		return
	}
	s.hasInput = true
	if len(s.pending) > 0 {
		combined := make([]byte, 0, len(s.pending)+len(p))
		combined = append(combined, s.pending...)
		combined = append(combined, p...)
		s.pending = s.pending[:0]
		s.writeBytes(combined)
		return
	}
	s.writeBytes(p)
}

// Tokens snapshots the incremental count identically to Estimate.
func (s *StreamingEstimate) Tokens() int {
	if s == nil {
		return 0
	}
	snapshot := *s
	if len(snapshot.pending) > 0 {
		for _, r := range string(snapshot.pending) {
			snapshot.countRune(snapshot.multipliers, r)
		}
	}
	if !snapshot.hasInput {
		return 0
	}
	return int(math.Ceil(snapshot.count))
}

func (s *StreamingEstimate) writeValidString(chunk string) {
	m := s.multipliers
	for _, r := range chunk {
		s.countRune(m, r)
	}
}

func (s *StreamingEstimate) writeBytes(p []byte) {
	m := s.multipliers
	for len(p) > 0 {
		if !utf8.FullRune(p) {
			rest := make([]byte, len(p))
			copy(rest, p)
			s.pending = rest
			return
		}
		r, size := utf8.DecodeRune(p)
		s.countRune(m, r)
		p = p[size:]
	}
}

func (s *StreamingEstimate) countRune(m weights, r rune) {
	if unicode.IsSpace(r) {
		s.wordType = streamWordNone
		if r == '\n' || r == '\t' {
			s.count += m.Newline
		} else {
			s.count += m.Space
		}
		return
	}
	if isCJK(r) {
		s.wordType = streamWordNone
		s.count += m.CJK
		return
	}
	if isEmoji(r) {
		s.wordType = streamWordNone
		s.count += m.Emoji
		return
	}
	if unicode.IsLetter(r) || unicode.IsNumber(r) {
		isNum := unicode.IsNumber(r)
		newType := streamWordLatin
		if isNum {
			newType = streamWordNumber
		}
		if s.wordType == streamWordNone || s.wordType != newType {
			if newType == streamWordNumber {
				s.count += m.Number
			} else {
				s.count += m.Word
			}
			s.wordType = newType
		}
		return
	}
	s.wordType = streamWordNone
	if isMathSymbol(r) {
		s.count += m.MathSymbol
	} else if r == '@' {
		s.count += m.AtSign
	} else if isURLDelim(r) {
		s.count += m.URLDelim
	} else {
		s.count += m.Symbol
	}
}
