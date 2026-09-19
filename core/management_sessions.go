package core

import (
	"sort"
	"strings"
)

// managementSessionManager pairs a SessionManager with the workspace it serves.
// An empty workspace means the engine's base (single-workspace) manager.
type managementSessionManager struct {
	manager   *SessionManager
	workspace string
}

// managementSession pairs a session with the manager and workspace that own it.
type managementSession struct {
	session   *Session
	manager   *SessionManager
	workspace string
}

// managementSessionID is the session id exposed to the management API.
//
// Session ids are assigned per SessionManager and therefore collide across
// workspaces (each manager starts at "s1"). In multi-workspace mode the
// workspace path is prefixed so ids stay unique and can be resolved back to
// the owning manager via findManagementSession.
const managementSessionWorkspaceSep = "::"

func managementSessionID(session *Session, workspace string) string {
	if workspace == "" {
		return session.ID
	}
	return workspace + managementSessionWorkspaceSep + session.ID
}

// managementSessionManagers returns the engine's base session manager plus one
// entry per live multi-workspace manager. The base manager is always first and
// workspaces are sorted by path so the management API output is stable.
func (e *Engine) managementSessionManagers() []managementSessionManager {
	managers := []managementSessionManager{{manager: e.sessions}}
	if !e.multiWorkspace || e.workspacePool == nil {
		return managers
	}

	all := e.workspacePool.All()
	paths := make([]string, 0, len(all))
	for path := range all {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	seen := map[*SessionManager]bool{e.sessions: true}
	for _, path := range paths {
		ws := all[path]
		if ws == nil || ws.sessions == nil || seen[ws.sessions] {
			continue
		}
		seen[ws.sessions] = true
		managers = append(managers, managementSessionManager{manager: ws.sessions, workspace: path})
	}
	return managers
}

// managementSessionCount returns the number of sessions across the base and all
// live workspace managers. In single-workspace mode this equals
// len(e.sessions.AllSessions()).
func (e *Engine) managementSessionCount() int {
	count := 0
	for _, mm := range e.managementSessionManagers() {
		count += len(mm.manager.AllSessions())
	}
	return count
}

// findManagementSession resolves a management session id to the session and the
// manager that owns it. It accepts both a bare id (single-workspace, or a first
// match while searching all managers) and the workspace-qualified id returned by
// managementSessionID.
func (e *Engine) findManagementSession(id string) (managementSession, bool) {
	if id == "" {
		return managementSession{}, false
	}

	managers := e.managementSessionManagers()

	if idx := strings.LastIndex(id, managementSessionWorkspaceSep); idx >= 0 {
		workspace := id[:idx]
		raw := id[idx+len(managementSessionWorkspaceSep):]
		for _, mm := range managers {
			if mm.workspace == workspace {
				if s := mm.manager.FindByID(raw); s != nil {
					return managementSession{session: s, manager: mm.manager, workspace: workspace}, true
				}
				return managementSession{}, false
			}
		}
		// Unknown workspace prefix: fall back to searching the bare id below.
		id = raw
	}

	for _, mm := range managers {
		if s := mm.manager.FindByID(id); s != nil {
			return managementSession{session: s, manager: mm.manager, workspace: mm.workspace}, true
		}
	}
	return managementSession{}, false
}
