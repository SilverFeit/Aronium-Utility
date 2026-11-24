package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/SilverFeit/Aronium-Utility/internal/config"
)

// UninstallReport summarizes the 100% clean uninstall and Documents backup of cuentas.db.
type UninstallReport struct {
	AccountsBackedUp      bool     `json:"accounts_backed_up"`
	AccountsBackupPath    string   `json:"accounts_backup_path,omitempty"`
	AccountsTimestampPath string   `json:"accounts_timestamp_path,omitempty"`
	DocumentsBackupDir    string   `json:"documents_backup_dir"`
	InstallDirRemoved     bool     `json:"install_dir_removed"`
	DataDirRemoved        bool     `json:"data_dir_removed"`
	Warnings              []string `json:"warnings,omitempty"`
}

// DocumentsBackupDir returns %USERPROFILE%\Documents\AroniumUtility_Respaldo_Cuentas (or localized Windows MyDocuments).
func DocumentsBackupDir() string {
	if custom := strings.TrimSpace(os.Getenv("ARONIUM_DOCS_BACKUP_DIR")); custom != "" {
		return custom
	}
	docsRoot := resolveUserDocumentsDir()
	return filepath.Join(docsRoot, "AroniumUtility_Respaldo_Cuentas")
}

func resolveUserDocumentsDir() string {
	if runtime.GOOS == "windows" {
		// Query Windows Shell Folders so localized / OneDrive Documents folders resolve accurately.
		cmd := exec.Command("reg.exe", "query", `HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\User Shell Folders`, "/v", "Personal")
		HideChildWindow(cmd)
		if out, err := cmd.Output(); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.Contains(strings.ToLower(trimmed), "personal") && strings.Contains(trimmed, "REG_") {
					fields := strings.SplitN(trimmed, "REG_EXPAND_SZ", 2)
					if len(fields) != 2 {
						fields = strings.SplitN(trimmed, "REG_SZ", 2)
					}
					if len(fields) == 2 {
						val := expandWindowsEnvVars(strings.TrimSpace(fields[1]))
						if val != "" {
							return val
						}
					}
				}
			}
		}
		psCmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "[Environment]::GetFolderPath('MyDocuments')")
		HideChildWindow(psCmd)
		if out, err := psCmd.Output(); err == nil {
			val := strings.TrimSpace(string(out))
			if val != "" {
				return val
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, "Documents")
	}
	return filepath.Join(os.TempDir(), "Documents")
}

func expandWindowsEnvVars(s string) string {
	result := s
	for _, key := range []string{"USERPROFILE", "ONEDRIVE", "ONEDRIVECOMMERCIAL", "LOCALAPPDATA", "APPDATA"} {
		if val := os.Getenv(key); val != "" {
			result = strings.ReplaceAll(result, "%"+key+"%", val)
		}
	}
	return result
}

// StopAllProcessesAndServices terminates all Aronium Utility processes and stops/removes services & tasks.
func StopAllProcessesAndServices() {
	if runtime.GOOS != "windows" {
		return
	}
	for _, cmdArgs := range [][]string{
		{"sc.exe", "stop", ServiceName},
		{"sc.exe", "delete", ServiceName},
		{"schtasks.exe", "/End", "/TN", TaskName},
		{"schtasks.exe", "/Delete", "/TN", TaskName, "/F"},
		{"taskkill.exe", "/F", "/T", "/IM", "aronium-utility-ui.exe"},
		{"taskkill.exe", "/F", "/T", "/IM", "aronium-util.exe"},
	} {
		c := exec.Command(cmdArgs[0], cmdArgs[1:]...)
		HideChildWindow(c)
		_ = c.Run()
	}
	ps := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"Get-Process -Name 'aronium-utility-ui','aronium-util' -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue")
	HideChildWindow(ps)
	_ = ps.Run()
	time.Sleep(500 * time.Millisecond)
}

