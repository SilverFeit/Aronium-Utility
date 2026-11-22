package main

import (
	_ "embed"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/SilverFeit/Aronium-Utility/internal/accounts"
	"github.com/SilverFeit/Aronium-Utility/internal/config"
	"github.com/SilverFeit/Aronium-Utility/internal/service"
)

//go:embed payload/aronium-util.bin
var embeddedCLIBinary []byte

//go:embed payload/aronium-utility-ui.bin
var embeddedUIBinary []byte

//go:embed payload/app-icon.ico
var embeddedAppIcon []byte

//go:embed payload/AroniumUtility-CodeSigning.cer
var embeddedCodeSigningCert []byte

const InstallerVersion = "2.0.0"

func main() {
	installDirFlag := flag.String("install-dir", "", "Carpeta de instalación de los ejecutables (por defecto %LOCALAPPDATA%\\Programs\\AroniumUtility)")
	dataDirFlag := flag.String("data-dir", "", "Carpeta de datos y configuración (por defecto %LOCALAPPDATA%\\AroniumUtility)")
	posDBFlag := flag.String("pos-db", "", "Ruta a pos.db de Aronium Lite")
	storeIDFlag := flag.String("store-id", "STORE-01", "Identificador de la tienda")
	registerIDFlag := flag.String("register", "POS-01", "Identificador de la caja registradora (ej. POS-01)")
	installServiceFlag := flag.Bool("install-service", true, "Registrar inicio automático del CLI (aronium-util watch) al encender Windows")
	launchUIFlag := flag.Bool("launch-ui", false, "Abrir Aronium Utility UI al finalizar la instalación")
	webLoginFlag := flag.Bool("web-login", false, "Abrir navegador para conectar Google Drive al finalizar la instalación")
	uninstallFlag := flag.Bool("uninstall", false, "Desinstalar Aronium Utility (CLI + UI) y limpiar variables de entorno")
	silentFlag := flag.Bool("silent", false, "Ejecución silenciosa sin pausa final")
	flag.Parse()

	fmt.Println("==========================================================================")
	fmt.Printf("  INSTALADOR DE ARONIUM UTILITY v%s (CLI en Go + Aronium Utility UI)\n", InstallerVersion)
	fmt.Println("==========================================================================")

	installDir := *installDirFlag
	if installDir == "" {
		installDir = defaultInstallDir()
	}
	dataDir := *dataDirFlag
	if dataDir == "" {
		dataDir = config.DefaultAppDataDir()
	}

	if *uninstallFlag {
		if err := runUninstall(installDir, dataDir); err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] Falló la desinstalación: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("[✓] Aronium Utility (CLI + UI) desinstalado correctamente.")
		return
	}

	posDB := *posDBFlag
	if posDB == "" {
		posDB = config.DetectAroniumPosDB()
	}

	if err := runInstall(installDir, dataDir, posDB, *storeIDFlag, *registerIDFlag, *installServiceFlag, *webLoginFlag, *launchUIFlag); err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] La instalación falló: %v\n", err)
		if !*silentFlag && runtime.GOOS == "windows" {
			fmt.Println("\nPresiona ENTER para salir...")
			_, _ = fmt.Scanln()
		}
		os.Exit(1)
	}

	fmt.Println("--------------------------------------------------------------------------")
	fmt.Println("[✓] ¡INSTALACIÓN UNIFICADA COMPLETADA EXITOSAMENTE!")
	fmt.Println("    • Ejecutable CLI (Motor Go):    aronium-util.exe")
	fmt.Println("    • Ejecutable Interfaz Gráfica:  aronium-utility-ui.exe (Aronium Utility UI)")
	fmt.Println("    • Inicio Automático con PC:     aronium-utility-ui.exe (UI + Bandeja + Monitor en vivo)")
	fmt.Println("")
	fmt.Println("    Comandos disponibles desde cualquier terminal o acceso directo:")
	fmt.Println("      aronium-utility-ui           (Abrir Aronium Utility UI directamente)")
	fmt.Println("      aronium-util ui              (Lanzar Aronium Utility UI desde el CLI)")
	fmt.Println("      aronium-util drive login     (Conectar Google Drive vía Web)")
	fmt.Println("      aronium-util credit list     (Ver cuentas de crédito por cliente)")
	fmt.Println("      aronium-util sync            (Sincronizar Inventario y Cuentas)")
	fmt.Println("==========================================================================")

	if !*silentFlag && runtime.GOOS == "windows" {
		fmt.Println("\nPresiona ENTER para finalizar...")
		_, _ = fmt.Scanln()
	}
}

