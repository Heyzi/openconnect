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
	// Prefixed names too: <secondary_password>, <session-token>.
	regexp.MustCompile(`(?i)(<[\w:-]*(?:password|passwd|token|cookie|authorization|pin|secret)\b[^>]*>)[^<]*(</[\w:-]*(?:password|passwd|token|cookie|authorization|pin|secret)\s*>)`),
	// A header value can hold several secrets ("Cookie: a=1; webvpn=2"): drop all of it.
	regexp.MustCompile(`(?i)(cookie|authorization|x-dtls[\w-]*-(?:master-secret|session-id))(\s*:\s*).*`),
	regexp.MustCompile(`(?i)(password|passwd|token|cookie|authorization|pin|secret)(\s*[:=]\s*)\S+`),
	regexp.MustCompile(`(?i)-----BEGIN [^-]*PRIVATE KEY-----`),
}

func New(limit int) *Buffer { return &Buffer{limit: limit, subscribers: map[chan Entry]struct{}{}} }
func redact(s string) string {
	s = secrets[0].ReplaceAllString(s, "$1[REDACTED]$2")
	for _, re := range secrets[1:] {
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
