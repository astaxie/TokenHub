package server

import (
	"net/http"
	"strings"
)

type adminSQLiteBackupItemHandler func(http.ResponseWriter, *http.Request, AdminUser, string)

func (s *Server) adminSQLiteBackupMethodNotAllowed(allowedMethods string) http.HandlerFunc {
	reject := jsonMethodNotAllowed(allowedMethods)
	return func(w http.ResponseWriter, r *http.Request) {
		backupID := r.PathValue("backup_id")
		if backupID == "" || strings.Contains(backupID, "/") {
			s.handleAdminSQLiteBackupItem(w, r)
			return
		}
		if _, ok := s.requireAdmin(w, r, "backup", r.Method); !ok {
			return
		}
		reject(w, r)
	}
}

func (s *Server) handleAdminSQLiteBackupsGet(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "backup", r.Method)
	if !ok {
		return
	}
	s.serveAdminSQLiteBackupsGet(w, r, user)
}

func (s *Server) handleAdminSQLiteBackupsPost(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "backup", r.Method)
	if !ok {
		return
	}
	s.serveAdminSQLiteBackupsPost(w, r, user)
}

func (s *Server) handleAdminSQLiteBackupGet(w http.ResponseWriter, r *http.Request) {
	s.handleAdminSQLiteBackupItemRoute(w, r, s.serveAdminSQLiteBackupGet)
}

func (s *Server) handleAdminSQLiteBackupDelete(w http.ResponseWriter, r *http.Request) {
	s.handleAdminSQLiteBackupItemRoute(w, r, s.serveAdminSQLiteBackupDelete)
}

func (s *Server) handleAdminSQLiteBackupDownloadGet(w http.ResponseWriter, r *http.Request) {
	s.handleAdminSQLiteBackupItemRoute(w, r, s.serveAdminSQLiteBackupDownload)
}

func (s *Server) handleAdminSQLiteBackupRestorePost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminSQLiteBackupItemRoute(w, r, s.serveAdminSQLiteBackupRestore)
}

func (s *Server) handleAdminSQLiteBackupItemRoute(w http.ResponseWriter, r *http.Request, handler adminSQLiteBackupItemHandler) {
	backupID := r.PathValue("backup_id")
	if backupID == "" || strings.Contains(backupID, "/") {
		s.handleAdminSQLiteBackupItem(w, r)
		return
	}
	user, ok := s.requireAdmin(w, r, "backup", r.Method)
	if !ok {
		return
	}
	handler(w, r, user, backupID)
}

func (s *Server) handleAdminExportGet(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("dataset")
	if kind == "" || strings.Contains(kind, "/") {
		s.handleAdminExport(w, r)
		return
	}
	user, ok := s.requireAdmin(w, r, "usage", r.Method)
	if !ok {
		return
	}
	s.serveAdminExport(w, r, user, kind)
}
