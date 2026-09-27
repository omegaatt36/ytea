package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/omegaatt36/ytea/internal/youtube"
)

// span is a half-open byte range [start, end) into a title or channel.
type span struct{ start, end int }

// filterMatch is one visible row: the underlying result index and, per query
// term, the first matched substring in its title and channel.
type filterMatch struct {
	index   int
	title   []span
	channel []span
}

// filterResults keeps, in order, the tracks whose title or channel contains
// every space-separated term of query, ignoring case. A blank query keeps all.
func filterResults(tracks []youtube.Track, query string) []filterMatch {
	terms := strings.Fields(query)
	out := make([]filterMatch, 0, len(tracks))
	for i, t := range tracks {
		m := filterMatch{index: i}
		ok := true
		for _, term := range terms {
			ts, tok := indexFold(t.Title, term)
			cs, cok := indexFold(t.Channel, term)
			if !tok && !cok {
				ok = false
				break
			}
			if tok {
				m.title = append(m.title, ts)
			}
			if cok {
				m.channel = append(m.channel, cs)
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
func indexFold(s, sub string) (span, bool) {
	for i := range s {
		if n, ok := prefixFold(s[i:], sub); ok {
			return span{i, i + n}, true
		}
	}
	return span{}, false
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
