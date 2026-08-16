package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type UserRecord struct {
	ID           int64  `json:"-"`
	PublicID     string `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"`
	Active       bool   `json:"active"`
	CreatedAt    int64  `json:"createdAt"`
}

func scanUser(row interface{ Scan(...any) error }) (UserRecord, error) {
	var user UserRecord
	err := row.Scan(&user.ID, &user.PublicID, &user.Username, &user.PasswordHash, &user.Role, &user.Active, &user.CreatedAt)
	return user, err
}

func (s *Store) UserByUsername(ctx context.Context, username string) (UserRecord, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT id,public_id,username,password_hash,role,active,created_at FROM users WHERE username=?`, username))
}

func (s *Store) UserByID(ctx context.Context, id int64) (UserRecord, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT id,public_id,username,password_hash,role,active,created_at FROM users WHERE id=?`, id))
}

func (s *Store) Users(ctx context.Context) ([]UserRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,public_id,username,password_hash,role,active,created_at FROM users ORDER BY username COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []UserRecord
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Store) CreateUser(ctx context.Context, username, passwordHash, role string) (UserRecord, error) {
	id, err := publicID()
	if err != nil {
		return UserRecord{}, err
	}
	now := time.Now().UnixMilli()
	result, err := s.db.ExecContext(ctx, `INSERT INTO users(public_id,username,password_hash,role,active,created_at) VALUES(?,?,?,?,1,?)`, id, username, passwordHash, role, now)
	if err != nil {
		return UserRecord{}, err
	}
	databaseID, err := result.LastInsertId()
	if err != nil {
		return UserRecord{}, err
	}
	return s.UserByID(ctx, databaseID)
}

func (s *Store) EnsureAdmin(ctx context.Context, username, passwordHash string) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	_, err := s.CreateUser(ctx, username, passwordHash, "admin")
	return err
}

func (s *Store) UpdatePassword(ctx context.Context, username, passwordHash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE users SET password_hash=? WHERE username=? AND active=1`, passwordHash, username)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) HasUsers(ctx context.Context) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&count)
	return count > 0, err
}

type FolderRecord struct {
	ID         int64  `json:"-"`
	PublicID   string `json:"id"`
	Name       string `json:"name"`
	ServerPath string `json:"serverPath,omitempty"`
	MountPath  string `json:"-"`
	Enabled    bool   `json:"enabled"`
	ScanStatus string `json:"scanStatus"`
	LastScanAt *int64 `json:"lastScanAt"`
	ScanError  string `json:"scanError,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

func scanFolder(row interface{ Scan(...any) error }) (FolderRecord, error) {
	var folder FolderRecord
	err := row.Scan(&folder.ID, &folder.PublicID, &folder.Name, &folder.ServerPath, &folder.MountPath, &folder.Enabled, &folder.ScanStatus, &folder.LastScanAt, &folder.ScanError, &folder.CreatedAt, &folder.UpdatedAt)
	return folder, err
}

const folderColumns = `id,public_id,name,server_path,mount_path,enabled,scan_status,last_scan_at,scan_error,created_at,updated_at`

func (s *Store) Folders(ctx context.Context) ([]FolderRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+folderColumns+` FROM folders WHERE enabled=1 ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var folders []FolderRecord
	for rows.Next() {
		folder, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		folders = append(folders, folder)
	}
	return folders, rows.Err()
}

func (s *Store) Folder(ctx context.Context, publicID string) (FolderRecord, error) {
	return scanFolder(s.db.QueryRowContext(ctx, `SELECT `+folderColumns+` FROM folders WHERE public_id=? AND enabled=1`, publicID))
}

func (s *Store) FolderByID(ctx context.Context, id int64) (FolderRecord, error) {
	return scanFolder(s.db.QueryRowContext(ctx, `SELECT `+folderColumns+` FROM folders WHERE id=? AND enabled=1`, id))
}

func (s *Store) EnsureBootstrapFolder(ctx context.Context, name, serverPath, mountPath string) (FolderRecord, error) {
	var folder FolderRecord
	err := s.db.QueryRowContext(ctx, `SELECT `+folderColumns+` FROM folders ORDER BY id LIMIT 1`).Scan(
		&folder.ID, &folder.PublicID, &folder.Name, &folder.ServerPath, &folder.MountPath, &folder.Enabled, &folder.ScanStatus, &folder.LastScanAt, &folder.ScanError, &folder.CreatedAt, &folder.UpdatedAt)
	if err != nil {
		return FolderRecord{}, err
	}
	if folder.ServerPath != "" {
		return folder, nil
	}
	_, err = s.db.ExecContext(ctx, `UPDATE folders SET name=?,server_path=?,mount_path=?,updated_at=? WHERE id=?`, name, serverPath, mountPath, time.Now().UnixMilli(), folder.ID)
	if err != nil {
		return FolderRecord{}, err
	}
	return s.FolderByID(ctx, folder.ID)
}

func (s *Store) CreateFolder(ctx context.Context, name, serverPath, mountPath string) (FolderRecord, error) {
	publicID, err := publicID()
	if err != nil {
		return FolderRecord{}, err
	}
	now := time.Now().UnixMilli()
	result, err := s.db.ExecContext(ctx, `INSERT INTO folders(public_id,name,server_path,mount_path,enabled,scan_status,created_at,updated_at) VALUES(?,?,?,?,1,'pending',?,?)`, publicID, name, serverPath, mountPath, now, now)
	if err != nil {
		return FolderRecord{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return FolderRecord{}, err
	}
	return s.FolderByID(ctx, id)
}

func (s *Store) UpdateFolder(ctx context.Context, publicID, name, serverPath, mountPath string) (FolderRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return FolderRecord{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE folders SET name=?,server_path=?,mount_path=?,scan_status='pending',scan_error='',updated_at=? WHERE public_id=? AND enabled=1`, name, serverPath, mountPath, time.Now().UnixMilli(), publicID)
	if err != nil {
		return FolderRecord{}, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return FolderRecord{}, sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, `UPDATE segments SET available=0,missing_at=? WHERE folder_id=(SELECT id FROM folders WHERE public_id=?)`, time.Now().UnixMilli(), publicID); err != nil {
		return FolderRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return FolderRecord{}, err
	}
	return s.Folder(ctx, publicID)
}

