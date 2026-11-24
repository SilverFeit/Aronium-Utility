package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/SilverFeit/Aronium-Utility/internal/config"
	"github.com/SilverFeit/Aronium-Utility/internal/db"
)

// BackupManifest describes a completed hot backup artifact (TODO-038, TODO-039).
type BackupManifest struct {
	ArchivePath    string    `json:"archive_path"`
	ChecksumPath   string    `json:"checksum_path"`
	SHA256         string    `json:"sha256"`
	Encrypted      bool      `json:"encrypted"`
	Compressed     bool      `json:"compressed"`
	SizeBytes      int64     `json:"size_bytes"`
	StoreID        string    `json:"store_id"`
	CashRegisterID string    `json:"cash_register_id"`
	CreatedAt      time.Time `json:"created_at"`
}

// Manager performs hot SQLite backups, AES-256-GCM encryption, retention pruning, verification, and safe restore.
type Manager struct {
	Cfg *config.Config
}

func NewManager(cfg *config.Config) *Manager {
	return &Manager{Cfg: cfg}
}

// Create creates a consistent hot backup using SQLite's online VACUUM INTO snapshot API (never raw file copy - TODO-038, TODO-039).
func (m *Manager) Create(ctx context.Context, passphrase string) (*BackupManifest, error) {
	backupDir := m.Cfg.Backup.BackupDir
	if backupDir == "" {
		backupDir = filepath.Join(config.DefaultAppDataDir(), "backups")
	}
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return nil, err
	}

	workingPosPath := m.Cfg.PosDBPath
	if cpRes, cpErr := db.SyncPosDBToLocalCopy(m.Cfg.PosDBPath, m.Cfg.LocalPosCopyPath, true); cpErr == nil && cpRes != nil && cpRes.LocalCopyPath != "" {
		workingPosPath = cpRes.LocalCopyPath
	}
	posConn, err := db.OpenPosDBForOnlineBackup(workingPosPath)
	if err != nil {
		return nil, err
	}
	defer posConn.Close()

	now := time.Now().UTC()
	ts := now.Format("20060102_150405")
	tmpSnapshot := filepath.Join(backupDir, fmt.Sprintf(".snapshot_%s_%d.db", ts, now.UnixNano()))
	defer os.Remove(tmpSnapshot)

	// Online consistent snapshot via VACUUM INTO with SQLITE_BUSY exponential backoff retry
	escapedDest := strings.ReplaceAll(filepath.ToSlash(tmpSnapshot), "'", "''")
	_, err = db.WithRetry(6, func() (int, error) {
		_, e := posConn.ExecContext(ctx, fmt.Sprintf(`VACUUM INTO '%s';`, escapedDest))
		return 0, e
	})
	if err != nil {
		return nil, fmt.Errorf("online sqlite snapshot failed: %w", err)
	}

	rawDBBytes, err := os.ReadFile(tmpSnapshot)
	if err != nil {
		return nil, err
	}

	var payloadBytes []byte
	ext := ".db"
	if m.Cfg.Backup.Compress {
		var zbuf bytes.Buffer
		zw := zip.NewWriter(&zbuf)
		w, err := zw.Create("pos.db")
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(rawDBBytes); err != nil {
			return nil, err
		}
		if err := zw.Close(); err != nil {
			return nil, err
		}
		payloadBytes = zbuf.Bytes()
		ext = ".db.zip"
	} else {
		payloadBytes = rawDBBytes
	}

	encrypted := false
	if m.Cfg.Backup.EncryptAES256 || passphrase != "" {
		enc, err := config.EncryptBytes(payloadBytes, passphrase)
		if err != nil {
			return nil, err
		}
		payloadBytes = enc
		ext += ".enc"
		encrypted = true
	}

	archiveName := fmt.Sprintf("aronium_backup_%s_%s_%s%s", m.Cfg.StoreID, m.Cfg.CashRegisterID, ts, ext)
	archivePath := filepath.Join(backupDir, archiveName)
	if err := os.WriteFile(archivePath, payloadBytes, 0600); err != nil {
		return nil, err
	}

	sum := sha256.Sum256(payloadBytes)
	shaHex := hex.EncodeToString(sum[:])
	checksumPath := archivePath + ".sha256"
	checksumContent := fmt.Sprintf("%s  %s\n", shaHex, archiveName)
	_ = os.WriteFile(checksumPath, []byte(checksumContent), 0644)

	return &BackupManifest{
		ArchivePath:    archivePath,
		ChecksumPath:   checksumPath,
		SHA256:         shaHex,
		Encrypted:      encrypted,
		Compressed:     m.Cfg.Backup.Compress,
		SizeBytes:      int64(len(payloadBytes)),
		StoreID:        m.Cfg.StoreID,
		CashRegisterID: m.Cfg.CashRegisterID,
		CreatedAt:      now,
	}, nil
}

