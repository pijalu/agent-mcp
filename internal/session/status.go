package session

import "sort"

// ServerInfo reports one configured server's session state to operators.
// State is "idle" when no session has been dialed, "connected" when one
// is live, and "stopped" once the entry has been closed.
type ServerInfo struct {
	Name           string `json:"name"`
	State          string `json:"state"`
	ActiveSessions int    `json:"active_sessions"`
	LastError      string `json:"last_error,omitempty"`
}

// ServerStatuses snapshots every configured server in name order.
func (m *Manager) ServerStatuses() []ServerInfo {
	names := make([]string, 0, len(m.configs))
	for name := range m.configs {
		names = append(names, name)
	}
	sort.Strings(names)

	infos := make([]ServerInfo, 0, len(names))
	for _, name := range names {
		m.mu.Lock()
		e, ok := m.entries[name]
		m.mu.Unlock()

		info := ServerInfo{Name: name, State: "idle"}
		if ok {
			e.mu.Lock()
			switch {
			case e.closed:
				info.State = "stopped"
			case e.session != nil:
				info.State = "connected"
				info.ActiveSessions = 1
			}
			info.LastError = e.lastError
			e.mu.Unlock()
		}
		infos = append(infos, info)
	}
	return infos
}
