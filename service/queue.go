package service

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/omegaatt36/ytea/domain"
)

const queueDebounceDelay = 50 * time.Millisecond

// Queue mirrors mpv's playlist and keeps it consistent while index-based
// edits race with delayed playlist events.
type Queue struct {
	player Player

	Entries []domain.PlaylistEntry
	Pos     int
	// Synced counts replacements of the mirror by mpv, so a front end can
	// reconcile its cursor, thumbnail and media session only when it changed.
	Synced uint64
	// InsertPending holds back index-based edits: an insert shifts playlist
	// indices until it has been read back from mpv.
	InsertPending bool

	projected bool
	// Once an edit has raced with playlist events, read mpv's current queue
	// after later events instead of trusting payloads that may have been delayed.
	authoritative   bool
	editsPending    int
	refreshPending  bool
	revision        uint64
	eventVersion    uint64
	debouncePending bool
	tail            <-chan struct{}
}

type Action uint8

const (
	ActionPlayNow Action = iota
	ActionEnqueue
	ActionPlayIndex
	ActionRemove
	ActionMove
	ActionReorder
	ActionReplace
	ActionClear
)

type (
	// Title names the track of ActionPlayNow and ActionEnqueue; Tracks counts those of ActionReplace.
	ActionDone struct {
		RequestID uint64
		Action    Action
		Title     string
		Tracks    int
		Err       error
		projected bool
	}
	Refreshed struct {
		revision     uint64
		eventVersion uint64
		entries      []domain.PlaylistEntry
		pos          int
		err          error
	}
	Debounced struct{ version uint64 }
)

func (ActionDone) serviceMsg() {}
func (Refreshed) serviceMsg()  {}
func (Debounced) serviceMsg()  {}

func NewQueue(p Player) Queue {
	return Queue{player: p, Pos: -1}
}

// Indices into the mirror, including Pos, are only trustworthy once Projected is false.
func (q Queue) Projected() bool { return q.projected }

func (q *Queue) Update(msg Msg) Cmd {
	switch msg := msg.(type) {
	case ActionDone:
		if msg.projected {
			q.editsPending--
		}
		return q.scheduleRefresh()

	case Refreshed:
		q.refreshPending = false
		if msg.revision != q.revision || q.editsPending > 0 {
			return q.scheduleRefresh()
		}
		if msg.err != nil {
			err := fmt.Errorf("refresh queue: %w", msg.err)
			return func() Msg { return Failed{err} }
		}
		q.projected = false
		q.authoritative = true
		q.InsertPending = false
		q.Entries, q.Pos = msg.entries, msg.pos
		q.Synced++
		if q.eventVersion != msg.eventVersion {
			return q.scheduleDebounce()
		}
		return nil

	case Debounced:
		if msg.version != q.eventVersion {
			return queueDebounce(q.eventVersion)
		}
		q.debouncePending = false
		return q.scheduleRefresh()
	}
	return nil
}

// Reconcile picks up entries added to mpv outside the queue's own edits, such as an import resolving.
func (q *Queue) Reconcile() Cmd {
	return q.scheduleRefresh()
}

func (q *Queue) ApplyEvent(ev PlayerEvent) Cmd {
	switch ev := ev.(type) {
	case QueueChanged:
		if q.projected || q.authoritative {
			// Event delivery can lag behind queued commands, and an older
			// playlist can equal the final projection after opposing edits.
			q.eventVersion++
			return q.scheduleDebounce()
		}
		q.Entries = ev.Entries
		q.Synced++
	case QueuePosChanged:
		q.Pos = ev.Pos
	}
	return nil
}

// Work that appends to mpv must run inside the reserved slot, so writes land in the
// order their keys were pressed whatever their lookups cost.
func (q *Queue) Reserve() Task {
	task := Task{previous: q.tail, done: make(chan struct{})}
	q.tail = task.done
	return task
}

func (q *Queue) expect() {
	q.projected = true
	q.editsPending++
	q.revision++
}

func (q *Queue) write(requestID uint64, action Action, fn func(context.Context) error) Cmd {
	return projectedAction(q.Reserve(), ActionDone{RequestID: requestID, Action: action}, fn)
}

func (q *Queue) PlayNow(requestID uint64, t domain.Track) Cmd {
	p := q.player
	task := q.Reserve()
	q.expect()
	q.InsertPending = true
	return projectedAction(task, ActionDone{RequestID: requestID, Action: ActionPlayNow, Title: t.Title},
		func(ctx context.Context) error { return p.PlayNow(ctx, t.URL) })
}