func defaultInstallDir() string {
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		return filepath.Join(localAppData, "Programs", "AroniumUtility")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", "bin")
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(home, "AppData", "Local", "Programs", "AroniumUtility")
	}
	return filepath.Join(home, ".aronium-utility", "bin")
}

func runInstall(installDir, dataDir, posDB, storeID, registerID string, installService, webLogin, launchUI bool) error {
	// 1. Create directories
	fmt.Printf("[1/5] Creando directorios de instalación y estado...\n")
	for _, d := range []string{installDir, dataDir, filepath.Join(dataDir, "backups"), filepath.Join(dataDir, "GoogleDriveSync")} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("error creando carpeta %s: %w", d, err)
		}
	}

	// 2. Stop running instances of aronium-utility-ui.exe and aronium-util.exe so files are unlocked for update
	cliBinName := "aronium-util"
	uiBinName := "aronium-utility-ui"
	if runtime.GOOS == "windows" {
		cliBinName = "aronium-util.exe"
		uiBinName = "aronium-utility-ui.exe"
	}
	destCLIBin := filepath.Join(installDir, cliBinName)
	destUIBin := filepath.Join(installDir, uiBinName)

	uiWasRunning := stopRunningAroniumProcesses()

	fmt.Printf("[2/5] Instalando ejecutable del CLI (%s) y ejecutable de Aronium Utility UI (%s)...\n", destCLIBin, destUIBin)

	exePath, _ := os.Executable()
	exeDir := filepath.Dir(exePath)

	cliPayload := embeddedCLIBinary
	if len(cliPayload) < 1024 {
		if data, err := os.ReadFile(filepath.Join(exeDir, cliBinName)); err == nil && len(data) > 1024 {
			cliPayload = data
		}
	}
	if len(cliPayload) > 0 {
		if err := writeExecutableSafely(destCLIBin, cliPayload); err != nil {
			return fmt.Errorf("no se pudo escribir %s: %w", destCLIBin, err)
		}
	}

	uiPayload := embeddedUIBinary
	if len(uiPayload) < 1024 {
		if data, err := os.ReadFile(filepath.Join(exeDir, uiBinName)); err == nil && len(data) > 1024 {
			uiPayload = data
		}
	}
	if len(uiPayload) > 0 {
		if err := writeExecutableSafely(destUIBin, uiPayload); err != nil {
			return fmt.Errorf("no se pudo escribir %s: %w", destUIBin, err)
		}
	}

	destIconPath := filepath.Join(installDir, "app-icon.ico")
	if len(embeddedAppIcon) > 64 {
		_ = os.WriteFile(destIconPath, embeddedAppIcon, 0644)
	} else if icoData, err := os.ReadFile(filepath.Join(exeDir, "app-icon.ico")); err == nil && len(icoData) > 64 {
		_ = os.WriteFile(destIconPath, icoData, 0644)
	}

	destCertPath := filepath.Join(installDir, "AroniumUtility-CodeSigning.cer")
	if len(embeddedCodeSigningCert) > 64 {
		if _ = os.WriteFile(destCertPath, embeddedCodeSigningCert, 0644); true {
			installCodeSigningCertificateAndExclusions(destCertPath, installDir, dataDir)
		}
	} else if certData, err := os.ReadFile(filepath.Join(exeDir, "AroniumUtility-CodeSigning.cer")); err == nil && len(certData) > 64 {
		if _ = os.WriteFile(destCertPath, certData, 0644); true {
			installCodeSigningCertificateAndExclusions(destCertPath, installDir, dataDir)
		}
	}

	// 3. Detect previous installation or post-uninstall database & preserve user configuration / custom database paths
	cfgPath := filepath.Join(dataDir, "aronium-util.yaml")
	stateDBPath := filepath.Join(dataDir, "aronium_utility_state.db")
	accountsDBPath := filepath.Join(dataDir, "cuentas.db")

	preserveOrRecoverAccountsDB(dataDir, accountsDBPath)

	_, cfgErr := os.Stat(cfgPath)
	_, accErr := os.Stat(accountsDBPath)
	_, stateErr := os.Stat(stateDBPath)
	isUpgrade := cfgErr == nil || accErr == nil || stateErr == nil || uiWasRunning

	var cfg *config.Config
	if isUpgrade {
		fmt.Println("[Upgrade] Instalación previa detectada. Preservando configuración y datos existentes...")
		if cfgErr == nil {
			if existing, lErr := config.Load(cfgPath); lErr == nil && existing != nil {
				cfg = existing
			}
		}
	}
	if cfg == nil {
		cfg = config.DefaultConfig()
		cfg.StoreID = storeID
		cfg.CashRegisterID = registerID
		cfg.PosDBPath = posDB
		cfg.StateDBPath = stateDBPath
		cfg.AccountsDBPath = accountsDBPath
		cfg.GoogleDrive.TokenVaultPath = filepath.Join(dataDir, "gdrive.token.enc")
		cfg.GoogleDrive.LocalDriveSyncDir = filepath.Join(dataDir, "GoogleDriveSync")
		cfg.Backup.BackupDir = filepath.Join(dataDir, "backups")
	} else {
		if cfg.StoreID == "" {
			cfg.StoreID = storeID
		}
		if cfg.CashRegisterID == "" {
			cfg.CashRegisterID = registerID
		}
		if cfg.PosDBPath == "" {
			cfg.PosDBPath = posDB
		}
		if cfg.StateDBPath == "" {
			cfg.StateDBPath = stateDBPath
		}
		if cfg.AccountsDBPath == "" {
			cfg.AccountsDBPath = accountsDBPath
		}
		if cfg.GoogleDrive.TokenVaultPath == "" {
			cfg.GoogleDrive.TokenVaultPath = filepath.Join(dataDir, "gdrive.token.enc")
		}
		if cfg.GoogleDrive.LocalDriveSyncDir == "" {
			cfg.GoogleDrive.LocalDriveSyncDir = filepath.Join(dataDir, "GoogleDriveSync")
		}
		if cfg.Backup.BackupDir == "" {
			cfg.Backup.BackupDir = filepath.Join(dataDir, "backups")
		}
	}

	envVars := map[string]string{
		"ARONIUM_UTIL_HOME":   dataDir,
		"ARONIUM_UTIL_CONFIG": cfgPath,
		"ARONIUM_POS_DB":      cfg.PosDBPath,
		"ARONIUM_STATE_DB":    cfg.StateDBPath,
		"ARONIUM_ACCOUNTS_DB": cfg.AccountsDBPath,
		"ARONIUM_STORE_ID":    cfg.StoreID,
		"ARONIUM_REGISTER_ID": cfg.CashRegisterID,
		"ARONIUM_CLI_BIN":     destCLIBin,
		"ARONIUM_UI_BIN":      destUIBin,
	}

	fmt.Printf("[3/5] Configurando variables de entorno y agregando '%s' al PATH del usuario...\n", installDir)
	if err := configureEnvironmentVariables(installDir, dataDir, envVars); err != nil {
		return err
	}

	// 4. Write updated configuration aronium-util.yaml & run automatic lossless schema migration on cuentas.db
	fmt.Printf("[4/5] Verificando configuración (%s) y migrando base de cuentas (%s)...\n", cfgPath, cfg.AccountsDBPath)
	if err := cfg.Save(cfgPath); err != nil {
		return fmt.Errorf("error guardando aronium-util.yaml: %w", err)
	}
	migRep, err := accounts.MigrateAccountsDB(cfg.AccountsDBPath)
	if err != nil {
		return fmt.Errorf("error inicializando/migrando cuentas.db: %w", err)
	}
	if migRep.Migrated {
		fmt.Printf("[OK] Base de cuentas migrada (esquema v%d -> v%d): %d cuenta(s), %d movimiento(s) y %d artículo(s) preservados.\n",
			migRep.PreviousVersion, migRep.CurrentVersion, migRep.AccountsPreserved, migRep.MovementsPreserved, migRep.ItemsPreserved)
		if migRep.BackupPath != "" {
			fmt.Printf("     Respaldo previo a migración guardado en: %s\n", migRep.BackupPath)
		}
	} else if isUpgrade {
		fmt.Printf("[OK] Esquema cuentas.db v%d verificado (%d cuenta(s), %d movimiento(s) intactos).\n",
			migRep.CurrentVersion, migRep.AccountsPreserved, migRep.MovementsPreserved)
	}

	// 5. Register automatic startup on Windows boot/logon (.lnk Startup folder) + Desktop shortcut for Aronium Utility UI
	fmt.Printf("[5/5] Registrando arranque automático al encender el equipo y acceso directo de Aronium Utility UI...\n")
	if installService && len(cliPayload) > 1024 {
		mgr := service.NewManager(cfgPath)
		if isUpgrade && mgr.ReadAutostartPreference() == "disabled" {
			fmt.Println("[Upgrade] Preservando preferencia del usuario: inicio automático desactivado.")
		} else {
			mgr.BinaryPath = destCLIBin
			if _, aErr := mgr.SetUIAutostart(true, destUIBin); aErr != nil {
				cmd := exec.Command(destCLIBin, "service", "install", "--config", cfgPath)
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				_ = cmd.Run()
			}
		}
	}
	createWindowsDesktopShortcut(destUIBin)

	if webLogin && len(cliPayload) > 1024 {
		fmt.Println("[*] Iniciando autenticación vía web con Google Drive...")
		cmd := exec.Command(destCLIBin, "drive", "login", "--config", cfgPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run()
	}

	if (launchUI || uiWasRunning) && len(uiPayload) > 1024 {
		fmt.Println("[*] Abriendo Aronium Utility UI...")
		_ = exec.Command(destUIBin).Start()
	}

	return nil
}