// Verify validates the SHA-256 checksum, decrypts/decompresses the backup in a temporary sandbox, and runs PRAGMA integrity_check (TODO-041).
func (m *Manager) Verify(archivePath, passphrase string) (string, error) {
	rawArchive, err := os.ReadFile(archivePath)
	if err != nil {
		return "", err
	}

	// 1. Validate SHA-256 companion file if present
	checksumPath := archivePath + ".sha256"
	if sumData, err := os.ReadFile(checksumPath); err == nil {
		expectedHex := strings.Fields(string(sumData))[0]
		actualSum := sha256.Sum256(rawArchive)
		actualHex := hex.EncodeToString(actualSum[:])
		if !strings.EqualFold(expectedHex, actualHex) {
			return "", fmt.Errorf("backup SHA-256 mismatch: expected %s, got %s", expectedHex, actualHex)
		}
	}

	// 2. Decrypt if .enc
	payload := rawArchive
	if strings.HasSuffix(archivePath, ".enc") {
		dec, err := config.DecryptBytes(payload, passphrase)
		if err != nil {
			return "", fmt.Errorf("backup AES-256-GCM decryption failed: %w", err)
		}
		payload = dec
	}

	// 3. Decompress if .zip
	var sqliteBytes []byte
	if strings.Contains(archivePath, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
		if err != nil {
			return "", fmt.Errorf("invalid backup zip archive: %w", err)
		}
		for _, f := range zr.File {
			if f.Name == "pos.db" || strings.HasSuffix(f.Name, ".db") {
				rc, err := f.Open()
				if err != nil {
					return "", err
				}
				sqliteBytes, err = io.ReadAll(rc)
				rc.Close()
				if err != nil {
					return "", err
				}
				break
			}
		}
		if len(sqliteBytes) == 0 {
			return "", fmt.Errorf("pos.db entry not found in backup zip")
		}
	} else {
		sqliteBytes = payload
	}

	// 4. Write to temp file and run PRAGMA integrity_check
	tmpVerify := filepath.Join(os.TempDir(), fmt.Sprintf("aronium_verify_%d.db", time.Now().UnixNano()))
	defer os.Remove(tmpVerify)
	if err := os.WriteFile(tmpVerify, sqliteBytes, 0600); err != nil {
		return "", err
	}

	conn, err := db.OpenPosDBReadOnly(tmpVerify)
	if err != nil {
		return "", fmt.Errorf("failed to open verified sqlite image: %w", err)
	}
	defer conn.Close()

	var integrity string
	if err := conn.QueryRow(`PRAGMA integrity_check;`).Scan(&integrity); err != nil {
		return "", fmt.Errorf("PRAGMA integrity_check failed: %w", err)
	}
	if strings.ToLower(integrity) != "ok" {
		return integrity, fmt.Errorf("sqlite integrity_check reported corruption: %s", integrity)
	}

	_, missing, ver, err := db.IntrospectSchema(conn)
	if err != nil {
		return "", err
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("backup is missing core Aronium tables: %v", missing)
	}

	return fmt.Sprintf("OK (PRAGMA integrity_check=ok, schema=%s, size=%d bytes)", ver, len(sqliteBytes)), nil
}

