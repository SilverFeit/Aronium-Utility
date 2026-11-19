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

const InstallerVersion = "1.0.0"

func main() {
	installDirFlag := flag.String("install-dir", "", "Carpeta de instalación del binario (por defecto %LOCALAPPDATA%\\Programs\\AroniumUtility)")
	dataDirFlag := flag.String("data-dir", "", "Carpeta de datos y configuración (por defecto %LOCALAPPDATA%\\AroniumUtility)")
	posDBFlag := flag.String("pos-db", "", "Ruta a pos.db de Aronium Lite")
	storeIDFlag := flag.String("store-id", "TIENDA-01", "Identificador de la tienda")
	registerIDFlag := flag.String("register", "POS-01", "Identificador de la caja registradora (ej. POS-01)")
	installServiceFlag := flag.Bool("install-service", false, "Registrar tarea/servicio de sincronización automática al iniciar Windows")
	webLoginFlag := flag.Bool("web-login", false, "Abrir navegador para conectar Google Drive al finalizar la instalación")
	uninstallFlag := flag.Bool("uninstall", false, "Desinstalar Aronium Utility y limpiar variables de entorno")
	silentFlag := flag.Bool("silent", false, "Ejecución silenciosa sin pausa final")
	flag.Parse()

	fmt.Println("======================================================================")
	fmt.Printf("  INSTALADOR DE ARONIUM UTILITY v%s (Windows CLI + Variables Entorno)\n", InstallerVersion)
	fmt.Println("======================================================================")

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
		fmt.Println("[✓] Aronium Utility desinstalado correctamente.")
		return
	}

	posDB := *posDBFlag
	if posDB == "" {
		posDB = config.DetectAroniumPosDB()
	}

	if err := runInstall(installDir, dataDir, posDB, *storeIDFlag, *registerIDFlag, *installServiceFlag, *webLoginFlag); err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] La instalación falló: %v\n", err)
		if !*silentFlag && runtime.GOOS == "windows" {
			fmt.Println("\nPresiona ENTER para salir...")
			_, _ = fmt.Scanln()
		}
		os.Exit(1)
	}

	fmt.Println("----------------------------------------------------------------------")
	fmt.Println("[✓] ¡INSTALACIÓN COMPLETADA EXITOSAMENTE!")
	fmt.Println("    Ya puedes abrir cualquier terminal (CMD o PowerShell) y ejecutar:")
	fmt.Println("      aronium-util doctor")
	fmt.Println("      aronium-util drive login     (Conectar Google Drive vía Web)")
	fmt.Println("      aronium-util sync            (Sincronizar Inventario y Fiados)")
	fmt.Println("      aronium-util fiados list     (Ver cuentas de fiado por cliente)")
	fmt.Println("      aronium-util watch           (Sincronización continua en paralelo)")
	fmt.Println("======================================================================")

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

func runInstall(installDir, dataDir, posDB, storeID, registerID string, installService, webLogin bool) error {
	// 1. Create directories
	fmt.Printf("[1/4] Creando directorios de instalación y estado...\n")
	for _, d := range []string{installDir, dataDir, filepath.Join(dataDir, "backups"), filepath.Join(dataDir, "GoogleDriveSync")} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("error creando carpeta %s: %w", d, err)
		}
	}

	// 2. Extract or copy aronium-util.exe
	binName := "aronium-util"
	if runtime.GOOS == "windows" {
		binName = "aronium-util.exe"
	}
	destBin := filepath.Join(installDir, binName)
	fmt.Printf("[2/4] Instalando binario CLI en: %s\n", destBin)

	payload := embeddedCLIBinary
	// Fallback to sibling binary if embedded placeholder is minimal
	if len(payload) < 1024 {
		exePath, _ := os.Executable()
		sibling := filepath.Join(filepath.Dir(exePath), binName)
		if data, err := os.ReadFile(sibling); err == nil && len(data) > 1024 {
			payload = data
		}
	}
	if len(payload) > 0 {
		if err := os.WriteFile(destBin, payload, 0755); err != nil {
			return fmt.Errorf("no se pudo escribir %s: %w", destBin, err)
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
	}

	fmt.Printf("[3/4] Configurando variables de entorno y agregando '%s' al PATH del usuario...\n", installDir)
	if err := configureEnvironmentVariables(installDir, dataDir, envVars); err != nil {
		return err
	}

	// 4. Write default configuration file aronium-util.yaml
	fmt.Printf("[4/4] Generando configuración declarativa inicial (%s)...\n", cfgPath)
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

	if installService && len(payload) > 1024 {
		fmt.Println("[*] Registrando servicio en segundo plano de Windows...")
		cmd := exec.Command(destBin, "service", "install", "--config", cfgPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run()
	}

	if webLogin && len(payload) > 1024 {
		fmt.Println("[*] Iniciando autenticación vía web con Google Drive...")
		cmd := exec.Command(destBin, "drive", "login", "--config", cfgPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run()
	}

	return nil
}

func configureEnvironmentVariables(installDir, dataDir string, vars map[string]string) error {
	for k, v := range vars {
		_ = os.Setenv(k, v)
	}

	// Always write a portable env profile file for verification and shell sourcing
	var profileBuf strings.Builder
	for k, v := range vars {
		fmt.Fprintf(&profileBuf, "%s=%s\n", k, v)
	}
	fmt.Fprintf(&profileBuf, "PATH_ENTRY=%s\n", installDir)
	_ = os.WriteFile(filepath.Join(dataDir, "aronium-env.profile"), []byte(profileBuf.String()), 0644)

	if runtime.GOOS != "windows" {
		return nil
	}

	// On Windows: Persist User environment variables in HKCU\Environment and update User PATH cleanly via PowerShell / reg.exe
	for k, v := range vars {
		_ = exec.Command("reg.exe", "add", `HKCU\Environment`, "/v", k, "/t", "REG_EXPAND_SZ", "/d", v, "/f").Run()
	}

	psScript := fmt.Sprintf(
		`$dir = '%s'; $cur = [Environment]::GetEnvironmentVariable('Path', 'User'); if (-not $cur) { $cur = '' }; $parts = $cur -split ';' | Where-Object { $_ -ne '' }; if ($parts -notcontains $dir) { $newPath = ($parts + $dir) -join ';'; [Environment]::SetEnvironmentVariable('Path', $newPath, 'User') }`,
		strings.ReplaceAll(installDir, "'", "''"),
	)
	if err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", psScript).Run(); err != nil {
		// Fallback to setx if PowerShell is restricted
		curPath := os.Getenv("PATH")
		if !strings.Contains(strings.ToLower(curPath), strings.ToLower(installDir)) {
			_ = exec.Command("setx.exe", "PATH", curPath+";"+installDir).Run()
		}
	}
	return nil
}

func runUninstall(installDir, dataDir string) error {
	binName := "aronium-util"
	if runtime.GOOS == "windows" {
		binName = "aronium-util.exe"
	}
	_ = os.Remove(filepath.Join(installDir, binName))
	_ = os.Remove(filepath.Join(dataDir, "aronium-env.profile"))

	if runtime.GOOS == "windows" {
		for _, k := range []string{"ARONIUM_UTIL_HOME", "ARONIUM_UTIL_CONFIG", "ARONIUM_POS_DB", "ARONIUM_STATE_DB", "ARONIUM_STORE_ID", "ARONIUM_REGISTER_ID"} {
			_ = exec.Command("reg.exe", "delete", `HKCU\Environment`, "/v", k, "/f").Run()
		}
	}
	return nil
}
