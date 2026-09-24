// Package pipewire inspects the PipeWire graph and taps audio streams.
package pipewire

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
)

// ErrNodeNotFound is returned when no node matches the requested name.
var ErrNodeNotFound = errors.New("pipewire node not found")

// Sink is an audio output device.
type Sink struct {
	ID          int
	Name        string
	Description string
	Default     bool
}

// Label is a human-readable sink name.
func (s Sink) Label() string {
	if s.Description != "" {
		return s.Description
	}
	return s.Name
}

type object struct {
	ID       int             `json:"id"`
	Type     string          `json:"type"`
	Info     *nodeInfo       `json:"info"`
	Props    map[string]any  `json:"props"`
	Metadata []metadataEntry `json:"metadata"`
}

type nodeInfo struct {
	Props map[string]any `json:"props"`
}

type metadataEntry struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

const (
	typeNode     = "PipeWire:Interface:Node"
	typeMetadata = "PipeWire:Interface:Metadata"
)

// Graph is a snapshot of the PipeWire object graph taken with pw-dump.
type Graph struct {
	objects []object
}

// Dump snapshots the PipeWire graph.
func Dump(ctx context.Context) (*Graph, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "pw-dump")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run pw-dump: %w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return ParseGraph(stdout.Bytes())
}

// ParseGraph decodes pw-dump output.
func ParseGraph(data []byte) (*Graph, error) {
	var objects []object
	if err := json.Unmarshal(data, &objects); err != nil {
		return nil, fmt.Errorf("decode pw-dump output: %w", err)
	}
	return &Graph{objects: objects}, nil
}

// Sinks lists audio output devices, default first.
func (g *Graph) Sinks() []Sink {
	defaultName := g.defaultSink()

	var sinks []Sink
	for _, o := range g.objects {
		if o.Type != typeNode || o.Info == nil {
			continue
		}
		props := o.Info.Props
		if propString(props, "media.class") != "Audio/Sink" {
			continue
		}
		name := propString(props, "node.name")
		sinks = append(sinks, Sink{
			ID:          o.ID,
			Name:        name,
			Description: propString(props, "node.description"),
			Default:     name == defaultName,
		})
	}
	slices.SortStableFunc(sinks, func(a, b Sink) int {
		if a.Default != b.Default {
			if a.Default {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Label(), b.Label())
	})
	return sinks
}

// StreamSerial returns the object.serial of the output stream whose node.name is name.
// pw-cat --target takes serials, and a stream's serial changes whenever mpv
// reopens its audio output (device switch, format change).
func (g *Graph) StreamSerial(name string) (int, error) {
	for _, o := range g.objects {
		if o.Type != typeNode || o.Info == nil {
			continue
		}
		props := o.Info.Props
		if propString(props, "media.class") != "Stream/Output/Audio" || propString(props, "node.name") != name {
			continue
		}
		if serial, ok := props["object.serial"].(float64); ok {
			return int(serial), nil
		}
	}
	return 0, fmt.Errorf("find stream %q: %w", name, ErrNodeNotFound)
}

func (g *Graph) defaultSink() string {
	for _, o := range g.objects {
		if o.Type != typeMetadata || propString(o.Props, "metadata.name") != "default" {
			continue
		}
		for _, m := range o.Metadata {
			if m.Key != "default.audio.sink" {
				continue
			}
			var v struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(m.Value, &v); err == nil {
				return v.Name
			}
		}
	}
	return ""
}

func propString(props map[string]any, key string) string {
	s, _ := props[key].(string)
	return s
}