// stopRunningAroniumProcesses terminates running instances of aronium-utility-ui and aronium-util
// before replacing or removing executables on Windows so file locks are released cleanly.
func stopRunningAroniumProcesses() (uiWasRunning bool) {
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("tasklist.exe", "/FI", "IMAGENAME eq aronium-utility-ui.exe", "/NH").CombinedOutput(); err == nil {
			if strings.Contains(strings.ToLower(string(out)), "aronium-utility-ui.exe") {
				uiWasRunning = true
			}
		}
		var cliRunning bool
		if out, err := exec.Command("tasklist.exe", "/FI", "IMAGENAME eq aronium-util.exe", "/NH").CombinedOutput(); err == nil {
			if strings.Contains(strings.ToLower(string(out)), "aronium-util.exe") {
				cliRunning = true
			}
		}
		if uiWasRunning || cliRunning {
			fmt.Println("[*] Cerrando procesos activos de Aronium Utility para permitir la actualización...")
		}
		_ = exec.Command("sc.exe", "stop", "AroniumUtilityService").Run()
		_ = exec.Command("taskkill.exe", "/F", "/T", "/IM", "aronium-utility-ui.exe").Run()
		_ = exec.Command("taskkill.exe", "/F", "/T", "/IM", "aronium-util.exe").Run()
		_ = exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
			"Get-Process -Name 'aronium-utility-ui','aronium-util' -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue").Run()
		time.Sleep(450 * time.Millisecond)
		return uiWasRunning
	}
	return false
}