func (q *Queue) Enqueue(requestID uint64, t domain.Track) Cmd {
	p := q.player
	return action(q.Reserve(), ActionDone{RequestID: requestID, Action: ActionEnqueue, Title: t.Title},
		func(ctx context.Context) error { return p.Append(ctx, t.URL) })
}

func (q *Queue) PlayIndex(requestID uint64, i int) Cmd {
	p := q.player
	return action(q.Reserve(), ActionDone{RequestID: requestID, Action: ActionPlayIndex},
		func(ctx context.Context) error { return p.PlayIndex(ctx, i) })
}

func (q *Queue) Remove(requestID uint64, i int) Cmd {
	p := q.player
	cmd := q.write(requestID, ActionRemove, func(ctx context.Context) error { return p.Remove(ctx, i) })
	q.Entries = slices.Delete(slices.Clone(q.Entries), i, i+1)
	q.expect()
	return cmd
}

// Move swaps the neighbours from and to.
func (q *Queue) Move(requestID uint64, from, to int) Cmd {
	p := q.player
	cmd := q.write(requestID, ActionMove, func(ctx context.Context) error { return p.Move(ctx, from, to) })
	q.Entries = slices.Clone(q.Entries)
	q.Entries[from], q.Entries[to] = q.Entries[to], q.Entries[from]
	q.expect()
	return cmd
}

func (q *Queue) Reorder(requestID uint64, pos int, current string, order []int) Cmd {
	p := q.player
	cmd := q.write(requestID, ActionReorder, func(ctx context.Context) error { return p.Reorder(ctx, pos, current, order) })
	entries := make([]domain.PlaylistEntry, len(order))
	for i, from := range order {
		entries[i] = q.Entries[from]
	}
	q.Entries = entries
	q.expect()
	return cmd
}

func (q *Queue) Replace(requestID uint64, tracks []domain.Track, start int) Cmd {
	p := q.player
	urls := make([]string, len(tracks))
	entries := make([]domain.PlaylistEntry, len(tracks))
	for i, t := range tracks {
		urls[i] = t.URL
		entries[i] = domain.PlaylistEntry{Filename: t.URL, Title: t.Title}
	}
	task := q.Reserve()
	done := ActionDone{RequestID: requestID, Action: ActionReplace, Tracks: len(tracks), projected: true}
	cmd := func() Msg {
		done.Err = task.Run(func() error {
			ctx, cancel := context.WithTimeout(context.Background(), playAllTimeout)
			defer cancel()
			return p.PlayAll(ctx, urls, start)
		})
		return done
	}
	q.Entries = entries
	q.Pos = -1
	q.expect()
	q.InsertPending = true
	return cmd
}

// Clear follows any in-flight edit, even on an empty mirror; the authoritative
// refresh settles its result.
func (q *Queue) Clear(requestID uint64) Cmd {
	p := q.player
	cmd := q.write(requestID, ActionClear, func(ctx context.Context) error { return p.Stop(ctx) })
	q.Entries = nil
	q.Pos = -1
	q.expect()
	return cmd
}

func (q *Queue) scheduleRefresh() Cmd {
	if (!q.projected && !q.authoritative) || q.editsPending > 0 || q.refreshPending {
		return nil
	}
	q.refreshPending = true
	return refresh(q.Reserve(), q.player, q.revision, q.eventVersion)
}

func (q *Queue) scheduleDebounce() Cmd {
	if q.debouncePending {
		return nil
	}
	q.debouncePending = true
	return queueDebounce(q.eventVersion)
}

type Task struct {
	previous <-chan struct{}
	done     chan struct{}
}

func (task Task) Run(fn func() error) error {
	if task.previous != nil {
		<-task.previous
	}
	defer close(task.done)
	return fn()
}

func action(task Task, done ActionDone, fn func(context.Context) error) Cmd {
	return func() Msg {
		done.Err = task.Run(func() error {
			ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
			defer cancel()
			return fn(ctx)
		})
		return done
	}
}

func projectedAction(task Task, done ActionDone, fn func(context.Context) error) Cmd {
	done.projected = true
	return action(task, done, fn)
}

func queueDebounce(version uint64) Cmd {
	return func() Msg {
		time.Sleep(queueDebounceDelay)
		return Debounced{version: version}
	}
}

func refresh(task Task, p Player, revision, eventVersion uint64) Cmd {
	return func() Msg {
		var entries []domain.PlaylistEntry
		pos := -1
		err := task.Run(func() error {
			ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
			defer cancel()
			var err error
			entries, pos, err = p.Playlist(ctx)
			return err
		})
		return Refreshed{revision: revision, eventVersion: eventVersion, entries: entries, pos: pos, err: err}
	}
}
