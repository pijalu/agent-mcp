package session

import (
	"errors"
)

// ErrUnknownServer marks Close calls for servers not in the config.
var ErrUnknownServer = errors.New("session: unknown server")

// Close tears down the session for name. Closing an unopened server is a
// no-op; closing one twice is safe; closing an unknown server is an error.
// The entry is left marked closed so later Open calls fail fast rather than
// silently redialing behind the caller's back.
func (m *Manager) Close(name string) error {
	if _, ok := m.configs[name]; !ok {
		return m.unknownServerError(name)
	}
	e := m.slot(name)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.session != nil {
		// ClientSession.Close is idempotent in the SDK; the error is only
		// useful for logging and must not block marking the entry closed.
		_ = e.session.Close()
		e.session = nil
	}
	e.closed = true
	return nil
}

// CloseAll tears down every session and returns the number closed.
func (m *Manager) CloseAll() int {
	m.mu.Lock()
	entries := make([]*entry, 0, len(m.entries))
	for _, e := range m.entries {
		entries = append(entries, e)
	}
	m.mu.Unlock()

	closed := 0
	for _, e := range entries {
		e.mu.Lock()
		if e.session != nil {
			_ = e.session.Close()
			e.session = nil
			closed++
		}
		e.closed = true
		e.mu.Unlock()
	}
	return closed
}
