package service

import (
	"context"
	"log/slog"
	"maps"
	"sync"
)

// capturedRecord is a log record with the attributes carried by its handler.
type capturedRecord struct {
	message string
	attrs   map[string]string
}

// captureStore collects the records written through a captureHandler.
type captureStore struct {
	mu      sync.Mutex
	records []capturedRecord
}

func (s *captureStore) add(r capturedRecord) {
	s.mu.Lock()
	s.records = append(s.records, r)
	s.mu.Unlock()
}

// messages returns the messages in the order they were written.
func (s *captureStore) messages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]string, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, r.message)
	}

	return out
}

// count returns how many records carry the given message.
func (s *captureStore) count(message string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	var n int
	for _, r := range s.records {
		if r.message == message {
			n++
		}
	}

	return n
}

// find returns the first record with the given message.
func (s *captureStore) find(message string) (capturedRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, r := range s.records {
		if r.message == message {
			return r, true
		}
	}

	return capturedRecord{}, false
}

// captureHandler is an slog.Handler that keeps every record in memory.
type captureHandler struct {
	store *captureStore
	attrs map[string]string
}

// newCaptureLogger returns a logger writing into the returned store.
func newCaptureLogger() (*slog.Logger, *captureStore) {
	store := &captureStore{}
	return slog.New(&captureHandler{store: store, attrs: map[string]string{}}), store
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool {
	return true
}

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	rec := capturedRecord{message: r.Message, attrs: maps.Clone(h.attrs)}
	r.Attrs(func(a slog.Attr) bool {
		rec.attrs[a.Key] = a.Value.String()
		return true
	})

	h.store.add(rec)
	return nil
}

func (h *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := maps.Clone(h.attrs)
	for _, a := range attrs {
		next[a.Key] = a.Value.String()
	}

	return &captureHandler{store: h.store, attrs: next}
}

func (h *captureHandler) WithGroup(string) slog.Handler {
	return h
}