func (s *Store) BeginFolderScan(ctx context.Context, id int64) (bool, error) {
	now := time.Now().UnixMilli()
	result, err := s.db.ExecContext(ctx, `UPDATE folders SET scan_status='scanning',scan_error='',updated_at=? WHERE id=? AND (scan_status<>'scanning' OR updated_at<?)`, now, id, now-int64((30*time.Minute)/time.Millisecond))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count > 0, err
}

func (s *Store) RecoverInterruptedScans(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE folders SET scan_status='pending',scan_error='',updated_at=? WHERE scan_status='scanning'`, time.Now().UnixMilli())
	return err
}

func (s *Store) SetFolderScan(ctx context.Context, id int64, status, safeError string) error {
	now := time.Now().UnixMilli()
	var lastScan any
	if status == "ready" || status == "failed" {
		lastScan = now
	}
	_, err := s.db.ExecContext(ctx, `UPDATE folders SET scan_status=?,last_scan_at=COALESCE(?,last_scan_at),scan_error=?,updated_at=? WHERE id=?`, status, lastScan, safeError, now, id)
	return err
}

func (s *Store) FolderFirstRecording(ctx context.Context, id int64) (int64, bool, error) {
	var value sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT min(start_ms) FROM segments WHERE folder_id=? AND available=1 AND probe_status='ready'`, id).Scan(&value)
	return value.Int64, value.Valid, err
}

func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }

func (s *Store) Find(token string) ([]byte, bool, error) {
	var data []byte
	err := s.db.QueryRow(`SELECT data FROM sessions WHERE token=? AND expiry>?`, token, time.Now().Unix()).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return data, err == nil, err
}

func (s *Store) Commit(token string, data []byte, expiry time.Time) error {
	_, err := s.db.Exec(`INSERT INTO sessions(token,data,expiry) VALUES(?,?,?) ON CONFLICT(token) DO UPDATE SET data=excluded.data,expiry=excluded.expiry`, token, data, expiry.Unix())
	return err
}

func (s *Store) Delete(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token=?`, token)
	return err
}
