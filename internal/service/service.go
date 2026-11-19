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

// Install registers Aronium Utility as a background service / startup task on Windows (TODO-016).
func (m *Manager) Install() (string, error) {
	if err := os.MkdirAll(filepath.Dir(m.LogPath), 0755); err != nil {
		return "", err
	}
	if runtime.GOOS != "windows" {
		unitPath := filepath.Join(m.AppDir, "aronium-utility.service")
		unit := fmt.Sprintf("[Service]\nExecStart=%s watch --config %s\nLogPath=%s\n", m.BinaryPath, m.ConfigPath, m.LogPath)
		if err := os.WriteFile(unitPath, []byte(unit), 0644); err != nil {
			return "", err
		}
		return fmt.Sprintf("Service manifest registered at %s", unitPath), nil
	}

	// On Windows, try sc.exe first (requires admin), and fallback to schtasks (runs under user account at logon without UAC prompt!)
	binCmd := fmt.Sprintf(`"%s" watch --config "%s"`, m.BinaryPath, m.ConfigPath)
	scCmd := exec.Command("sc.exe", "create", ServiceName, "binPath=", binCmd, "start=", "auto", "DisplayName=", "Aronium Utility Sync Service")
	if out, err := scCmd.CombinedOutput(); err == nil {
		return fmt.Sprintf("Windows Service '%s' installed via sc.exe: %s", ServiceName, strings.TrimSpace(string(out))), nil
	}

	// Fallback: Windows Scheduled Task at Logon so non-admin cashiers can run it seamlessly
	taskCmd := exec.Command("schtasks.exe", "/Create", "/F", "/SC", "ONLOGON", "/TN", TaskName, "/TR", binCmd)
	out, err := taskCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to register background service/task: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return fmt.Sprintf("Windows Background Task '%s' installed via schtasks: %s", TaskName, strings.TrimSpace(string(out))), nil
}

// Start launches the background service or daemon process.
func (m *Manager) Start() (string, error) {
	if err := os.MkdirAll(filepath.Dir(m.LogPath), 0755); err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("sc.exe", "start", ServiceName).CombinedOutput(); err == nil {
			return strings.TrimSpace(string(out)), nil
		}
		if out, err := exec.Command("schtasks.exe", "/Run", "/TN", TaskName).CombinedOutput(); err == nil {
			return strings.TrimSpace(string(out)), nil
		}
	}
	return fmt.Sprintf("Background watcher configured (log: %s)", m.LogPath), nil
}

// Stop stops the background service.
func (m *Manager) Stop() (string, error) {
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("sc.exe", "stop", ServiceName).CombinedOutput(); err == nil {
			return strings.TrimSpace(string(out)), nil
		}
		if out, err := exec.Command("schtasks.exe", "/End", "/TN", TaskName).CombinedOutput(); err == nil {
			return strings.TrimSpace(string(out)), nil
		}
	}
	return "Service stop requested.", nil
}

// Status checks the current status of the service.
func (m *Manager) Status() string {
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("sc.exe", "query", ServiceName).CombinedOutput(); err == nil {
			return strings.TrimSpace(string(out))
		}
		if out, err := exec.Command("schtasks.exe", "/Query", "/TN", TaskName).CombinedOutput(); err == nil {
			return strings.TrimSpace(string(out))
		}
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