// BackupAccountsDBToDocuments copies cuentas.db (and a timestamped copy) into DocumentsBackupDir().
func BackupAccountsDBToDocuments(dataDir, explicitAccountsDB string) (latestPath, tsPath string, err error) {
	candidates := []string{}
	if strings.TrimSpace(explicitAccountsDB) != "" {
		candidates = append(candidates, explicitAccountsDB)
	}
	if envAcc := strings.TrimSpace(os.Getenv("ARONIUM_ACCOUNTS_DB")); envAcc != "" {
		candidates = append(candidates, envAcc)
	}
	if strings.TrimSpace(dataDir) != "" {
		candidates = append(candidates,
			filepath.Join(dataDir, "cuentas.db"),
			filepath.Join(dataDir, "backups", "cuentas_pre_install_snapshot.db"),
			filepath.Join(dataDir, "GoogleDriveSync", "AroniumUtility", "Cuentas_Credito", "cuentas_backup.db"),
		)
	}
	if defData := config.DefaultAppDataDir(); defData != "" && defData != dataDir {
		candidates = append(candidates, filepath.Join(defData, "cuentas.db"))
	}

	var srcPath string
	var raw []byte
	for _, cand := range candidates {
		if data, rErr := os.ReadFile(cand); rErr == nil && len(data) > 0 {
			srcPath = cand
			raw = data
			break
		}
	}
	if len(raw) == 0 {
		return "", "", nil
	}

	destDir := DocumentsBackupDir()
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", "", fmt.Errorf("no se pudo crear carpeta de respaldo en Mis Documentos (%s): %w", destDir, err)
	}

	latestPath = filepath.Join(destDir, "cuentas.db")
	tsPath = filepath.Join(destDir, fmt.Sprintf("cuentas_respaldo_%s.db", time.Now().Format("20060102_150405")))

	if err := os.WriteFile(latestPath, raw, 0644); err != nil {
		return "", "", fmt.Errorf("error copiando %s a %s: %w", srcPath, latestPath, err)
	}
	_ = os.WriteFile(tsPath, raw, 0644)

	// Copy WAL/SHM companions if present for extra safety when raw copy occurred
	for _, ext := range []string{"-wal", "-shm"} {
		if sideData, sErr := os.ReadFile(srcPath + ext); sErr == nil && len(sideData) > 0 {
			_ = os.WriteFile(latestPath+ext, sideData, 0644)
		}
	}
	return latestPath, tsPath, nil
}

// RunCompleteUninstall backs up cuentas.db into Mis Documentos and wipes 100% of Aronium Utility binaries,
// data folders, shortcuts, services, PATH entries, shims, and environment variables.
func RunCompleteUninstall(installDir, dataDir, explicitAccountsDB string) (*UninstallReport, error) {
	docsDir := DocumentsBackupDir()
	rep := &UninstallReport{
		DocumentsBackupDir: docsDir,
	}

	// 1. Stop running processes & services so SQLite WAL/SHM flush cleanly before copying cuentas.db
	StopAllProcessesAndServices()

	// 2. Copy cuentas.db safely to Mis Documentos\AroniumUtility_Respaldo_Cuentas
	latestCopy, tsCopy, bErr := BackupAccountsDBToDocuments(dataDir, explicitAccountsDB)
	if bErr != nil {
		return rep, fmt.Errorf("no se pudo respaldar cuentas.db en Mis Documentos (desinstalación cancelada para proteger tus datos): %w", bErr)
	}
	if latestCopy != "" {
		rep.AccountsBackedUp = true
		rep.AccountsBackupPath = latestCopy
		rep.AccountsTimestampPath = tsCopy
	}

	// 3. Remove Windows shortcuts, services, scheduled tasks, registry variables, PATH entries & global shims
	if runtime.GOOS == "windows" {
		for _, sp := range []string{
			UIAutostartShortcutPath(),
			StartupShortcutPath(),
		} {
			if sp != "" {
				_ = os.Remove(sp)
			}
		}

		// Desktop shortcut (both current user & Public desktop)
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			_ = os.Remove(filepath.Join(home, "Desktop", "Aronium Utility UI.lnk"))
			_ = os.Remove(filepath.Join(home, "Escritorio", "Aronium Utility UI.lnk"))
		}
		if publicDir := os.Getenv("PUBLIC"); publicDir != "" {
			_ = os.Remove(filepath.Join(publicDir, "Desktop", "Aronium Utility UI.lnk"))
		}

		// Delete HKCU / HKLM ARONIUM_* environment variables and Run keys
		envVars := []string{
			"ARONIUM_UTIL_HOME", "ARONIUM_UTIL_CONFIG", "ARONIUM_POS_DB",
			"ARONIUM_LOCAL_POS_COPY", "ARONIUM_STATE_DB", "ARONIUM_ACCOUNTS_DB",
			"ARONIUM_STORE_ID", "ARONIUM_REGISTER_ID", "ARONIUM_CLI_BIN",
			"ARONIUM_UI_BIN", "ARONIUM_POS_DB_PASSWORD", "ARONIUM_TIMEZONE",
		}
		for _, k := range envVars {
			for _, root := range []string{`HKCU\Environment`, `HKLM\SYSTEM\CurrentControlSet\Control\Session Manager\Environment`} {
				c := exec.Command("reg.exe", "delete", root, "/v", k, "/f")
				HideChildWindow(c)
				_ = c.Run()
			}
		}
		for _, runVal := range []string{"AroniumUtilityUI", "AroniumUtilityService"} {
			c := exec.Command("reg.exe", "delete", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, "/v", runVal, "/f")
			HideChildWindow(c)
			_ = c.Run()
		}

		// Clean installDir from User & System PATH
		RemoveDirFromWindowsRegistryPath(`HKCU\Environment`, installDir)
		RemoveDirFromWindowsRegistryPath(`HKLM\SYSTEM\CurrentControlSet\Control\Session Manager\Environment`, installDir)

		// Remove global command shims from Windows & System32
		winDir := os.Getenv("SystemRoot")
		if winDir == "" {
			winDir = `C:\Windows`
		}
		for _, shim := range []string{
			filepath.Join(winDir, "aronium-util.cmd"),
			filepath.Join(winDir, "aronium-utility-ui.cmd"),
			filepath.Join(winDir, "aronium-util-uninstall.cmd"),
			filepath.Join(winDir, "System32", "aronium-util.exe"),
			filepath.Join(winDir, "SysWOW64", "aronium-util.exe"),
		} {
			_ = os.Remove(shim)
		}
		BroadcastEnvironmentChange()
	}

	// 4. Delete 100% of installDir and dataDir
	if strings.TrimSpace(dataDir) != "" {
		if err := os.RemoveAll(dataDir); err == nil {
			rep.DataDirRemoved = true
		} else {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("Limpieza diferida en %s: %v", dataDir, err))
		}
	}
	if strings.TrimSpace(installDir) != "" {
		if err := os.RemoveAll(installDir); err == nil {
			rep.InstallDirRemoved = true
		} else {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("Limpieza diferida en %s: %v", installDir, err))
		}
	}

	// If any handle (like the uninstaller running inside installDir) kept a folder locked, schedule delayed cleanup
	if runtime.GOOS == "windows" && (!rep.InstallDirRemoved || !rep.DataDirRemoved) {
		cleanupCmd := fmt.Sprintf(`ping 127.0.0.1 -n 3 >nul & if exist "%s" rmdir /s /q "%s" & if exist "%s" rmdir /s /q "%s"`,
			installDir, installDir, dataDir, dataDir)
		c := exec.Command("cmd.exe", "/C", cleanupCmd)
		HideChildWindow(c)
		_ = c.Start()
	}

	return rep, nil
}

