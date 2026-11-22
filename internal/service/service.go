package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/SilverFeit/Aronium-Utility/internal/config"
)

const ServiceName = "AroniumUtilityService"
const TaskName = "AroniumUtilityBackgroundWatcher"

// AutostartStatus describes whether Aronium Utility UI (+ background watcher) opens automatically on Windows logon.
type AutostartStatus struct {
	Enabled      bool   `json:"enabled"`
	Preference   string `json:"preference"`
	ShortcutPath string `json:"shortcut_path"`
	UIBinaryPath string `json:"ui_binary_path"`
	Mode         string `json:"mode"`
}

// Manager manages the background Windows service / startup task (TODO-016).
type Manager struct {
	AppDir     string
	BinaryPath string
	ConfigPath string
	LogPath    string
}

func NewManager(cfgPath string) *Manager {
	appDir := config.DefaultAppDataDir()
	exe, err := os.Executable()
	if err != nil {
		exe = "aronium-util.exe"
	}
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	return &Manager{
		AppDir:     appDir,
		BinaryPath: exe,
		ConfigPath: cfgPath,
		LogPath:    filepath.Join(appDir, "logs", "service.log"),
	}
}

// UIAutostartShortcutPath returns the Windows Startup Folder shortcut (.lnk) that opens Aronium Utility UI at logon.
func UIAutostartShortcutPath() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return ""
	}
	return filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup", "Aronium Utility.lnk")
}

// StartupShortcutPath returns the legacy watcher-only Startup shortcut path for cleanup/migration.
func StartupShortcutPath() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return ""
	}
	return filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup", "Aronium Utility Watcher.lnk")
}

// AutostartPreferencePath returns the local preference file path storing explicit user autostart choice.
func AutostartPreferencePath(appDir string) string {
	if strings.TrimSpace(appDir) == "" {
		appDir = config.DefaultAppDataDir()
	}
	return filepath.Join(appDir, "autostart.pref")
}

// ResolveUIBinaryPath locates aronium-utility-ui executable relative to explicit hints, sibling directory, or LocalAppData.
func ResolveUIBinaryPath(hint string) string {
	if custom := os.Getenv("ARONIUM_UI_BIN"); custom != "" {
		if info, err := os.Stat(custom); err == nil && !info.IsDir() {
			return custom
		}
	}
	uiName := "aronium-utility-ui"
	if runtime.GOOS == "windows" {
		uiName = "aronium-utility-ui.exe"
	}
	var dirs []string
	if strings.TrimSpace(hint) != "" {
		dirs = append(dirs, filepath.Dir(hint))
	}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		dirs = append(dirs, filepath.Join(localAppData, "Programs", "AroniumUtility"))
	}
	for _, d := range dirs {
		cand := filepath.Join(d, uiName)
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			return cand
		}
	}
	if strings.Contains(strings.ToLower(filepath.Base(hint)), "aronium-utility-ui") {
		return hint
	}
	return ""
}

// ReadAutostartPreference returns "enabled", "disabled", or "" if not yet saved.
func (m *Manager) ReadAutostartPreference() string {
	raw, err := os.ReadFile(AutostartPreferencePath(m.AppDir))
	if err != nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(string(raw))) {
	case "enabled", "true", "1", "on":
		return "enabled"
	case "disabled", "false", "0", "off":
		return "disabled"
	default:
		return ""
	}
}

func (m *Manager) writeAutostartPreference(enabled bool) error {
	if err := os.MkdirAll(m.AppDir, 0755); err != nil {
		return err
	}
	val := "disabled\n"
	if enabled {
		val = "enabled\n"
	}
	return os.WriteFile(AutostartPreferencePath(m.AppDir), []byte(val), 0644)
}

// UIAutostartStatus inspects whether automatic startup on Windows logon is active.
func (m *Manager) UIAutostartStatus() AutostartStatus {
	pref := m.ReadAutostartPreference()
	uiBin := ResolveUIBinaryPath(m.BinaryPath)
	st := AutostartStatus{
		Preference:   pref,
		UIBinaryPath: uiBin,
	}

	if runtime.GOOS == "windows" {
		st.Mode = "windows_startup_lnk"
		st.ShortcutPath = UIAutostartShortcutPath()
		if st.ShortcutPath != "" {
			if _, err := os.Stat(st.ShortcutPath); err == nil {
				st.Enabled = true
				return st
			}
		}
		if legacy := StartupShortcutPath(); legacy != "" {
			if _, err := os.Stat(legacy); err == nil && pref != "disabled" {
				st.ShortcutPath = legacy
				st.Enabled = true
				return st
			}
		}
		st.Enabled = false
		return st
	}

	st.Mode = "cross_platform_pref"
	st.ShortcutPath = AutostartPreferencePath(m.AppDir)
	st.Enabled = pref == "enabled"
	return st
}

