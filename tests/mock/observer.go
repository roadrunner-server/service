package mocklogger

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
)

// LoggedEntry is a representation of a log record captured by the observer.
type LoggedEntry struct {
	Level   slog.Level
	Message string
	Attrs   map[string]any
}

// All returns the captured records. Attribute maps are immutable.
func (o *ObservedLogs) All() []LoggedEntry {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return slices.Clone(o.logs)
}

// ObservedLogs is a concurrency-safe, ordered collection of observed logs.
type ObservedLogs struct {
	mu   sync.RWMutex
	logs []LoggedEntry
}

// Len returns the number of items in the collection.
func (o *ObservedLogs) Len() int {
	o.mu.RLock()
	n := len(o.logs)
	o.mu.RUnlock()
	return n
}

// FilterMessage returns the entries whose message is exactly msg. Short messages
// such as "wait" need this instead of a snippet match.
func (o *ObservedLogs) FilterMessage(msg string) *ObservedLogs {
	o.mu.RLock()
	defer o.mu.RUnlock()

	var filtered []LoggedEntry
	for _, entry := range o.logs {
		if entry.Message == msg {
			filtered = append(filtered, entry)
		}
	}

	return &ObservedLogs{logs: filtered}
}

// FilterMessageSnippet returns the entries whose message contains the snippet.
func (o *ObservedLogs) FilterMessageSnippet(snippet string) *ObservedLogs {
	o.mu.RLock()
	defer o.mu.RUnlock()

	var filtered []LoggedEntry
	for _, entry := range o.logs {
		if strings.Contains(entry.Message, snippet) {
			filtered = append(filtered, entry)
		}
	}

	return &ObservedLogs{logs: filtered}
}

func (o *ObservedLogs) add(entry LoggedEntry) {
	o.mu.Lock()
	o.logs = append(o.logs, entry)
	o.mu.Unlock()
}

// observerHandler captures records and their attributes.
type observerHandler struct {
	level slog.Level
	logs  *ObservedLogs
	attrs map[string]any
}

// NewObserverHandler creates a new slog.Handler that buffers logs in memory.
func NewObserverHandler(level slog.Level) (slog.Handler, *ObservedLogs) {
	ol := &ObservedLogs{}
	return &observerHandler{
		level: level,
		logs:  ol,
		attrs: make(map[string]any),
	}, ol
}

func (h *observerHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *observerHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := maps.Clone(h.attrs)
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	h.logs.add(LoggedEntry{
		Level:   r.Level,
		Message: r.Message,
		Attrs:   attrs,
	})
	return nil
}

func (h *observerHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := *h
	next.attrs = maps.Clone(h.attrs)
	for _, a := range attrs {
		next.attrs[a.Key] = a.Value.Any()
	}
	return &next
}

func (h *observerHandler) WithGroup(_ string) slog.Handler {
	return h
}
