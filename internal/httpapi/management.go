package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"mijia-archive/internal/auth"
	"mijia-archive/internal/store"
)

type loginAttempt struct {
	Count int
	Since time.Time
}

var loginAttempts = struct {
	sync.Mutex
	items map[string]loginAttempt
}{items: make(map[string]loginAttempt)}

func requestIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func loginAllowed(key string, failed bool) bool {
	loginAttempts.Lock()
	defer loginAttempts.Unlock()
	now := time.Now()
	entry := loginAttempts.items[key]
	if now.Sub(entry.Since) > 5*time.Minute {
		entry = loginAttempt{Since: now}
	}
	if !failed {
		delete(loginAttempts.items, key)
		return true
	}
	entry.Count++
	loginAttempts.items[key] = entry
	return entry.Count <= 5
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		problem(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

func publicUser(user store.UserRecord) map[string]any {
	return map[string]any{"id": user.PublicID, "username": user.Username, "role": user.Role, "active": user.Active, "createdAt": user.CreatedAt}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.Auth == nil || !sameOrigin(r) {
		problem(w, http.StatusForbidden, "invalid_origin")
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	key := requestIP(r) + "\x00" + strings.ToLower(input.Username)
	loginAttempts.Lock()
	entry := loginAttempts.items[key]
	blocked := entry.Count >= 5 && time.Since(entry.Since) <= 5*time.Minute
	loginAttempts.Unlock()
	if blocked {
		problem(w, http.StatusTooManyRequests, "login_rate_limited")
		return
	}
	user, ok := s.Auth.Authenticate(r.Context(), input.Username, input.Password)
	if !ok {
		loginAllowed(key, true)
		problem(w, http.StatusUnauthorized, "invalid_credentials")
		return
	}
	loginAllowed(key, false)
	if err := s.Auth.Sessions.RenewToken(r.Context()); err != nil {
		problem(w, http.StatusInternalServerError, "session_failed")
		return
	}
	s.Auth.Sessions.Put(r.Context(), "user_id", user.ID)
	jsonOut(w, http.StatusOK, map[string]any{"user": publicUser(user)})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, _ := s.Auth.User(r.Context())
	jsonOut(w, http.StatusOK, map[string]any{"user": publicUser(user)})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if err := s.Auth.Sessions.Destroy(r.Context()); err != nil {
		problem(w, http.StatusInternalServerError, "session_failed")
		return
	}
	jsonOut(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var input struct {
		CurrentPassword         string `json:"currentPassword"`
		NewPassword             string `json:"newPassword"`
		NewPasswordConfirmation string `json:"newPasswordConfirmation"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.NewPassword != input.NewPasswordConfirmation {
		problem(w, http.StatusBadRequest, "password_confirmation_mismatch")
		return
	}
	if err := auth.ValidatePassword(input.NewPassword); err != nil {
		problem(w, http.StatusBadRequest, "invalid_password")
		return
	}
	user, _ := s.Auth.User(r.Context())
	key := requestIP(r) + "\x00password\x00" + strings.ToLower(user.Username)
	loginAttempts.Lock()
	entry := loginAttempts.items[key]
	blocked := entry.Count >= 5 && time.Since(entry.Since) <= 5*time.Minute
	loginAttempts.Unlock()
	if blocked {
		problem(w, http.StatusTooManyRequests, "password_change_rate_limited")
		return
	}
	if _, ok := s.Auth.Authenticate(r.Context(), user.Username, input.CurrentPassword); !ok {
		loginAllowed(key, true)
		problem(w, http.StatusUnauthorized, "invalid_current_password")
		return
	}
	hash, err := auth.HashPassword(input.NewPassword)
	if err != nil {
		problem(w, http.StatusInternalServerError, "password_hash_failed")
		return
	}
	if err := s.Store.UpdatePassword(r.Context(), user.Username, hash); err != nil {
		problem(w, http.StatusInternalServerError, "password_update_failed")
		return
	}
	loginAllowed(key, false)
	if err := s.Auth.Sessions.Destroy(r.Context()); err != nil {
		problem(w, http.StatusInternalServerError, "session_failed")
		return
	}
	jsonOut(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) users(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	users, err := s.Store.Users(r.Context())
	if err != nil {
		problem(w, http.StatusInternalServerError, "query_failed")
		return
	}
	result := make([]map[string]any, 0, len(users))
	for _, user := range users {
		result = append(result, publicUser(user))
	}
	jsonOut(w, http.StatusOK, map[string]any{"users": result})
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var input struct {
		Username             string `json:"username"`
		Password             string `json:"password"`
		PasswordConfirmation string `json:"passwordConfirmation"`
		Role                 string `json:"role"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Username = strings.TrimSpace(input.Username)
	if input.Password != input.PasswordConfirmation {
		problem(w, http.StatusBadRequest, "password_confirmation_mismatch")
		return
	}
	if err := auth.ValidateNewUser(input.Username, input.Password, input.Role); err != nil {
		problem(w, http.StatusBadRequest, "invalid_user")
		return
	}
	hash, err := auth.HashPassword(input.Password)
	if err != nil {
		problem(w, http.StatusInternalServerError, "password_hash_failed")
		return
	}
	user, err := s.Store.CreateUser(r.Context(), input.Username, hash, input.Role)
	if err != nil {
		problem(w, http.StatusConflict, "user_exists")
		return
	}
	jsonOut(w, http.StatusCreated, map[string]any{"user": publicUser(user)})
}

type folderView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ServerPath string `json:"serverPath,omitempty"`
	Mounted    bool   `json:"mounted"`
	ScanStatus string `json:"scanStatus"`
	LastScanAt *int64 `json:"lastScanAt"`
	FirstDate  string `json:"firstDate,omitempty"`
	Message    string `json:"message,omitempty"`
}

func (s *Server) folderView(folder store.FolderRecord, admin bool) folderView {
	_, err := s.Library.IsArchiveFolder(folder.ServerPath)
	view := folderView{ID: folder.PublicID, Name: folder.Name, Mounted: err == nil, ScanStatus: folder.ScanStatus, LastScanAt: folder.LastScanAt}
	if admin {
		view.ServerPath = folder.ServerPath
	}
	if first, ok, queryErr := s.Store.FolderFirstRecording(context.Background(), folder.ID); queryErr == nil && ok {
		view.FirstDate = time.UnixMilli(first).In(s.Location).Format("2006-01-02")
	}
	if err != nil {
		view.Message = "目录当前不可用。请确认路径位于已配置的录像库根目录内，且包含 MIJIA_RECORD_VIDEO。"
	} else if folder.ScanStatus == "failed" {
		view.Message = "最近一次索引失败，请检查目录格式后重新扫描。"
	} else if folder.ScanStatus == "pending" || folder.ScanStatus == "scanning" {
		view.Message = "正在建立录像索引。"
	}
	return view
}

func (s *Server) folders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, _ := s.Auth.User(r.Context())
	folders, err := s.Store.Folders(r.Context())
	if err != nil {
		problem(w, http.StatusInternalServerError, "query_failed")
		return
	}
	result := make([]folderView, 0, len(folders))
	for _, folder := range folders {
		result = append(result, s.folderView(folder, user.Role == "admin"))
	}
	jsonOut(w, http.StatusOK, map[string]any{"folders": result})
}

func (s *Server) validatedFolder(name, serverPath string) (string, string, error) {
	name = strings.TrimSpace(name)
	serverPath = strings.TrimSpace(serverPath)
	if len(name) < 1 || len(name) > 80 || len(serverPath) < 1 || len(serverPath) > 1024 {
		return "", "", errInvalidFolder
	}
	mountPath, err := s.Library.IsArchiveFolder(serverPath)
	if err != nil {
		return "", "", err
	}
	return name, mountPath, nil
}

var errInvalidFolder = &folderInputError{}

type folderInputError struct{}

func (*folderInputError) Error() string { return "invalid folder" }

func (s *Server) createFolder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name       string `json:"name"`
		ServerPath string `json:"serverPath"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	name, mountPath, err := s.validatedFolder(input.Name, input.ServerPath)
	if err != nil {
		problem(w, http.StatusBadRequest, "folder_outside_library_or_invalid")
		return
	}
	folder, err := s.Store.CreateFolder(r.Context(), name, strings.TrimSpace(input.ServerPath), mountPath)
	if err != nil {
		problem(w, http.StatusConflict, "folder_exists")
		return
	}
	s.beginScan(folder)
	jsonOut(w, http.StatusCreated, map[string]any{"folder": s.folderView(folder, true)})
}

func (s *Server) updateFolder(w http.ResponseWriter, r *http.Request) {
	if !idPattern.MatchString(r.PathValue("id")) {
		problem(w, http.StatusNotFound, "folder_not_found")
		return
	}
	var input struct {
		Name       string `json:"name"`
		ServerPath string `json:"serverPath"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	name, mountPath, err := s.validatedFolder(input.Name, input.ServerPath)
	if err != nil {
		problem(w, http.StatusBadRequest, "folder_outside_library_or_invalid")
		return
	}
	folder, err := s.Store.UpdateFolder(r.Context(), r.PathValue("id"), name, strings.TrimSpace(input.ServerPath), mountPath)
	if err != nil {
		problem(w, http.StatusNotFound, "folder_not_found")
		return
	}
	s.beginScan(folder)
	jsonOut(w, http.StatusOK, map[string]any{"folder": s.folderView(folder, true)})
}

func (s *Server) scanFolder(w http.ResponseWriter, r *http.Request) {
	folder, err := s.Store.Folder(r.Context(), r.PathValue("id"))
	if err != nil {
		problem(w, http.StatusNotFound, "folder_not_found")
		return
	}
	if _, err := s.Library.IsArchiveFolder(folder.ServerPath); err != nil {
		problem(w, http.StatusConflict, "folder_unavailable")
		return
	}
	s.beginScan(folder)
	jsonOut(w, http.StatusAccepted, map[string]bool{"accepted": true})
}

func (s *Server) beginScan(folder store.FolderRecord) {
	if s.ScanFolder == nil {
		return
	}
	started, err := s.Store.BeginFolderScan(context.Background(), folder.ID)
	if err != nil || !started {
		return
	}
	go s.ScanFolder(folder)
}