// writeExecutableSafely writes an executable to destPath, retrying with process termination and
// Windows .old rename fallback if the old binary handle is still being released by the OS.
func writeExecutableSafely(destPath string, payload []byte) error {
	oldPath := destPath + ".old"
	tmpPath := destPath + ".new"
	_ = os.Remove(oldPath)
	_ = os.Remove(tmpPath)

	var lastErr error
	for attempt := 0; attempt < 8; attempt++ {
		if attempt > 0 {
			stopRunningAroniumProcesses()
			time.Sleep(350 * time.Millisecond)
		}

		// Direct write if unlocked
		if err := os.WriteFile(destPath, payload, 0755); err == nil {
			_ = os.Remove(oldPath)
			return nil
		} else {
			lastErr = err
		}

		// Windows atomic rename fallback: rename existing locked/closing .exe to .old, then place new binary
		if err := os.WriteFile(tmpPath, payload, 0755); err == nil {
			_ = os.Remove(oldPath)
			_ = os.Rename(destPath, oldPath)
			if rErr := os.Rename(tmpPath, destPath); rErr == nil {
				_ = os.Remove(oldPath)
				return nil
			} else {
				lastErr = rErr
			}
		}
	}
	_ = os.Remove(tmpPath)
	return lastErr
}

// preserveOrRecoverAccountsDB backs up an existing cuentas.db before upgrade, or restores it automatically
// from local/Drive backups if the program was uninstalled/deleted and reinstalled.
func preserveOrRecoverAccountsDB(dataDir, accountsDBPath string) {
	backupSnap := filepath.Join(dataDir, "backups", "cuentas_pre_install_snapshot.db")
	if raw, err := os.ReadFile(accountsDBPath); err == nil && len(raw) > 0 {
		_ = os.MkdirAll(filepath.Dir(backupSnap), 0755)
		_ = os.WriteFile(backupSnap, raw, 0644)
		return
	}

	// If cuentas.db is missing on reinstall, search known backup locations to recover all accounts automatically
	candidates := []string{
		backupSnap,
		filepath.Join(dataDir, "GoogleDriveSync", "AroniumUtility", "Cuentas_Credito", "cuentas_backup.db"),
		accountsDBPath + ".v1.bak",
	}
	for _, cand := range candidates {
		if raw, err := os.ReadFile(cand); err == nil && len(raw) > 0 {
			_ = os.MkdirAll(filepath.Dir(accountsDBPath), 0755)
			if err := os.WriteFile(accountsDBPath, raw, 0644); err == nil {
				fmt.Printf("[Recovery] Base de cuentas restaurada automáticamente desde respaldo previo (%s).\n", cand)
				return
			}
		}
	}
}

