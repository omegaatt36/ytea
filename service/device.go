package service

import (
	"context"
	"slices"

	"github.com/omegaatt36/ytea/domain"
)

type (
	DevicesLoaded struct {
		Devices []domain.AudioDevice
		Err     error
	}
	StreamLoaded struct {
		Info domain.StreamInfo
		Err  error
	}
)

func (DevicesLoaded) serviceMsg() {}
func (StreamLoaded) serviceMsg()  {}

func (c Core) Events() <-chan PlayerEvent { return c.deps.Player.Events() }

func (c Core) LoadDevices() Cmd {
	p := c.deps.Player
	return func() Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		defer cancel()
		devices, err := p.AudioDevices(ctx)
		return DevicesLoaded{Devices: devices, Err: err}
	}
}

func (c Core) LoadStream() Cmd {
	p := c.deps.Player
	return func() Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		defer cancel()
		info, err := p.StreamInfo(ctx)
		return StreamLoaded{Info: info, Err: err}
	}
}

func (c *Core) ApplyDevices(msg DevicesLoaded) {
	if msg.Err == nil {
		c.Devices = msg.Devices
	}
}

func (c *Core) ApplyStream(msg StreamLoaded) {
	if msg.Err == nil {
		c.Stream = msg.Info
	}
}

func (c Core) IsCurrentDevice(d domain.AudioDevice) bool {
	return d.Name == c.Playback.DeviceName()
}

func (c Core) CurrentDevice() (domain.AudioDevice, bool) {
	i := slices.IndexFunc(c.Devices, c.IsCurrentDevice)
	if i < 0 {
		return domain.AudioDevice{}, false
	}
	return c.Devices[i], true
}
