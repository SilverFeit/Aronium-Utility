package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SilverFeit/Aronium-Utility/internal/config"
	"github.com/SilverFeit/Aronium-Utility/internal/db"
	"github.com/SilverFeit/Aronium-Utility/internal/service"
	_ "modernc.org/sqlite"
)

// BackedUpDBFile describes a single database or config file snapshot created by CreateAllDatabasesSnapshot.
type BackedUpDBFile struct {
	Name        string `json:"name"`
	SourcePath  string `json:"source_path"`
	LocalPath   string `json:"local_path"`
	LogicalPath string `json:"logical_path"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
	MimeType    string `json:"mime_type"`
}

// AllDBsBackupResult summarizes an explicit full backup of pos.db, cuentas.db, state.db and config to PC & Google Drive.
type AllDBsBackupResult struct {
	Timestamp             string            `json:"timestamp"`
	BackupFolder          string            `json:"backup_folder"`
	ZipArchivePath        string            `json:"zip_archive_path"`
	ZipLogicalPath        string            `json:"zip_logical_path"`
	ZipSHA256             string            `json:"zip_sha256"`
	DocumentsAccountsCopy string            `json:"documents_accounts_copy,omitempty"`
	Files                 []BackedUpDBFile  `json:"files"`
	DriveConnected        bool              `json:"drive_connected"`
	UploadedDriveIDs      map[string]string `json:"uploaded_drive_ids"`
	UploadWarnings        []string          `json:"upload_warnings,omitempty"`
	Message               string            `json:"message"`
}

// snapshotSQLiteFile captures a consistent SQLite snapshot using VACUUM INTO when readable,
// or falls back to reading raw bytes safely if the database is custom-encrypted/locked.
func snapshotSQLiteFile(ctx context.Context, srcPath, destPath string, key string) ([]byte, error) {
	if _, err := os.Stat(srcPath); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(srcPath)
	if err != nil {
		abs = srcPath
	}
	slash := filepath.ToSlash(abs)
	if !strings.HasPrefix(slash, "/") {
		slash = "/" + slash
	}
	u := url.URL{Scheme: "file", Path: slash}
	q := url.Values{}
	if strings.TrimSpace(key) != "" {
		q.Add("_pragma", fmt.Sprintf("key('%s')", strings.ReplaceAll(key, "'", "''")))
	}
	q.Set("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()

	tmpFile := destPath + fmt.Sprintf(".%d.snap.tmp", time.Now().UnixNano())
	defer os.Remove(tmpFile)

	if conn, oErr := sql.Open("sqlite", u.String()); oErr == nil {
		escaped := strings.ReplaceAll(filepath.ToSlash(tmpFile), "'", "''")
		_, vErr := db.WithRetry(4, func() (int, error) {
			_, e := conn.ExecContext(ctx, fmt.Sprintf(`VACUUM INTO '%s';`, escaped))
			return 0, e
		})
		_ = conn.Close()
		if vErr == nil {
			if raw, rErr := os.ReadFile(tmpFile); rErr == nil && len(raw) > 0 {
				if wErr := os.WriteFile(destPath, raw, 0644); wErr != nil {
					return nil, wErr
				}
				return raw, nil
			}
		}
	}

	raw, err := os.ReadFile(srcPath)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(destPath, raw, 0644); err != nil {
		return nil, err
	}
	return raw, nil
}

// CreateAllDatabasesSnapshot snapshots pos.db, cuentas.db, state.db and aronium-util.yaml into local sync & backup folders.
func (m *Manager) CreateAllDatabasesSnapshot(ctx context.Context, cfgPath string) (*AllDBsBackupResult, error) {
	tsNow := time.Now()
	tsTag := tsNow.Format("20060102_150405")

	backupDir := m.Cfg.Backup.BackupDir
	if strings.TrimSpace(backupDir) == "" {
		backupDir = filepath.Join(config.DefaultAppDataDir(), "backups")
	}
	rootFolder := m.Cfg.GoogleDrive.RootFolderName
	if strings.TrimSpace(rootFolder) == "" {
		rootFolder = "AroniumUtility"
	}
	syncDir := m.Cfg.GoogleDrive.LocalDriveSyncDir
	if strings.TrimSpace(syncDir) == "" {
		syncDir = filepath.Join(config.DefaultAppDataDir(), "GoogleDriveSync")
	}
	syncBackupDir := filepath.Join(syncDir, rootFolder, "Backups_BD")

	for _, d := range []string{backupDir, syncBackupDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return nil, fmt.Errorf("no se pudo crear directorio de respaldo %s: %w", d, err)
		}
	}

	res := &AllDBsBackupResult{
		Timestamp:        tsNow.Format("2006-01-02 15:04:05"),
		BackupFolder:     syncBackupDir,
		UploadedDriveIDs: make(map[string]string),
	}

	// Refresh isolated pos_local_copy.db via pure OS byte copy first so Aronium Lite is never locked
	posSrcForBackup := m.Cfg.LocalPosCopyPath
	if cpRes, cpErr := db.SyncPosDBToLocalCopy(m.Cfg.PosDBPath, m.Cfg.LocalPosCopyPath, true); cpErr == nil && cpRes != nil {
		posSrcForBackup = cpRes.LocalCopyPath
	} else if strings.TrimSpace(posSrcForBackup) == "" {
		posSrcForBackup = db.LocalPosDBCopyPath()
	}
	if _, stErr := os.Stat(posSrcForBackup); stErr != nil {
		posSrcForBackup = m.Cfg.PosDBPath
	}

	type sourceTarget struct {
		Name     string
		Src      string
		SQLite   bool
		Key      string
		MimeType string
	}
	targets := []sourceTarget{
		{Name: "pos.db", Src: posSrcForBackup, SQLite: true, Key: os.Getenv("ARONIUM_POS_DB_PASSWORD"), MimeType: "application/x-sqlite3"},
		{Name: "cuentas.db", Src: m.Cfg.AccountsDBPath, SQLite: true, MimeType: "application/x-sqlite3"},
		{Name: "aronium_utility_state.db", Src: m.Cfg.StateDBPath, SQLite: true, MimeType: "application/x-sqlite3"},
	}
	if strings.TrimSpace(cfgPath) != "" {
		targets = append(targets, sourceTarget{Name: "aronium-util.yaml", Src: cfgPath, SQLite: false, MimeType: "text/yaml"})
	}

	zipName := fmt.Sprintf("Respaldo_Completo_BDs_%s.zip", tsTag)
	zipInBackups := filepath.Join(backupDir, zipName)
	zipInSync := filepath.Join(syncBackupDir, zipName)

	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)

	for _, t := range targets {
		if strings.TrimSpace(t.Src) == "" {
			continue
		}
		if _, stErr := os.Stat(t.Src); stErr != nil {
			res.UploadWarnings = append(res.UploadWarnings, fmt.Sprintf("Omitido %s (no encontrado en %s)", t.Name, t.Src))
			continue
		}
		destLocal := filepath.Join(syncBackupDir, t.Name)
		var data []byte
		var err error
		if t.SQLite {
			data, err = snapshotSQLiteFile(ctx, t.Src, destLocal, t.Key)
		} else {
			data, err = os.ReadFile(t.Src)
			if err == nil {
				err = os.WriteFile(destLocal, data, 0644)
			}
		}
		if err != nil {
			res.UploadWarnings = append(res.UploadWarnings, fmt.Sprintf("Error respaldando %s: %v", t.Name, err))
			continue
		}

		if w, zErr := zw.Create(t.Name); zErr == nil {
			_, _ = w.Write(data)
		}
		sum := sha256.Sum256(data)
		res.Files = append(res.Files, BackedUpDBFile{
			Name:        t.Name,
			SourcePath:  t.Src,
			LocalPath:   destLocal,
			LogicalPath: "Backups_BD/" + t.Name,
			SizeBytes:   int64(len(data)),
			SHA256:      hex.EncodeToString(sum[:]),
			MimeType:    t.MimeType,
		})
	}

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("error creando paquete zip de bases: %w", err)
	}
	zipBytes := zbuf.Bytes()
	if err := os.WriteFile(zipInBackups, zipBytes, 0644); err != nil {
		return nil, err
	}
	_ = os.WriteFile(zipInSync, zipBytes, 0644)

	zipSum := sha256.Sum256(zipBytes)
	zipHex := hex.EncodeToString(zipSum[:])
	_ = os.WriteFile(zipInBackups+".sha256", []byte(fmt.Sprintf("%s  %s\n", zipHex, zipName)), 0644)

	res.ZipArchivePath = zipInBackups
	res.ZipLogicalPath = "Backups_BD/" + zipName
	res.ZipSHA256 = zipHex

	// Also refresh safe copy of cuentas.db in Mis Documentos
	if latestDocCopy, _, dErr := service.BackupAccountsDBToDocuments(filepath.Dir(m.Cfg.AccountsDBPath), m.Cfg.AccountsDBPath); dErr == nil && latestDocCopy != "" {
		res.DocumentsAccountsCopy = latestDocCopy
	}

	res.Message = fmt.Sprintf("Respaldo local completo (%d archivo(s)) guardado en %s y %s", len(res.Files), zipInBackups, syncBackupDir)
	return res, nil
}
