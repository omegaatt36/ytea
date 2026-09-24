package mpv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"
)

func newFakePair(t *testing.T, handle func(req request) string) *Client {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	go serveFake(serverConn, handle)
	c := newClient(clientConn)
	t.Cleanup(func() {
		_ = c.Close()
		_ = serverConn.Close()
	})
	return c
}

// serveFake answers requests on the far end of a pipe the way mpv does.
// Scan errors are ignored: they only mean the test closed the pipe.
func serveFake(conn net.Conn, handle func(req request) string) {
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			return
		}
		if _, err := conn.Write([]byte(handle(req) + "\n")); err != nil {
			return
		}
	}
}

func TestClientCommand(t *testing.T) {
	tests := []struct {
		name      string
		reply     string
		wantData  string
		wantError bool
	}{
		{name: "success returns data", reply: `"data":"opus","error":"success"`, wantData: `"opus"`},
		{name: "mpv error becomes CommandError", reply: `"error":"property unavailable"`, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newFakePair(t, func(req request) string {
				// An unrelated event before the reply must not be mistaken for it.
				b, _ := json.Marshal(req.RequestID)
				return `{"event":"idle"}` + "\n" + `{"request_id":` + string(b) + `,` + tt.reply + `}`
			})

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			data, err := c.Command(ctx, "get_property", "audio-codec-name")

			if tt.wantError {
				if _, ok := errors.AsType[*CommandError](err); !ok {
					t.Fatalf("Command() error = %v, want *CommandError", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Command() error = %v", err)
			}
			if string(data) != tt.wantData {
				t.Errorf("Command() data = %q, want %q", data, tt.wantData)
			}
		})
	}
}

func TestClientDeliversEvents(t *testing.T) {
	c := newFakePair(t, func(req request) string {
		b, _ := json.Marshal(req.RequestID)
		return `{"event":"property-change","id":3,"name":"time-pos","data":12.5}` + "\n" +
			`{"request_id":` + string(b) + `,"error":"success"}`
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := c.Command(ctx, "observe_property", 3, "time-pos"); err != nil {
		t.Fatalf("Command() error = %v", err)
	}

	select {
	case ev := <-c.Events():
		if ev.Name != "property-change" || ev.Prop != PropTimePos || ev.ID != 3 {
			t.Fatalf("event = %+v, want property-change time-pos id 3", ev)
		}
		if got := Decode[float64](ev.Data); got != 12.5 {
			t.Errorf("Decode(time-pos) = %v, want 12.5", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no event received")
	}
}

func TestClientCommandAfterClose(t *testing.T) {
	c := newFakePair(t, func(request) string { return "" })
	_ = c.Close()
	<-c.Done()

	if _, err := c.Command(context.Background(), "quit"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Command() error = %v, want %v", err, ErrClosed)
	}
}

func TestDecodeNullIsZero(t *testing.T) {
	if got := Decode[float64](json.RawMessage("null")); got != 0 {
		t.Errorf("Decode(null) = %v, want 0", got)
	}
	if got := Decode[[]PlaylistEntry](nil); got != nil {
		t.Errorf("Decode(nil) = %v, want nil", got)
	}
}