// RemoveDirFromWindowsRegistryPath removes targetDir from the Path registry value if present.
func RemoveDirFromWindowsRegistryPath(regKey, targetDir string) {
	if runtime.GOOS != "windows" || strings.TrimSpace(targetDir) == "" {
		return
	}
	qCmd := exec.Command("reg.exe", "query", regKey, "/v", "Path")
	HideChildWindow(qCmd)
	out, err := qCmd.Output()
	if err != nil {
		return
	}
	var existing string
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(strings.ToLower(trimmed), "path") && strings.Contains(trimmed, "REG_") {
			fields := strings.SplitN(trimmed, "REG_EXPAND_SZ", 2)
			if len(fields) != 2 {
				fields = strings.SplitN(trimmed, "REG_SZ", 2)
			}
			if len(fields) == 2 {
				existing = strings.TrimSpace(fields[1])
			}
		}
	}
	if existing == "" {
		return
	}
	normTarget := strings.TrimRight(strings.TrimSpace(targetDir), `\`)
	parts := strings.Split(existing, ";")
	var kept []string
	changed := false
	for _, p := range parts {
		pt := strings.TrimSpace(p)
		if pt == "" {
			continue
		}
		if strings.EqualFold(strings.TrimRight(pt, `\`), normTarget) {
			changed = true
			continue
		}
		kept = append(kept, pt)
	}
	if changed {
		newVal := strings.Join(kept, ";")
		addCmd := exec.Command("reg.exe", "add", regKey, "/v", "Path", "/t", "REG_EXPAND_SZ", "/d", newVal, "/f")
		HideChildWindow(addCmd)
		_ = addCmd.Run()
	}
}

// BroadcastEnvironmentChange notifies Windows Explorer / shells that environment variables changed.
func BroadcastEnvironmentChange() {
	if runtime.GOOS != "windows" {
		return
	}
	ps := `Add-Type -Namespace Win32 -Name NativeMethods -MemberDefinition '[DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Auto)] public static extern IntPtr SendMessageTimeout(IntPtr hWnd, uint Msg, UIntPtr wParam, string lParam, uint fuFlags, uint uTimeout, out UIntPtr lpdwResult);'; $r = [UIntPtr]::Zero; [Win32.NativeMethods]::SendMessageTimeout([IntPtr]0xffff, 0x001A, [UIntPtr]::Zero, 'Environment', 2, 3000, [ref]$r) | Out-Null`
	c := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", ps)
	HideChildWindow(c)
	_ = c.Run()
}
