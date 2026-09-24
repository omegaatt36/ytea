// Package mpv drives an mpv process through its JSON IPC socket.
package mpv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
)

// ErrClosed is returned for commands issued after the connection is gone.
var ErrClosed = errors.New("mpv connection closed")

// Event is an asynchronous message from mpv, such as property-change or end-file.
type Event struct {
	Name string
	// ID is the observer id for property-change events.
	ID   int
	Prop string
	Data json.RawMessage
	// Reason and FileError are only set on end-file.
	Reason    string
	FileError string
}

type response struct {
	Error string
	Data  json.RawMessage
}

// message is the union of replies and events; embedding both would make
// their shared "data" key ambiguous and encoding/json would drop it.
type message struct {
	RequestID int64           `json:"request_id"`
	Error     string          `json:"error"`
	Data      json.RawMessage `json:"data"`
	Event     string          `json:"event"`
	ID        int             `json:"id"`
	Prop      string          `json:"name"`
	Reason    string          `json:"reason"`
	FileError string          `json:"file_error"`
}

type request struct {
	Command   []any `json:"command"`
	RequestID int64 `json:"request_id"`
}

// Client is a goroutine-safe mpv IPC connection.
type Client struct {
	conn   net.Conn
	events chan Event

	writeMu sync.Mutex

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan response
	closed  bool
	done    chan struct{}
}

// Dial connects to an mpv IPC socket.
func Dial(ctx context.Context, socket string) (*Client, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, fmt.Errorf("dial mpv socket %s: %w", socket, err)
	}
	return newClient(conn), nil
}

func newClient(conn net.Conn) *Client {
	c := &Client{
		conn: conn,
		// Buffered so a slow UI render does not stall mpv's socket reader;
		// time-pos alone fires several times a second.
		events:  make(chan Event, 256),
		pending: make(map[int64]chan response),
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c
}

// Events delivers asynchronous mpv events. It is closed when the connection ends.
func (c *Client) Events() <-chan Event {
	return c.events
}

// Done is closed when the connection ends.
func (c *Client) Done() <-chan struct{} {
	return c.done
}

// Command sends a command and waits for its reply data.
func (c *Client) Command(ctx context.Context, args ...any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	c.nextID++
	id := c.nextID
	reply := make(chan response, 1)
	c.pending[id] = reply
	c.mu.Unlock()

	payload, err := json.Marshal(request{Command: args, RequestID: id})
	if err != nil {
		c.forget(id)
		return nil, fmt.Errorf("encode mpv command %v: %w", args, err)
	}
	payload = append(payload, '\n')

	c.writeMu.Lock()
	_, err = c.conn.Write(payload)
	c.writeMu.Unlock()
	if err != nil {
		c.forget(id)
		return nil, fmt.Errorf("write mpv command %v: %w", args, err)
	}

	select {
	case res, ok := <-reply:
		if !ok {
			return nil, ErrClosed
		}
		if res.Error != "success" {
			return nil, &CommandError{Command: args, Reason: res.Error}
		}
		return res.Data, nil
	case <-ctx.Done():
		c.forget(id)
		return nil, ctx.Err()
	}
}

// CommandError is mpv's rejection of a command, e.g. "property unavailable".
type CommandError struct {
	Command []any
	Reason  string
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("mpv command %v: %s", e.Command, e.Reason)
}

// Close terminates the connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) readLoop() {
	defer c.shutdown()

	scanner := bufio.NewScanner(c.conn)
	// playlist and track-list replies can be large.
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var msg message
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		if msg.Event != "" {
			c.events <- Event{
				Name: msg.Event, ID: msg.ID, Prop: msg.Prop, Data: msg.Data,
				Reason: msg.Reason, FileError: msg.FileError,
			}
			continue
		}
		c.mu.Lock()
		reply, ok := c.pending[msg.RequestID]
		delete(c.pending, msg.RequestID)
		c.mu.Unlock()
		if ok {
			reply <- response{Error: msg.Error, Data: msg.Data}
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.EOF) {
		c.events <- Event{Name: "ipc-error", Reason: err.Error()}
	}
}

func (c *Client) shutdown() {
	c.mu.Lock()
	c.closed = true
	for id, reply := range c.pending {
		close(reply)
		delete(c.pending, id)
	}
	c.mu.Unlock()
	close(c.events)
	close(c.done)
}
