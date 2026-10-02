package service

import (
	"strings"
	"unicode/utf8"

	"github.com/omegaatt36/ytea/domain"
)

// Span is a half-open byte range [Start, End) into a title or channel.
type Span struct{ Start, End int }

// Match is one row that passed a filter: the underlying track index and, per
// query term, the first matched substring in its title and channel.
type Match struct {
	Index   int
	Title   []Span
	Channel []Span
}

// A blank query keeps every track.
func FilterTracks(tracks []domain.Track, query string) []Match {
	terms := strings.Fields(query)
	out := make([]Match, 0, len(tracks))
	for i, t := range tracks {
		m := Match{Index: i}
		ok := true
		for _, term := range terms {
			ts, tok := indexFold(t.Title, term)
			cs, cok := indexFold(t.Channel, term)
			if !tok && !cok {
				ok = false
				break
			}
			if tok {
				m.Title = append(m.Title, ts)
			}
			if cok {
				m.Channel = append(m.Channel, cs)
			}
		}
		if ok {
			out = append(out, m)
		}
	}
	return out
}

// indexFold finds the first case-insensitive occurrence of sub in s and
// returns its byte range in s itself, since case folding can change byte
// lengths and the range must address the original text for highlighting.
func indexFold(s, sub string) (Span, bool) {
	for i := range s {
		if n, ok := prefixFold(s[i:], sub); ok {
			return Span{i, i + n}, true
		}
	}
	return Span{}, false
}

func prefixFold(s, prefix string) (int, bool) {
	n := 0
	for prefix != "" {
		if n >= len(s) {
			return 0, false
		}
		r, rs := utf8.DecodeRuneInString(s[n:])
		p, ps := utf8.DecodeRuneInString(prefix)
		if !strings.EqualFold(string(r), string(p)) {
			return 0, false
		}
		n += rs
		prefix = prefix[ps:]
	}
	return n, true
}
