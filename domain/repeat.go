package domain

type Repeat string

const (
	RepeatOff Repeat = "off"
	RepeatAll Repeat = "all"
	RepeatOne Repeat = "one"
)

func (r Repeat) String() string {
	return string(r)
}

// ParseRepeat is the inverse of String; unknown values are RepeatOff.
func ParseRepeat(s string) Repeat {
	switch r := Repeat(s); r {
	case RepeatOff, RepeatAll, RepeatOne:
		return r
	}
	return RepeatOff
}

// Next is the mode after r in the cycle off → all → one → off.
func (r Repeat) Next() Repeat {
	switch r {
	case RepeatAll:
		return RepeatOne
	case RepeatOne:
		return RepeatOff
	case RepeatOff:
	}
	return RepeatAll
}

// Single-file looping wins because it keeps the player on the current track.
func RepeatFrom(loopPlaylist, loopFile bool) Repeat {
	switch {
	case loopFile:
		return RepeatOne
	case loopPlaylist:
		return RepeatAll
	default:
		return RepeatOff
	}
}
