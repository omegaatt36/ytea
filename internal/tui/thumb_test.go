package tui

import (
	"image"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"

	"github.com/omegaatt36/ytea/internal/thumbnail"
)

func TestNextThumbIDStaysInPaletteRange(t *testing.T) {
	tests := []struct {
		prev, want int
	}{
		{prev: 0, want: 16},
		{prev: 16, want: 17},
		{prev: 254, want: 255},
		{prev: 255, want: 16},
	}
	for _, tt := range tests {
		if got := nextThumbID(tt.prev); got != tt.want {
			t.Errorf("nextThumbID(%d) = %d, want %d", tt.prev, got, tt.want)
		}
	}
}

func TestGraphicsProbe(t *testing.T) {
	reply := func(id int, payload string) uv.KittyGraphicsEvent {
		return uv.KittyGraphicsEvent{Options: kitty.Options{ID: id}, Payload: []byte(payload)}
	}
	plainOK := reply(thumbnail.QueryID, "OK")
	placeholderOK := reply(thumbnail.PlaceholderQueryID, "OK")
	placeholderRejected := reply(thumbnail.PlaceholderQueryID, "ENOTSUPPORTED:unicode placeholders are not supported")
	da1 := uv.PrimaryDeviceAttributesEvent{62, 22}

	tests := []struct {
		name   string
		events []tea.Msg
		want   graphicsSupport
	}{
		{name: "Ghostty: both probes OK", events: []tea.Msg{plainOK, placeholderOK, da1}, want: graphicsPlaceholder},
		{name: "Zellij 0.45: placeholders rejected", events: []tea.Msg{plainOK, placeholderRejected, da1}, want: graphicsDirect},
		{name: "Zellij 0.44 or tmux: only DA1", events: []tea.Msg{da1}, want: graphicsNone},
		{name: "replies after DA1 are ignored", events: []tea.Msg{da1, plainOK, placeholderOK}, want: graphicsNone},
		{name: "undecided until DA1", events: []tea.Msg{plainOK, placeholderOK}, want: graphicsUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m tea.Model = New(Deps{Thumbnails: true})
			for _, ev := range tt.events {
				m, _ = m.Update(ev)
			}
			if got := m.(Model).thumb.graphics; got != tt.want {
				t.Errorf("graphics = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestApplyThumbPicksRendering(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 32, 18))

	tests := []struct {
		name      string
		graphics  graphicsSupport
		wantKitty bool
	}{
		{name: "kitty placeholders", graphics: graphicsPlaceholder, wantKitty: true},
		{name: "kitty direct placement, e.g. Zellij 0.45", graphics: graphicsDirect, wantKitty: true},
		{name: "half-block fallback", graphics: graphicsNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, cmd := playingModel(tt.graphics).update(thumbMsg{videoID: "abc", img: img})
			gm := got.(Model)

			if gotKitty := gm.thumb.id != 0; gotKitty != tt.wantKitty {
				t.Errorf("kitty image = %v, want %v", gotKitty, tt.wantKitty)
			}
			if gotArt := gm.thumb.art != ""; gotArt == tt.wantKitty {
				t.Errorf("half-block art = %v, want %v", gotArt, !tt.wantKitty)
			}
			if (cmd != nil) != tt.wantKitty {
				t.Errorf("cmd = %v, want out-of-band transmit only for kitty", cmd)
			}
			if w := lipgloss.Width(gm.renderPlayer()); w != 100 {
				t.Errorf("now playing width = %d, want 100", w)
			}
		})
	}
}

func TestThumbOriginIsInsideNowPlayingBox(t *testing.T) {
	for _, viz := range []bool{false, true} {
		m := playingModel(graphicsDirect)
		m.thumb.id, m.showViz = 16, viz

		at := m.thumbOrigin()
		lines := strings.Split(ansi.Strip(m.render()), "\n")
		if corner := []rune(lines[at.Y-1])[at.X-boxInset]; corner != '╭' {
			t.Errorf("viz=%v: box corner left of the row above thumbOrigin %v = %q, want ╭", viz, at, corner)
		}
		if side := []rune(lines[at.Y])[at.X-boxInset]; side != '│' {
			t.Errorf("viz=%v: box side left of thumbOrigin = %q, want │", viz, side)
		}
	}
}

func TestSyncPlacement(t *testing.T) {
	m := playingModel(graphicsDirect)
	m.thumb.id = 16

	if cmd := m.syncPlacement(); cmd == nil {
		t.Fatal("first sync: cmd = nil, want placement scheduled")
	}
	if cmd := m.syncPlacement(); cmd != nil {
		t.Error("unchanged layout: cmd != nil, want no re-placement")
	}

	m.height += 4 // moves the now-playing box
	if cmd := m.syncPlacement(); cmd == nil {
		t.Error("layout moved: cmd = nil, want re-placement")
	}

	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if !next.(Model).thumb.placed {
		t.Error("after resize: placed = false, want a placement rescheduled by Update")
	}
	if _, cmd := m.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height}); cmd == nil {
		t.Error("same-size resize: cmd = nil, want re-placement since the terminal drops placed images")
	}

	if _, cmd := m.update(placeMsg{id: 99, at: m.thumb.placedAt}); cmd != nil {
		t.Errorf("placeMsg for a replaced image: cmd = %v, want nil", cmd)
	}
	if _, cmd := m.update(placeMsg{id: 16, at: m.thumb.placedAt}); cmd == nil {
		t.Error("placeMsg for the current image: cmd = nil, want Put")
	}
}