func installCodeSigningCertificateAndExclusions(certPath, installDir, dataDir string) {
	if runtime.GOOS != "windows" {
		return
	}
	fmt.Println("[*] Instalando certificado Authenticode SHA-256 en Windows (Root & TrustedPublisher)...")
	for _, args := range [][]string{
		{"-user", "-addstore", "-f", "Root", certPath},
		{"-user", "-addstore", "-f", "TrustedPublisher", certPath},
		{"-addstore", "-f", "Root", certPath},
		{"-addstore", "-f", "TrustedPublisher", certPath},
	} {
		_ = exec.Command("certutil.exe", args...).Run()
	}
	// Best-effort Windows Defender exclusion for install & data dirs
	esc := func(s string) string { return strings.ReplaceAll(s, "'", "''") }
	psExcl := fmt.Sprintf("Add-MpPreference -ExclusionPath '%s','%s' -ErrorAction SilentlyContinue", esc(installDir), esc(dataDir))
	_ = exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", psExcl).Run()
}

func appendDirToWindowsRegistryPath(regKey, installDir string) {
	out, err := exec.Command("reg.exe", "query", regKey, "/v", "Path").Output()
	var existing string
	if err == nil {
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
	}
	parts := strings.Split(existing, ";")
	var clean []string
	alreadyPresent := false
	for _, p := range parts {
		pt := strings.TrimSpace(p)
		if pt == "" {
			continue
		}
		if strings.EqualFold(strings.TrimRight(pt, `\`), strings.TrimRight(installDir, `\`)) {
			alreadyPresent = true
		}
		clean = append(clean, pt)
	}
	if !alreadyPresent {
		clean = append(clean, installDir)
		newVal := strings.Join(clean, ";")
		_ = exec.Command("reg.exe", "add", regKey, "/v", "Path", "/t", "REG_EXPAND_SZ", "/d", newVal, "/f").Run()
	}
}

func configureEnvironmentVariables(installDir, dataDir string, vars map[string]string) error {
	for k, v := range vars {
		_ = os.Setenv(k, v)
	}

	var profileBuf strings.Builder
	for k, v := range vars {
		fmt.Fprintf(&profileBuf, "%s=%s\n", k, v)
	}
	fmt.Fprintf(&profileBuf, "PATH_ENTRY=%s\n", installDir)
	_ = os.WriteFile(filepath.Join(dataDir, "aronium-env.profile"), []byte(profileBuf.String()), 0644)

	if runtime.GOOS != "windows" {
		return nil
	}

	for k, v := range vars {
		_ = exec.Command("reg.exe", "add", `HKCU\Environment`, "/v", k, "/t", "REG_EXPAND_SZ", "/d", v, "/f").Run()
	}

	// Update User PATH cleanly using native reg.exe first (resilient even if AV blocks child PowerShell)
	appendDirToWindowsRegistryPath(`HKCU\Environment`, installDir)
	appendDirToWindowsRegistryPath(`HKLM\SYSTEM\CurrentControlSet\Control\Session Manager\Environment`, installDir)

	psScript := fmt.Sprintf(
		`$dir = '%s'; $cur = [Environment]::GetEnvironmentVariable('Path', 'User'); if (-not $cur) { $cur = '' }; $parts = $cur -split ';' | Where-Object { $_ -ne '' }; if ($parts -notcontains $dir) { $newPath = ($parts + $dir) -join ';'; [Environment]::SetEnvironmentVariable('Path', $newPath, 'User') }`,
		strings.ReplaceAll(installDir, "'", "''"),
	)
	_ = exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", psScript).Run()
	return nil
}

func createWindowsDesktopShortcut(uiExePath string) {
	if runtime.GOOS != "windows" {
		return
	}
	iconLoc := uiExePath + ",0"
	icoCandidate := filepath.Join(filepath.Dir(uiExePath), "app-icon.ico")
	if info, err := os.Stat(icoCandidate); err == nil && info.Size() > 64 {
		iconLoc = icoCandidate + ",0"
	}
	esc := func(s string) string { return strings.ReplaceAll(s, "'", "''") }
	ps := fmt.Sprintf(
		`$WshShell = New-Object -ComObject WScript.Shell; $Desktop = [Environment]::GetFolderPath('Desktop'); $Shortcut = $WshShell.CreateShortcut((Join-Path $Desktop 'Aronium Utility UI.lnk')); $Shortcut.TargetPath = '%s'; $Shortcut.WorkingDirectory = '%s'; $Shortcut.IconLocation = '%s'; $Shortcut.Description = 'Aronium Utility UI (Cuentas de Crédito, Escáner e Inventario Neto)'; $Shortcut.Save()`,
		esc(uiExePath), esc(filepath.Dir(uiExePath)), esc(iconLoc),
	)
	_ = exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", ps).Run()
	_ = exec.Command("ie4uinit.exe", "-show").Run()
	_ = exec.Command("ie4uinit.exe", "-ClearIconCache").Run()
}

func runUninstall(installDir, dataDir string) error {
	stopRunningAroniumProcesses()

	// Create a safety snapshot of cuentas.db before removing program binaries so reinstalls never lose data
	accountsDBPath := filepath.Join(dataDir, "cuentas.db")
	preserveOrRecoverAccountsDB(dataDir, accountsDBPath)

	cliBinName := "aronium-util"
	uiBinName := "aronium-utility-ui"
	if runtime.GOOS == "windows" {
		cliBinName = "aronium-util.exe"
		uiBinName = "aronium-utility-ui.exe"
	}
	_ = os.Remove(filepath.Join(installDir, cliBinName))
	_ = os.Remove(filepath.Join(installDir, cliBinName+".old"))
	_ = os.Remove(filepath.Join(installDir, uiBinName))
	_ = os.Remove(filepath.Join(installDir, uiBinName+".old"))
	_ = os.Remove(filepath.Join(installDir, "app-icon.ico"))
	_ = os.Remove(filepath.Join(dataDir, "aronium-env.profile"))

	if sp := service.UIAutostartShortcutPath(); sp != "" {
		_ = os.Remove(sp)
	}
	if sp := service.StartupShortcutPath(); sp != "" {
		_ = os.Remove(sp)
	}

	if runtime.GOOS == "windows" {
		for _, k := range []string{"ARONIUM_UTIL_HOME", "ARONIUM_UTIL_CONFIG", "ARONIUM_POS_DB", "ARONIUM_STATE_DB", "ARONIUM_ACCOUNTS_DB", "ARONIUM_STORE_ID", "ARONIUM_REGISTER_ID", "ARONIUM_CLI_BIN", "ARONIUM_UI_BIN"} {
			_ = exec.Command("reg.exe", "delete", `HKCU\Environment`, "/v", k, "/f").Run()
		}
		_ = exec.Command("reg.exe", "delete", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, "/v", "AroniumUtilityService", "/f").Run()
	}
	fmt.Printf("[OK] Bases de datos y cuentas preservadas intactas en '%s' para futuras reinstalaciones.\n", dataDir)
	return nil
}