// SetUIAutostart enables or disables automatic startup of Aronium Utility UI (+ watcher) at Windows logon.
func (m *Manager) SetUIAutostart(enabled bool, uiBinPath string) (AutostartStatus, error) {
	if err := m.writeAutostartPreference(enabled); err != nil {
		return m.UIAutostartStatus(), err
	}
	if strings.TrimSpace(uiBinPath) == "" {
		uiBinPath = ResolveUIBinaryPath(m.BinaryPath)
	}

	if runtime.GOOS != "windows" {
		return m.UIAutostartStatus(), nil
	}

	if enabled {
		if uiBinPath == "" {
			if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
				uiBinPath = filepath.Join(localAppData, "Programs", "AroniumUtility", "aronium-utility-ui.exe")
			} else {
				uiBinPath = filepath.Join(filepath.Dir(m.BinaryPath), "aronium-utility-ui.exe")
			}
		}
		if _, err := createWindowsStartupShortcut(UIAutostartShortcutPath(), uiBinPath, "", "Aronium Utility UI (Cuentas, Bandeja y Monitor en Vivo)"); err != nil {
			return m.UIAutostartStatus(), fmt.Errorf("failed to create Windows Startup shortcut: %w", err)
		}
		// Migrate away from watcher-only startup entry so UI + watcher open cleanly without duplicate daemons.
		if legacy := StartupShortcutPath(); legacy != "" {
			_ = os.Remove(legacy)
		}
		return m.UIAutostartStatus(), nil
	}

	if sp := UIAutostartShortcutPath(); sp != "" {
		if err := os.Remove(sp); err != nil && !os.IsNotExist(err) {
			return m.UIAutostartStatus(), err
		}
	}
	_ = m.RemoveStartupShortcut()
	cmdReg := exec.Command("reg.exe", "delete", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, "/v", "AroniumUtilityUI", "/f")
	HideChildWindow(cmdReg)
	_ = cmdReg.Run()
	cmdRegOld := exec.Command("reg.exe", "delete", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, "/v", "AroniumUtilityService", "/f")
	HideChildWindow(cmdRegOld)
	_ = cmdRegOld.Run()
	cmdTask := exec.Command("schtasks.exe", "/Delete", "/TN", TaskName, "/F")
	HideChildWindow(cmdTask)
	_ = cmdTask.Run()

	return m.UIAutostartStatus(), nil
}

// Install registers Aronium Utility UI + watcher as an automatic Windows startup app (TODO-016).
func (m *Manager) Install() (string, error) {
	if err := os.MkdirAll(filepath.Dir(m.LogPath), 0755); err != nil {
		return "", err
	}
	if runtime.GOOS != "windows" {
		_, _ = m.SetUIAutostart(true, "")
		unitPath := filepath.Join(m.AppDir, "aronium-utility.service")
		unit := fmt.Sprintf("[Service]\nExecStart=%s watch --config %s\nLogPath=%s\n", m.BinaryPath, m.ConfigPath, m.LogPath)
		if err := os.WriteFile(unitPath, []byte(unit), 0644); err != nil {
			return "", err
		}
		return fmt.Sprintf("Service manifest registered at %s", unitPath), nil
	}

	// Prefer unified Windows Startup Folder shortcut launching aronium-utility-ui.exe (which starts tray + watcher)
	if uiBin := ResolveUIBinaryPath(m.BinaryPath); uiBin != "" {
		st, err := m.SetUIAutostart(true, uiBin)
		if err == nil {
			return fmt.Sprintf("Windows Startup Shortcut registered (%s)", st.ShortcutPath), nil
		}
	}

	// Fallback if UI binary isn't installed: register CLI watcher startup shortcut
	shortcutPath, shortcutErr := m.installStartupShortcut()
	binCmd := fmt.Sprintf(`"%s" watch --config "%s"`, m.BinaryPath, m.ConfigPath)

	scCmd := exec.Command("sc.exe", "create", ServiceName, "binPath=", binCmd, "start=", "auto", "DisplayName=", "Aronium Utility Sync Service")
	HideChildWindow(scCmd)
	if out, err := scCmd.CombinedOutput(); err == nil {
		return fmt.Sprintf("Windows Service '%s' & Startup Shortcut registered: %s", ServiceName, strings.TrimSpace(string(out))), nil
	}

	taskCmd := exec.Command("schtasks.exe", "/Create", "/F", "/SC", "ONLOGON", "/TN", TaskName, "/TR", binCmd)
	HideChildWindow(taskCmd)
	if out, err := taskCmd.CombinedOutput(); err == nil {
		return fmt.Sprintf("Windows Startup Task '%s' & Startup Shortcut registered: %s", TaskName, strings.TrimSpace(string(out))), nil
	}
	if shortcutErr != nil {
		return "", fmt.Errorf("failed to register Windows Startup Shortcut: %w", shortcutErr)
	}
	return fmt.Sprintf("Windows Startup Shortcut registered (%s)", shortcutPath), nil
}

