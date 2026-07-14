package logging

import (
	"regexp"
	"strings"
	"sync"
	"time"
)

type Entry struct {
	Time      time.Time `json:"time"`
	Level     string    `json:"level"`
	Component string    `json:"component"`
	Message   string    `json:"message"`
}
type Buffer struct {
	mu          sync.RWMutex
	entries     []Entry
	subscribers map[chan Entry]struct{}
	limit       int
}

var secrets = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(password|passwd|token|cookie|authorization|pin)(\s*[:=]\s*)\S+`),
	regexp.MustCompile(`(?i)-----BEGIN [^-]*PRIVATE KEY-----`),
}

func New(limit int) *Buffer { return &Buffer{limit: limit, subscribers: map[chan Entry]struct{}{}} }
func redact(s string) string {
	for _, re := range secrets {
		s = re.ReplaceAllString(s, "$1$2[REDACTED]")
	}
	return strings.TrimSpace(s)
}
func (b *Buffer) Add(level, component, message string) {
	e := Entry{Time: time.Now().UTC(), Level: level, Component: component, Message: redact(message)}
	b.mu.Lock()
	b.entries = append(b.entries, e)
	if len(b.entries) > b.limit {
		b.entries = b.entries[len(b.entries)-b.limit:]
	}
	for ch := range b.subscribers {
		select {
		case ch <- e:
		default:
		}
	}
	b.mu.Unlock()
}
func (b *Buffer) Entries() []Entry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return append([]Entry{}, b.entries...)
}
func (b *Buffer) Subscribe() (<-chan Entry, func()) {
	ch := make(chan Entry, 32)
	b.mu.Lock()
	b.subscribers[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() { b.mu.Lock(); delete(b.subscribers, ch); close(ch); b.mu.Unlock() }
}
