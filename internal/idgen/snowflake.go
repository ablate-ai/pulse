package idgen

import (
	"strconv"
	"sync"
	"time"
)

const (
	customEpoch int64 = 1735689600000 // 2025-01-01T00:00:00Z
	nodeBits          = 10
	seqBits           = 12
	maxNodeID   int64 = (1 << nodeBits) - 1
	maxSeq      int64 = (1 << seqBits) - 1
	timeShift         = nodeBits + seqBits
	nodeShift         = seqBits
)

type Generator struct {
	mu        sync.Mutex
	lastMs    int64
	sequence  int64
	machineID int64
}

func New(machineID int64) *Generator {
	return &Generator{
		machineID: machineID & maxNodeID,
	}
}

func (g *Generator) NextString() string {
	return strconv.FormatInt(g.Next(), 10)
}

func (g *Generator) Next() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := nowMillis()
	if now < g.lastMs {
		now = g.lastMs
	}
	if now == g.lastMs {
		g.sequence = (g.sequence + 1) & maxSeq
		if g.sequence == 0 {
			for now <= g.lastMs {
				now = nowMillis()
			}
		}
	} else {
		g.sequence = 0
	}

	g.lastMs = now
	return ((now - customEpoch) << timeShift) | (g.machineID << nodeShift) | g.sequence
}

func nowMillis() int64 {
	return time.Now().UTC().UnixMilli()
}

var defaultGenerator = New(1)

func NextString() string {
	return defaultGenerator.NextString()
}
