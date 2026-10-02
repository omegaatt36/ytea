package service

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/omegaatt36/ytea/domain"
)

// Sanitize drops what remote text must not put in front of a user: control
// bytes, which move a terminal's cursor or restyle it, and default-ignorable
// fillers such as U+3164, which width tables count as wide but are drawn as
// nothing.
func Sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) {
			return -1
		}
		return r
	}, s)
}

func DisplayTitle(e domain.PlaylistEntry, t domain.Track) string {
	for _, title := range []string{t.Title, e.Title} {
		if title = Sanitize(title); title != "" {
			return title
		}
	}
	return e.Filename
}

func FormatDuration(d time.Duration) string {
	if d <= 0 {
		return "--:--"
	}
	s := int(d.Round(time.Second).Seconds())
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
