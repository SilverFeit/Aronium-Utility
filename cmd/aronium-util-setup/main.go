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

	"github.com/SilverFeit/Aronium-Utility/internal/config"
)

//go:embed payload/aronium-util.bin
var embeddedCLIBinary []byte

//go:embed payload/aronium-utility-ui.bin
var embeddedUIBinary []byte

const InstallerVersion = "1.1.0"

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
	fmt.Println("    • Inicio Automático con PC:     aronium-util.exe watch (Activo al encender)")
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

	// 2. Extract or copy BOTH executables: aronium-util.exe (CLI) and aronium-utility-ui.exe (Desktop UI)
	cliBinName := "aronium-util"
	uiBinName := "aronium-utility-ui"
	if runtime.GOOS == "windows" {
		cliBinName = "aronium-util.exe"
		uiBinName = "aronium-utility-ui.exe"
	}
	destCLIBin := filepath.Join(installDir, cliBinName)
	destUIBin := filepath.Join(installDir, uiBinName)

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
		if err := os.WriteFile(destCLIBin, cliPayload, 0755); err != nil {
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
		if err := os.WriteFile(destUIBin, uiPayload, 0755); err != nil {
			return fmt.Errorf("no se pudo escribir %s: %w", destUIBin, err)
		}
	}

	// 3. Configure environment variables & User PATH
	cfgPath := filepath.Join(dataDir, "aronium-util.yaml")
	stateDBPath := filepath.Join(dataDir, "aronium_utility_state.db")
	envVars := map[string]string{
		"ARONIUM_UTIL_HOME":   dataDir,
		"ARONIUM_UTIL_CONFIG": cfgPath,
		"ARONIUM_POS_DB":      posDB,
		"ARONIUM_STATE_DB":    stateDBPath,
		"ARONIUM_STORE_ID":    storeID,
		"ARONIUM_REGISTER_ID": registerID,
		"ARONIUM_CLI_BIN":     destCLIBin,
		"ARONIUM_UI_BIN":      destUIBin,
	}

	fmt.Printf("[3/5] Configurando variables de entorno y agregando '%s' al PATH del usuario...\n", installDir)
	if err := configureEnvironmentVariables(installDir, dataDir, envVars); err != nil {
		return err
	}

	// 4. Write default configuration file aronium-util.yaml
	fmt.Printf("[4/5] Generando configuración declarativa inicial (%s)...\n", cfgPath)
	cfg := config.DefaultConfig()
	cfg.StoreID = storeID
	cfg.CashRegisterID = registerID
	cfg.PosDBPath = posDB
	cfg.StateDBPath = stateDBPath
	cfg.GoogleDrive.TokenVaultPath = filepath.Join(dataDir, "gdrive.token.enc")
	cfg.GoogleDrive.LocalDriveSyncDir = filepath.Join(dataDir, "GoogleDriveSync")
	cfg.Backup.BackupDir = filepath.Join(dataDir, "backups")
	if err := cfg.Save(cfgPath); err != nil {
		return fmt.Errorf("error guardando aronium-util.yaml: %w", err)
	}

	// 5. Register automatic startup on Windows boot/logon + Desktop shortcut for Aronium Utility UI
	fmt.Printf("[5/5] Registrando arranque automático del CLI al encender el equipo y acceso directo de Aronium Utility UI...\n")
	if installService && len(cliPayload) > 1024 {
		cmd := exec.Command(destCLIBin, "service", "install", "--config", cfgPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run()
	}
	createWindowsDesktopShortcut(destUIBin)

	if webLogin && len(cliPayload) > 1024 {
		fmt.Println("[*] Iniciando autenticación vía web con Google Drive...")
		cmd := exec.Command(destCLIBin, "drive", "login", "--config", cfgPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run()
	}

	if launchUI && len(uiPayload) > 1024 {
		fmt.Println("[*] Abriendo Aronium Utility UI...")
		_ = exec.Command(destUIBin).Start()
	}

	return nil
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

	psScript := fmt.Sprintf(
		`$dir = '%s'; $cur = [Environment]::GetEnvironmentVariable('Path', 'User'); if (-not $cur) { $cur = '' }; $parts = $cur -split ';' | Where-Object { $_ -ne '' }; if ($parts -notcontains $dir) { $newPath = ($parts + $dir) -join ';'; [Environment]::SetEnvironmentVariable('Path', $newPath, 'User') }`,
		strings.ReplaceAll(installDir, "'", "''"),
	)
	if err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", psScript).Run(); err != nil {
		curPath := os.Getenv("PATH")
		if !strings.Contains(strings.ToLower(curPath), strings.ToLower(installDir)) {
			_ = exec.Command("setx.exe", "PATH", curPath+";"+installDir).Run()
		}
	}
	return nil
}

func createWindowsDesktopShortcut(uiExePath string) {
	if runtime.GOOS != "windows" {
		return
	}
	ps := fmt.Sprintf(
		`$WshShell = New-Object -ComObject WScript.Shell; $Desktop = [Environment]::GetFolderPath('Desktop'); $Shortcut = $WshShell.CreateShortcut((Join-Path $Desktop 'Aronium Utility UI.lnk')); $Shortcut.TargetPath = '%s'; $Shortcut.Description = 'Aronium Utility UI (Tauri 2 + Svelte + Go CLI)'; $Shortcut.Save()`,
		strings.ReplaceAll(uiExePath, "'", "''"),
	)
	_ = exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", ps).Run()
}

func runUninstall(installDir, dataDir string) error {
	cliBinName := "aronium-util"
	uiBinName := "aronium-utility-ui"
	if runtime.GOOS == "windows" {
		cliBinName = "aronium-util.exe"
		uiBinName = "aronium-utility-ui.exe"
	}
	_ = os.Remove(filepath.Join(installDir, cliBinName))
	_ = os.Remove(filepath.Join(installDir, uiBinName))
	_ = os.Remove(filepath.Join(dataDir, "aronium-env.profile"))

	if runtime.GOOS == "windows" {
		for _, k := range []string{"ARONIUM_UTIL_HOME", "ARONIUM_UTIL_CONFIG", "ARONIUM_POS_DB", "ARONIUM_STATE_DB", "ARONIUM_STORE_ID", "ARONIUM_REGISTER_ID", "ARONIUM_CLI_BIN", "ARONIUM_UI_BIN"} {
			_ = exec.Command("reg.exe", "delete", `HKCU\Environment`, "/v", k, "/f").Run()
		}
		_ = exec.Command("reg.exe", "delete", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, "/v", "AroniumUtilityService", "/f").Run()
	}
	return nil
}
