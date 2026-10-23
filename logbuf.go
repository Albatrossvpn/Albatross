package main

import (
	"fmt"
	"sync"
	"time"
)

// LogBuffer keeps the most recent log lines for the UI.
type LogBuffer struct {
	mu    sync.Mutex
	lines []string
	max   int
	seq   int
}

func newLogBuffer(max int) *LogBuffer { return &LogBuffer{max: max} }

func (l *LogBuffer) Add(format string, args ...any) {
	line := time.Now().Format("15:04:05") + " " + fmt.Sprintf(format, args...)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, line)
	if len(l.lines) > l.max {
		l.lines = l.lines[len(l.lines)-l.max:]
	}
	l.seq++
}

// Since returns lines added after sequence number seq and the current sequence.
func (l *LogBuffer) Since(seq int) ([]string, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := l.seq - seq
	if n <= 0 {
		return nil, l.seq
	}
	if n > len(l.lines) {
		n = len(l.lines)
	}
	out := make([]string, n)
	copy(out, l.lines[len(l.lines)-n:])
	return out, l.seq
}