// Start launches the background service or daemon process.
func (m *Manager) Start() (string, error) {
	if err := os.MkdirAll(filepath.Dir(m.LogPath), 0755); err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		scCmd := exec.Command("sc.exe", "start", ServiceName)
		HideChildWindow(scCmd)
		if out, err := scCmd.CombinedOutput(); err == nil {
			return strings.TrimSpace(string(out)), nil
		}
		tCmd := exec.Command("schtasks.exe", "/Run", "/TN", TaskName)
		HideChildWindow(tCmd)
		if out, err := tCmd.CombinedOutput(); err == nil {
			return strings.TrimSpace(string(out)), nil
		}
	}
	return fmt.Sprintf("Background watcher configured (log: %s)", m.LogPath), nil
}

// Stop stops the background service.
func (m *Manager) Stop() (string, error) {
	if runtime.GOOS == "windows" {
		scCmd := exec.Command("sc.exe", "stop", ServiceName)
		HideChildWindow(scCmd)
		if out, err := scCmd.CombinedOutput(); err == nil {
			return strings.TrimSpace(string(out)), nil
		}
		tCmd := exec.Command("schtasks.exe", "/End", "/TN", TaskName)
		HideChildWindow(tCmd)
		if out, err := tCmd.CombinedOutput(); err == nil {
			return strings.TrimSpace(string(out)), nil
		}
	}
	return "Service stop requested.", nil
}

func createWindowsStartupShortcut(shortcutPath, targetPath, args, description string) (string, error) {
	if shortcutPath == "" {
		return "", fmt.Errorf("APPDATA environment variable not set")
	}
	_ = os.MkdirAll(filepath.Dir(shortcutPath), 0o755)
	iconLoc := targetPath + ",0"
	icoCandidate := filepath.Join(filepath.Dir(targetPath), "app-icon.ico")
	if info, err := os.Stat(icoCandidate); err == nil && info.Size() > 64 {
		iconLoc = icoCandidate + ",0"
	}
	esc := func(s string) string { return strings.ReplaceAll(s, "'", "''") }
	winStyle := 1
	if strings.Contains(args, "watch") {
		winStyle = 7
	}
	psScript := fmt.Sprintf(
		"$ws = New-Object -ComObject WScript.Shell; "+
			"$s = $ws.CreateShortcut('%s'); "+
			"$s.TargetPath = '%s'; "+
			"$s.Arguments = '%s'; "+
			"$s.WorkingDirectory = '%s'; "+
			"$s.WindowStyle = %d; "+
			"$s.IconLocation = '%s'; "+
			"$s.Description = '%s'; "+
			"$s.Save()",
		esc(shortcutPath), esc(targetPath), esc(args), esc(filepath.Dir(targetPath)), winStyle, esc(iconLoc), esc(description),
	)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", psScript)
	HideChildWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	ieCmd := exec.Command("ie4uinit.exe", "-show")
	HideChildWindow(ieCmd)
	_ = ieCmd.Run()
	return shortcutPath, nil
}

func (m *Manager) installStartupShortcut() (string, error) {
	return createWindowsStartupShortcut(
		StartupShortcutPath(),
		m.BinaryPath,
		fmt.Sprintf(`watch --config "%s"`, m.ConfigPath),
		"Aronium Utility Background Watcher & Sync",
	)
}

func (m *Manager) RemoveStartupShortcut() error {
	shortcutPath := StartupShortcutPath()
	if shortcutPath == "" {
		return nil
	}
	if err := os.Remove(shortcutPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (m *Manager) Status() string {
	if runtime.GOOS == "windows" {
		if sp := UIAutostartShortcutPath(); sp != "" {
			if _, err := os.Stat(sp); err == nil {
				return fmt.Sprintf("Inicio automático activo (%s)", sp)
			}
		}
		scCmd := exec.Command("sc.exe", "query", ServiceName)
		HideChildWindow(scCmd)
		if out, err := scCmd.CombinedOutput(); err == nil {
			return strings.TrimSpace(string(out))
		}
		tCmd := exec.Command("schtasks.exe", "/Query", "/TN", TaskName)
		HideChildWindow(tCmd)
		if out, err := tCmd.CombinedOutput(); err == nil {
			return strings.TrimSpace(string(out))
		}
		if sp := StartupShortcutPath(); sp != "" {
			if _, err := os.Stat(sp); err == nil {
				return fmt.Sprintf("Windows Startup Shortcut active: %s", sp)
			}
		}
	}
	if m.ReadAutostartPreference() == "enabled" {
		return fmt.Sprintf("INSTALLED (autostart enabled, log: %s)", m.LogPath)
	}
	unitPath := filepath.Join(m.AppDir, "aronium-utility.service")
	if _, err := os.Stat(unitPath); err == nil {
		return fmt.Sprintf("INSTALLED (manifest: %s, log: %s)", unitPath, m.LogPath)
	}
	return "NOT_INSTALLED"
}

// Logs reads the tail of the service log file.
func (m *Manager) Logs(maxBytes int) (string, error) {
	if maxBytes <= 0 {
		maxBytes = 8192
	}
	data, err := os.ReadFile(m.LogPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "(Sin registros en service.log todavía)", nil
		}
		return "", err
	}
	if len(data) > maxBytes {
		data = data[len(data)-maxBytes:]
	}
	return config.RedactPII(string(data)), nil
}
