package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/SilverFeit/Aronium-Utility/internal/config"
	"github.com/SilverFeit/Aronium-Utility/internal/service"
)

const UninstallerVersion = "2.0.3"

func main() {
	installDirFlag := flag.String("install-dir", "", "Carpeta de instalación (%LOCALAPPDATA%\\Programs\\AroniumUtility)")
	dataDirFlag := flag.String("data-dir", "", "Carpeta de datos y configuración (%LOCALAPPDATA%\\AroniumUtility)")
	accountsDBFlag := flag.String("accounts-db", "", "Ruta explícita opcional a cuentas.db")
	silentFlag := flag.Bool("silent", false, "Ejecutar desinstalación sin confirmación ni pausa final")
	yesFlag := flag.Bool("yes", false, "Confirmar automáticamente sin preguntar")
	flag.Parse()

	installDir := *installDirFlag
	if installDir == "" {
		installDir = defaultInstallDir()
	}
	dataDir := *dataDirFlag
	if dataDir == "" {
		dataDir = config.DefaultAppDataDir()
	}

	fmt.Println("==========================================================================")
	fmt.Printf("  DESINSTALADOR COMPLETO DE ARONIUM UTILITY v%s (LIMPIEZA 100%%)\n", UninstallerVersion)
	fmt.Println("==========================================================================")
	fmt.Println("Este asistente:")
	fmt.Println("  1. Detendrá y eliminará todos los procesos, servicios y tareas programadas")
	fmt.Println("     de Aronium Utility.")
	fmt.Printf("  2. Guardará una copia segura de tu base de cuentas (cuentas.db) en:\n     %s\n", service.DocumentsBackupDir())
	fmt.Printf("  3. Eliminará al 100%% las carpetas de programa y configuración:\n     • %s\n     • %s\n", installDir, dataDir)
	fmt.Println("  4. Limpiará variables de entorno ARONIUM_*, accesos directos y PATH.")
	fmt.Println("--------------------------------------------------------------------------")

	if !*silentFlag && !*yesFlag {
		fmt.Print("¿Deseas continuar con la desinstalación completa ahora? [S/n]: ")
		reader := bufio.NewReader(os.Stdin)
		ans, _ := reader.ReadString('\n')
		ans = strings.TrimSpace(strings.ToLower(ans))
		if ans != "" && ans != "s" && ans != "si" && ans != "sí" && ans != "y" && ans != "yes" {
			fmt.Println("Desinstalación cancelada. No se modificó ningún archivo.")
			return
		}
	}

	rep, err := service.RunCompleteUninstall(installDir, dataDir, *accountsDBFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Falló la desinstalación: %v\n", err)
		if !*silentFlag && runtime.GOOS == "windows" {
			fmt.Println("\nPresiona ENTER para salir...")
			_, _ = fmt.Scanln()
		}
		os.Exit(1)
	}

	fmt.Println("--------------------------------------------------------------------------")
	if rep.AccountsBackedUp {
		fmt.Printf("[✓] Copia de seguridad de cuentas.db guardada en Mis Documentos:\n")
		fmt.Printf("    • Copia principal:   %s\n", rep.AccountsBackupPath)
		fmt.Printf("    • Copia con fecha:   %s\n", rep.AccountsTimestampPath)
	} else {
		fmt.Println("[i] No se encontró ningún archivo cuentas.db previo que respaldar.")
	}
	fmt.Println("[✓] Procesos, servicios, tareas programadas, accesos directos y PATH limpiados.")
	fmt.Println("[✓] Carpetas del sistema eliminadas al 100%.")
	for _, w := range rep.Warnings {
		fmt.Printf("[*] Nota: %s\n", w)
	}
	fmt.Println("==========================================================================")
	fmt.Println("  Reinstalación recomendada: ejecuta el nuevo aronium-util-setup-x64.exe")
	fmt.Println("  como Administrador (restaurará automáticamente tus cuentas desde Mis Documentos).")
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
