package tui

import (
	"context"
	"fmt"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/omegaatt36/ytea/internal/mpv"
	"github.com/omegaatt36/ytea/internal/youtube"
)

const queueDebounceDelay = 50 * time.Millisecond

// queueSync mirrors mpv's playlist and keeps it consistent while index-based
// edits race with delayed playlist events.
type queueSync struct {
	player Player

	entries []mpv.PlaylistEntry
	pos     int
	// synced lets the root reconcile cursor, thumbnail and MPRIS only when mpv replaced the mirror.
	synced uint64

	projection *queueProjection
	// Once an edit has raced with playlist events, read mpv's current queue
	// after later events instead of trusting payloads that may have been delayed.
	authoritative   bool
	editsPending    int
	insertPending   bool
	refreshPending  bool
	revision        uint64
	eventVersion    uint64
	debouncePending bool
	tail            <-chan struct{}
}

type queueProjection struct{}

type (
	queueActionDoneMsg struct {
		requestID uint64
		status    string
		err       error
		projected bool
	}
	queueRefreshMsg struct {
		revision     uint64
		eventVersion uint64
		entries      []mpv.PlaylistEntry
		pos          int
		err          error
	}
	queueDebounceMsg struct{ version uint64 }
)

func newQueueSync(p Player) queueSync {
	return queueSync{player: p, pos: -1}
}

func (q queueSync) update(msg tea.Msg) (queueSync, tea.Cmd) {
	switch msg := msg.(type) {
	case queueDoneMsg:
		return q, q.scheduleRefresh()

	case queueActionDoneMsg:
		if msg.projected {
			q.editsPending--
		}
		return q, q.scheduleRefresh()

	case queueRefreshMsg:
		q.refreshPending = false
		if msg.revision != q.revision || q.editsPending > 0 {
			return q, q.scheduleRefresh()
		}
		if msg.err != nil {
			err := fmt.Errorf("refresh queue: %w", msg.err)
			return q, func() tea.Msg { return errMsg{err} }
		}
		q.projection = nil
		q.authoritative = true
		q.insertPending = false
		q.entries, q.pos = msg.entries, msg.pos
		q.synced++
		if q.eventVersion != msg.eventVersion {
			return q, q.scheduleDebounce()
		}
		return q, nil

	case queueDebounceMsg:
		if msg.version != q.eventVersion {
			return q, queueDebounce(q.eventVersion)
		}
		q.debouncePending = false
		return q, q.scheduleRefresh()

	case mpvEventMsg:
		return q, q.applyProperty(mpv.Event(msg))
	}
	return q, nil
}

func (q *queueSync) applyProperty(ev mpv.Event) tea.Cmd {
	switch ev.Prop {
	case mpv.PropPlaylist:
		if q.projection != nil || q.authoritative {
			// Event delivery can lag behind queued commands, and an older
			// playlist can equal the final projection after opposing edits.
			q.eventVersion++
			return q.scheduleDebounce()
		}
		q.entries = mpv.Decode[[]mpv.PlaylistEntry](ev.Data)
		q.synced++
	case mpv.PropPlaylistPos:
		// null (unavailable) would decode to 0 and wrongly mark the first entry as playing.
		q.pos = -1
		if string(ev.Data) != "null" {
			q.pos = mpv.Decode[int](ev.Data)
		}
	}
	return nil
}

func (q *queueSync) reserve() queueTask {
	task := queueTask{previous: q.tail, done: make(chan struct{})}
	q.tail = task.done
	return task
}

func (q *queueSync) expect() {
	q.projection = &queueProjection{}
	q.editsPending++
	q.revision++
}

func (q *queueSync) write(requestID uint64, fn func(context.Context) error) tea.Cmd {
	return projectedQueueAction(q.reserve(), requestID, "queue updated", fn)
}

// The insert shifts playlist indices, so index-based edits stay paused until
// it has been read back from mpv.
func (q *queueSync) playNow(requestID uint64, t youtube.Track) tea.Cmd {
	p := q.player
	task := q.reserve()
	q.expect()
	q.insertPending = true
	return projectedQueueAction(task, requestID, "playing "+quote(t.Title), func(ctx context.Context) error { return p.PlayNow(ctx, t.URL) })
}

func (q *queueSync) enqueue(requestID uint64, t youtube.Track) tea.Cmd {
	p := q.player
	return queueAction(q.reserve(), requestID, "queued "+quote(t.Title), func(ctx context.Context) error { return p.Append(ctx, t.URL) })
}

func (q *queueSync) playIndex(requestID uint64, i int) tea.Cmd {
	p := q.player
	return queueAction(q.reserve(), requestID, "playing selected track", func(ctx context.Context) error { return p.PlayIndex(ctx, i) })
}

