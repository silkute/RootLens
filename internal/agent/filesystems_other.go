//go:build !linux

package agent

import (
	"rootlens/internal/protocol"
	"time"
)

type filesystemWorker struct{}

func (c *Collector) filesystems(f *protocol.Frame, entities *[]protocol.Entity, caps map[string]protocol.Capability, now time.Time) {
	caps["filesystem"] = protocol.Capability{Status: "unsupported", Reason: "The agent requires Linux.", At: now}
}