// Restore verifies the backup, checks that Aronium is not running, saves a pre-restore safety backup, and restores pos.db (TODO-041, TC-017, TC-025).
func (m *Manager) Restore(archivePath, targetPosDBPath, passphrase string, skipProcessCheck bool) (safetyBackupPath string, err error) {
	if targetPosDBPath == "" {
		targetPosDBPath = m.Cfg.PosDBPath
	}
	if !skipProcessCheck && IsAroniumProcessRunning() {
		return "", fmt.Errorf("restore aborted: Aronium Lite (Aronium.Pos.exe) is currently running. Please close Aronium before restoring pos.db")
	}

	// 1. Verify archive integrity first
	if _, err := m.Verify(archivePath, passphrase); err != nil {
		return "", fmt.Errorf("pre-restore verification failed: %w", err)
	}

	// 2. Extract raw SQLite bytes
	rawArchive, err := os.ReadFile(archivePath)
	if err != nil {
		return "", err
	}
	payload := rawArchive
	if strings.HasSuffix(archivePath, ".enc") {
		payload, err = config.DecryptBytes(payload, passphrase)
		if err != nil {
			return "", err
		}
	}
	var sqliteBytes []byte
	if strings.Contains(archivePath, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
		if err != nil {
			return "", err
		}
		for _, f := range zr.File {
			if f.Name == "pos.db" || strings.HasSuffix(f.Name, ".db") {
				rc, _ := f.Open()
				sqliteBytes, _ = io.ReadAll(rc)
				rc.Close()
				break
			}
		}
	} else {
		sqliteBytes = payload
	}

	// 3. Create pre-restore safety backup if target exists
	if existing, err := os.ReadFile(targetPosDBPath); err == nil && len(existing) > 0 {
		safetyBackupPath = fmt.Sprintf("%s.pre_restore_%s.bak", targetPosDBPath, time.Now().UTC().Format("20060102_150405"))
		if err := os.WriteFile(safetyBackupPath, existing, 0600); err != nil {
			return "", fmt.Errorf("failed to create pre-restore safety backup: %w", err)
		}
	}

	// 4. Write restored database atomically and remove stale WAL/SHM files
	if err := os.MkdirAll(filepath.Dir(targetPosDBPath), 0755); err != nil {
		return safetyBackupPath, err
	}
	_ = os.Remove(targetPosDBPath + "-wal")
	_ = os.Remove(targetPosDBPath + "-shm")

	tmpRestore := targetPosDBPath + ".restoring.tmp"
	if err := os.WriteFile(tmpRestore, sqliteBytes, 0644); err != nil {
		return safetyBackupPath, err
	}
	if err := os.Rename(tmpRestore, targetPosDBPath); err != nil {
		return safetyBackupPath, err
	}
	return safetyBackupPath, nil
}

// ApplyRetention prunes old backups keeping at most maxKeep recent archives (TODO-040).
func (m *Manager) ApplyRetention(maxKeep int) (removed []string, err error) {
	if maxKeep <= 0 {
		maxKeep = m.Cfg.Backup.RetainDaily
		if maxKeep <= 0 {
			maxKeep = 7
		}
	}
	entries, err := os.ReadDir(m.Cfg.Backup.BackupDir)
	if err != nil {
		return nil, err
	}
	var archives []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && strings.HasPrefix(name, "aronium_backup_") && !strings.HasSuffix(name, ".sha256") {
			archives = append(archives, filepath.Join(m.Cfg.Backup.BackupDir, name))
		}
	}
	sort.Strings(archives)
	if len(archives) <= maxKeep {
		return nil, nil
	}
	toDelete := archives[:len(archives)-maxKeep]
	for _, p := range toDelete {
		_ = os.Remove(p)
		_ = os.Remove(p + ".sha256")
		removed = append(removed, p)
	}
	return removed, nil
}

// IsAroniumProcessRunning checks whether Aronium.Pos.exe is currently running on Windows.
func IsAroniumProcessRunning() bool {
	if runtime.GOOS != "windows" {
		return false
	}
	out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq Aronium.Pos.exe", "/NH").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), "aronium.pos.exe")
}