func (q *queueSync) remove(requestID uint64, i int) tea.Cmd {
	p := q.player
	cmd := q.write(requestID, func(ctx context.Context) error { return p.Remove(ctx, i) })
	q.entries = slices.Delete(slices.Clone(q.entries), i, i+1)
	q.expect()
	return cmd
}

func (q *queueSync) move(requestID uint64, from, to int) tea.Cmd {
	p := q.player
	cmd := q.write(requestID, func(ctx context.Context) error { return p.Move(ctx, from, to) })
	q.entries = slices.Clone(q.entries)
	q.entries[from], q.entries[to] = q.entries[to], q.entries[from]
	q.expect()
	return cmd
}

// reorder projects order[i] as the entry now at i; entries before the first
// displaced one, including the current track, keep their slots.
func (q *queueSync) reorder(requestID uint64, pos int, current string, order []int) tea.Cmd {
	p := q.player
	cmd := projectedQueueAction(q.reserve(), requestID, "queue shuffled", func(ctx context.Context) error { return p.Reorder(ctx, pos, current, order) })
	entries := make([]mpv.PlaylistEntry, len(order))
	for i, from := range order {
		entries[i] = q.entries[from]
	}
	q.entries = entries
	q.expect()
	return cmd
}

// replace swaps the whole queue for tracks and plays tracks[start]. Like
// playNow, it pauses index-based edits until mpv's queue is read back.
func (q *queueSync) replace(requestID uint64, tracks []youtube.Track, start int) tea.Cmd {
	p := q.player
	urls := make([]string, len(tracks))
	entries := make([]mpv.PlaylistEntry, len(tracks))
	for i, t := range tracks {
		urls[i] = t.URL
		entries[i] = mpv.PlaylistEntry{Filename: t.URL, Title: t.Title}
	}
	task := q.reserve()
	status := "playing " + pluralize(len(tracks), "track")
	cmd := func() tea.Msg {
		err := task.run(func() error {
			ctx, cancel := context.WithTimeout(context.Background(), importTimeout)
			defer cancel()
			return p.PlayAll(ctx, urls, start)
		})
		return queueActionDoneMsg{requestID: requestID, status: status, err: err, projected: true}
	}
	q.entries = entries
	q.pos = -1
	q.expect()
	q.insertPending = true
	return cmd
}

// clear follows any in-flight edit, even on an empty mirror; the authoritative
// refresh settles its result.
func (q *queueSync) clear(requestID uint64) tea.Cmd {
	p := q.player
	cmd := projectedQueueAction(q.reserve(), requestID, "queue cleared", func(ctx context.Context) error { return p.Stop(ctx) })
	q.entries = nil
	q.pos = -1
	q.expect()
	return cmd
}

func (q *queueSync) scheduleRefresh() tea.Cmd {
	if (q.projection == nil && !q.authoritative) || q.editsPending > 0 || q.refreshPending {
		return nil
	}
	q.refreshPending = true
	return refreshQueue(q.reserve(), q.player, q.revision, q.eventVersion)
}

func (q *queueSync) scheduleDebounce() tea.Cmd {
	if q.debouncePending {
		return nil
	}
	q.debouncePending = true
	return queueDebounce(q.eventVersion)
}

// queueTask links writes in the order their keys were pressed, regardless of
// how long their network lookups or Bubble Tea command scheduling take.
type queueTask struct {
	previous <-chan struct{}
	done     chan struct{}
}

func (task queueTask) run(fn func() error) error {
	if task.previous != nil {
		<-task.previous
	}
	defer close(task.done)
	return fn()
}

func queueAction(task queueTask, requestID uint64, status string, fn func(context.Context) error) tea.Cmd {
	return func() tea.Msg {
		err := task.run(func() error {
			ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
			defer cancel()
			return fn(ctx)
		})
		return queueActionDoneMsg{requestID: requestID, status: status, err: err}
	}
}

func projectedQueueAction(task queueTask, requestID uint64, status string, fn func(context.Context) error) tea.Cmd {
	cmd := queueAction(task, requestID, status, fn)
	return func() tea.Msg {
		msg := cmd().(queueActionDoneMsg)
		msg.projected = true
		return msg
	}
}

func queueDebounce(version uint64) tea.Cmd {
	return tea.Tick(queueDebounceDelay, func(time.Time) tea.Msg { return queueDebounceMsg{version: version} })
}

func refreshQueue(task queueTask, p Player, revision, eventVersion uint64) tea.Cmd {
	return func() tea.Msg {
		var entries []mpv.PlaylistEntry
		pos := -1
		err := task.run(func() error {
			ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
			defer cancel()
			var err error
			entries, pos, err = p.Playlist(ctx)
			return err
		})
		return queueRefreshMsg{revision: revision, eventVersion: eventVersion, entries: entries, pos: pos, err: err}
	}
}
