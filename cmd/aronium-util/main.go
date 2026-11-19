package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/SilverFeit/Aronium-Utility/internal/admin"
	"github.com/SilverFeit/Aronium-Utility/internal/backup"
	"github.com/SilverFeit/Aronium-Utility/internal/config"
	"github.com/SilverFeit/Aronium-Utility/internal/credit"
	"github.com/SilverFeit/Aronium-Utility/internal/db"
	"github.com/SilverFeit/Aronium-Utility/internal/events"
	"github.com/SilverFeit/Aronium-Utility/internal/fiscal"
	"github.com/SilverFeit/Aronium-Utility/internal/hub"
	"github.com/SilverFeit/Aronium-Utility/internal/inventory"
	"github.com/SilverFeit/Aronium-Utility/internal/migrate"
	"github.com/SilverFeit/Aronium-Utility/internal/registers"
	"github.com/SilverFeit/Aronium-Utility/internal/reports"
	"github.com/SilverFeit/Aronium-Utility/internal/service"
	"github.com/SilverFeit/Aronium-Utility/internal/sync"
	"github.com/SilverFeit/Aronium-Utility/internal/testutil"
)

const Version = "1.1.0"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	cmd := strings.ToLower(args[0])
	subArgs := args[1:]

	switch cmd {
	case "version", "--version", "-v":
		fmt.Printf("aronium-util v%s (Open Source MIT — Windows POS Companion & CLI Engine for Aronium Lite)\n", Version)
		return nil
	case "help", "--help", "-h":
		printUsage()
		return nil
	case "ui", "gui":
		return runUI(subArgs)
	case "init":
		return runInit(subArgs)
	case "doctor":
		return runDoctor(subArgs)
	case "drive", "gdrive", "auth":
		return runDrive(subArgs)
	case "watch":
		return runWatch(subArgs)
	case "service":
		return runService(subArgs)
	case "credit", "accounts", "cartera":
		return runCredit(subArgs)
	case "inventory":
		return runInventory(subArgs)
	case "registers":
		return runRegisters(subArgs)
	case "sync":
		return runSync(subArgs)
	case "hub":
		return runHub(subArgs)
	case "backup":
		return runBackup(subArgs)
	case "migrate":
		return runMigrate(subArgs)
	case "reports":
		return runReports(subArgs)
	case "fiscal":
		return runFiscal(subArgs)
	case "admin":
		return runAdmin(subArgs)
	case "demo-db", "test-fixture":
		return runDemoDB(subArgs)
	default:
		return fmt.Errorf("comando desconocido '%s'. Ejecuta 'aronium-util help' para ver todos los comandos disponibles", cmd)
	}
}

func printUsage() {
	fmt.Printf(`Aronium Utility v%s — CLI Open Source (MIT) & Motor Local para Aronium Utility UI (Tauri 2 + Svelte + TS)

USO:
  aronium-util <comando> [subcomando] [opciones]

COMANDOS PRINCIPALES:
  ui          Lanza la aplicación gráfica de escritorio "Aronium Utility UI" (Tauri 2 + Svelte + TypeScript)
  init        Configura aronium-util.yaml (autodetecta pos.db, ID de caja POS-01, Google Drive, Hub y Facturación)
  doctor      Diagnóstico de salud (pos.db solo lectura, PRAGMA quick_check, esquema, credenciales, cajas)
  drive       Conexión directa a Google Drive & Google Sheets con autenticación vía Web (login, sync, status)
  watch       Demonio en vivo que se inicia al encender el equipo y procesa transacciones sobre pos.db
  service     Registra, inicia, detiene y consulta el servicio de inicio automático en Windows
  credit      Gestiona cuentas de crédito por cliente, pagos parciales, notas, límites y estados de cuenta
  inventory   Consulta inventario vivo, valorización, alertas de stock bajo/vencimiento, kardex y listas de precio
  registers   Gestión de identidad de cajas (POS-01, POS-02), prevención de conflictos y ventas por estación
  sync        Sincronización inmediata de Tabla de Inventario + Cuentas de Crédito hacia Google Drive / Sheets
  hub         Servidor Central Multi-Estación Offline-First (serve, status, discover)
  backup      Respaldos en caliente (SQLite Online Snapshot), cifrado AES-256-GCM, rotación y restauración
  migrate     Exportación (JSON, CSV, PostgreSQL, SQL Server Express) y verificador post-migración (MIG-004..006)
  reports     Cierres diarios, moneda dual, envío por correo/Telegram/Drive y comandas ESC/POS
  fiscal      Conector de Facturación Electrónica Modular UBL 2.1 (emit, list, credit-note)
  admin       Consola administrativa (TUI en terminal, --json para UI y servidor local en localhost:8080)
  demo-db     Genera una base pos.db sintética realista para pruebas inmediatas

EJEMPLOS RÁPIDOS:
  aronium-util ui
  aronium-util init --pos-db "C:\Users\Caja\AppData\Local\Aronium\Data\pos.db" --register POS-01
  aronium-util drive login
  aronium-util sync
  aronium-util credit list --customer "Pedro"
  aronium-util credit note add "Pedro Pérez" "Compromiso de pago viernes"
  aronium-util watch
`, Version)
}

func loadConfigWithFlags(fs *flag.FlagSet, args []string) (*config.Config, string, error) {
	cfgPath := fs.String("config", "", "Ruta a aronium-util.yaml")
	posDB := fs.String("pos-db", "", "Ruta a pos.db de Aronium Lite")
	stateDB := fs.String("state-db", "", "Ruta a aronium_utility_state.db")
	regID := fs.String("register", "", "ID de caja registradora (ej. POS-01)")
	if err := fs.Parse(args); err != nil {
		return nil, "", err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return nil, "", err
	}
	if *posDB != "" {
		cfg.PosDBPath = *posDB
	}
	if *stateDB != "" {
		cfg.StateDBPath = *stateDB
	}
	if *regID != "" {
		cfg.CashRegisterID = *regID
	}
	resolvedPath := *cfgPath
	if resolvedPath == "" {
		resolvedPath = config.DefaultConfigPath()
	}
	return cfg, resolvedPath, nil
}

// 0. ui (Launches Aronium Utility UI executable - Tauri 2 + Svelte + TypeScript)
func runUI(args []string) error {
	uiBin := os.Getenv("ARONIUM_UI_BIN")
	exePath, _ := os.Executable()
	exeDir := filepath.Dir(exePath)

	uiNames := []string{"aronium-utility-ui", "Aronium Utility UI"}
	if runtime.GOOS == "windows" {
		uiNames = []string{"aronium-utility-ui.exe", "Aronium Utility UI.exe"}
	}

	var candidates []string
	if uiBin != "" {
		candidates = append(candidates, uiBin)
	}
	for _, n := range uiNames {
		candidates = append(candidates,
			filepath.Join(exeDir, n),
			filepath.Join(".", n),
			filepath.Join(".", "dist", "bin", n),
		)
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			candidates = append(candidates, filepath.Join(localAppData, "Programs", "AroniumUtility", n))
		}
	}

	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			cmd := exec.Command(c, args...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Start(); err != nil {
				return fmt.Errorf("error iniciando Aronium Utility UI (%s): %w", c, err)
			}
			fmt.Printf("[✓] Aronium Utility UI iniciada (PID %d): %s\n", cmd.Process.Pid, c)
			return nil
		}
	}

	return fmt.Errorf("no se encontró el ejecutable 'aronium-utility-ui'. Asegúrate de haber ejecutado el instalador o compilado la UI")
}

// 1. init (TODO-007)
func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Ruta donde guardar aronium-util.yaml")
	posDB := fs.String("pos-db", "", "Ruta a pos.db (autodetecta %LOCALAPPDATA%\\Aronium\\Data\\pos.db)")
	storeID := fs.String("store-id", "STORE-01", "Identificador de la tienda o sede")
	storeName := fs.String("store-name", "Mi Comercio Aronium", "Nombre comercial de la tienda")
	registerID := fs.String("register", "POS-01", "ID único de la caja registradora (ej. POS-01)")
	driveDir := fs.String("drive-dir", "", "Carpeta local de sincronización de Google Drive")
	clientID := fs.String("gdrive-client-id", "", "Google OAuth 2.0 Client ID")
	sheetID := fs.String("gsheet-id", "", "ID de hoja de Google Sheets maestra (opcional)")
	webLogin := fs.Bool("web-login", false, "Iniciar autenticación web con Google Drive inmediatamente")
	createDemoIfMissing := fs.Bool("create-demo-if-missing", false, "Crear pos.db de demostración si no existe aún")
	jsonOut := fs.Bool("json", false, "Salida en formato JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg := config.DefaultConfig()
	if *posDB != "" {
		cfg.PosDBPath = *posDB
	}
	cfg.StoreID = *storeID
	cfg.StoreName = *storeName
	cfg.CashRegisterID = *registerID
	if *driveDir != "" {
		cfg.GoogleDrive.LocalDriveSyncDir = *driveDir
	}
	if *clientID != "" {
		cfg.GoogleDrive.ClientID = *clientID
	}
	if *sheetID != "" {
		cfg.GoogleDrive.MasterSpreadsheetID = *sheetID
	}

	if *createDemoIfMissing {
		if _, err := os.Stat(cfg.PosDBPath); os.IsNotExist(err) {
			if err := testutil.CreateSyntheticPosDB(cfg.PosDBPath); err != nil {
				return fmt.Errorf("no se pudo crear pos.db demo: %w", err)
			}
		}
	}

	targetCfgPath := *cfgPath
	if targetCfgPath == "" {
		targetCfgPath = config.DefaultConfigPath()
	}
	if err := cfg.Save(targetCfgPath); err != nil {
		return err
	}

	stateConn, err := db.OpenStateDB(cfg.StateDBPath)
	if err != nil {
		return err
	}
	defer stateConn.Close()
	regMgr := registers.NewManager(nil, stateConn, cfg.StoreID)
	_, _ = regMgr.ClaimIdentity(cfg.CashRegisterID, cfg.CashRegisterName, "", true)

	if *jsonOut {
		return json.NewEncoder(os.Stdout).Encode(cfg)
	}

	fmt.Printf("[✓] Configuración inicializada en: %s\n", targetCfgPath)
	fmt.Printf("    • Comercio:          %s (%s)\n", cfg.StoreName, cfg.StoreID)
	fmt.Printf("    • Caja Registradora: %s\n", cfg.CashRegisterID)
	fmt.Printf("    • Base Aronium:      %s\n", cfg.PosDBPath)
	fmt.Printf("    • Base de Estado:    %s\n", cfg.StateDBPath)
	fmt.Printf("    • Carpeta Drive:     %s\n", filepath.Join(cfg.GoogleDrive.LocalDriveSyncDir, cfg.GoogleDrive.RootFolderName))

	if *webLogin {
		return runDrive([]string{"login", "--config", targetCfgPath})
	}
	return nil
}

// 2. doctor (TODO-008)
func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "Salida en formato JSON")
	cfg, _, err := loadConfigWithFlags(fs, args)
	if err != nil {
		return err
	}

	rep, err := db.RunDoctor(context.Background(), cfg.PosDBPath, cfg.StateDBPath, cfg.CashRegisterID, cfg.GoogleDrive.TokenVaultPath)
	if err != nil {
		return err
	}
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}

	fmt.Println("===============================================================")
	fmt.Println("  ARONIUM UTILITY — DIAGNÓSTICO DE SALUD (DOCTOR)")
	fmt.Println("===============================================================")
	fmt.Printf("Estado General:         %v\n", map[bool]string{true: "SALUDABLE (OK)", false: "ATENCIÓN REQUERIDA"}[rep.Healthy])
	fmt.Printf("Ruta pos.db (RO):       %s (Legible: %v)\n", rep.PosDBPath, rep.PosDBReadable)
	fmt.Printf("PRAGMA quick_check:     %s\n", rep.QuickCheckResult)
	fmt.Printf("Versión Esquema:        %s (%d tablas detectadas)\n", rep.AroniumSchemaVersion, len(rep.DetectedTables))
	fmt.Printf("Base de Estado Local:   %s (Lista: %v)\n", rep.StateDBPath, rep.StateDBReady)
	fmt.Printf("Identidad de Caja:      %s (Conflicto en red: %v)\n", rep.CashRegisterID, rep.RegisterIDConflict)
	fmt.Printf("Google Drive OAuth:     Configurado=%v | Token Cifrado Válido=%v\n", rep.GDriveConfigured, rep.GDriveTokenValid)
	fmt.Printf("Cola Offline (Outbox):  %d eventos pendientes\n", rep.PendingOutboxCount)
	if len(rep.Warnings) > 0 {
		fmt.Println("---------------------------------------------------------------")
		fmt.Println("ADVERTENCIAS:")
		for _, w := range rep.Warnings {
			fmt.Printf("  [!] %s\n", w)
		}
	}
	fmt.Println("===============================================================")
	return nil
}

// 3. drive / gdrive / auth (TODO-025..028 — Direct Web OAuth 2.0 Authentication & Google Drive Sync)
func runDrive(args []string) error {
	sub := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = strings.ToLower(args[0])
		args = args[1:]
	}

	fs := flag.NewFlagSet("drive", flag.ContinueOnError)
	clientID := fs.String("client-id", "", "Google Cloud OAuth 2.0 Client ID")
	clientSecret := fs.String("client-secret", "", "Google Cloud OAuth 2.0 Client Secret")
	timeoutSec := fs.Int("timeout", 180, "Tiempo máximo de espera para autenticación web (segundos)")
	noBrowser := fs.Bool("no-browser", false, "No abrir el navegador automáticamente (mostrar URL en consola)")
	jsonOut := fs.Bool("json", false, "Salida en formato JSON")
	cfg, cfgPath, err := loadConfigWithFlags(fs, args)
	if err != nil {
		return err
	}

	stateConn, err := db.OpenStateDB(cfg.StateDBPath)
	if err != nil {
		return err
	}
	defer stateConn.Close()

	switch sub {
	case "login", "connect", "auth", "web":
		mgr := sync.NewDriveManager(cfg, nil, stateConn)
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeoutSec)*time.Second)
		defer cancel()

		port := cfg.GoogleDrive.OAuthCallbackPort
		if port <= 0 {
			port = 8765
		}
		if !*jsonOut {
			fmt.Println("======================================================================")
			fmt.Println("  CONEXIÓN DIRECTA A GOOGLE DRIVE & SHEETS (AUTENTICACIÓN VÍA WEB)")
			fmt.Println("======================================================================")
			fmt.Printf("Iniciando portal local de autenticación OAuth 2.0 en http://127.0.0.1:%d ...\n", port)
		}

		tok, urlUsed, err := mgr.AuthenticateViaWeb(ctx, *clientID, *clientSecret, !*noBrowser)
		if err != nil {
			return fmt.Errorf("autenticación web cancelada o expirada (%s): %w", urlUsed, err)
		}
		_ = cfg.Save(cfgPath)
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{
				"connected":     true,
				"account_email": config.MaskEmail(tok.AccountEmail),
				"expiry":        tok.Expiry,
				"vault_path":    cfg.GoogleDrive.TokenVaultPath,
			})
		}
		fmt.Printf("[✓] ¡Google Drive conectado exitosamente! (Token cifrado en %s)\n", cfg.GoogleDrive.TokenVaultPath)
		return nil

	case "status":
		mgr := sync.NewDriveManager(cfg, nil, stateConn)
		tok, err := mgr.LoadToken()
		syncDir := filepath.Join(cfg.GoogleDrive.LocalDriveSyncDir, cfg.GoogleDrive.RootFolderName)
		if *jsonOut {
			statusMap := map[string]any{
				"connected":            err == nil && tok != nil && tok.AccessToken != "",
				"sync_dir":             syncDir,
				"master_workbook_name": cfg.GoogleDrive.MasterWorkbookName,
				"spreadsheet_id":       cfg.GoogleDrive.MasterSpreadsheetID,
			}
			if err == nil && tok != nil {
				statusMap["account_email"] = config.MaskEmail(tok.AccountEmail)
				statusMap["expiry"] = tok.Expiry
			}
			return json.NewEncoder(os.Stdout).Encode(statusMap)
		}
		fmt.Println("======================================================================")
		fmt.Println("  ESTADO DE CONEXIÓN CON GOOGLE DRIVE / GOOGLE SHEETS")
		fmt.Println("======================================================================")
		fmt.Printf("Carpeta Sincronizada: %s\n", syncDir)
		fmt.Printf("Tabla Maestra (.xlsx): %s\n", cfg.GoogleDrive.MasterWorkbookName)
		if err != nil {
			fmt.Println("Token OAuth 2.0 Web:   NO VINCULADO (Ejecuta 'aronium-util drive login' para autenticar vía web)")
		} else {
			fmt.Printf("Token OAuth 2.0 Web:   ACTIVO Y CIFRADO (Expira: %s)\n", tok.Expiry.Format(time.RFC3339))
			if tok.AccountEmail != "" {
				fmt.Printf("Cuenta Vinculada:      %s\n", config.MaskEmail(tok.AccountEmail))
			}
		}
		return nil

	case "sync":
		return runSync(args)

	default:
		return fmt.Errorf("subcomando de drive desconocido '%s' (usa: login, status, sync)", sub)
	}
}

// 4. sync (TODO-026..028)
func runSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "Salida en formato JSON")
	cfg, _, err := loadConfigWithFlags(fs, args)
	if err != nil {
		return err
	}

	posConn, err := db.OpenPosDBReadOnly(cfg.PosDBPath)
	if err != nil {
		return err
	}
	defer posConn.Close()

	stateConn, err := db.OpenStateDB(cfg.StateDBPath)
	if err != nil {
		return err
	}
	defer stateConn.Close()

	driveMgr := sync.NewDriveManager(cfg, posConn, stateConn)
	res, err := driveMgr.SyncAll(context.Background())
	if err != nil {
		return err
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}

	fmt.Println("[✓] Sincronización de Inventario y Cuentas de Crédito completada:")
	fmt.Printf("    • Tabla Maestra Unificada: %s\n", res.MasterWorkbookPath)
	fmt.Printf("    • Archivos de Inventario:  %d actualizados\n", len(res.InventoryFiles))
	fmt.Printf("    • Cuentas de Clientes:     %d fichas de clientes enrutadas a /Cuentas_Credito/Clientes/\n", len(res.CustomerFiles))
	if len(res.UploadedDriveIDs) > 0 {
		fmt.Printf("    • Google Drive API v3:     %d archivos sincronizados en la nube\n", len(res.UploadedDriveIDs))
	}
	if res.UpdatedSheetID != "" {
		fmt.Printf("    • Google Sheets API v4:    Hoja %s actualizada en tiempo real\n", res.UpdatedSheetID)
	}
	if res.QueuedOffline {
		fmt.Println("    • Modo Offline-First:      Cambios encolados en sync_outbox para subida automática al recuperar internet")
	}
	return nil
}

// 5. watch (TODO-014, TODO-015)
func runWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	once := fs.Bool("once", false, "Ejecutar un único ciclo de detección y sincronización y terminar")
	cfg, _, err := loadConfigWithFlags(fs, args)
	if err != nil {
		return err
	}

	posConn, err := db.OpenPosDBReadOnly(cfg.PosDBPath)
	if err != nil {
		return err
	}
	defer posConn.Close()

	if _, err := db.ValidateSchemaCompatibility(posConn); err != nil {
		return err
	}

	stateConn, err := db.OpenStateDB(cfg.StateDBPath)
	if err != nil {
		return err
	}
	defer stateConn.Close()

	bus := events.NewBus(cfg.Hooks)
	driveMgr := sync.NewDriveManager(cfg, posConn, stateConn)
	hostname, _ := os.Hostname()
	hubClient := hub.NewClient(cfg.Hub.ServerURL, config.LoadSecret("hub_api_token", "ARONIUM_HUB_TOKEN"), cfg.StoreID, cfg.CashRegisterID, hostname, stateConn)
	fiscalProv := fiscal.NewStandardUBLProvider(cfg, stateConn)

	bus.SubscribeAll(func(ctx context.Context, ev events.Event) error {
		fmt.Printf("[%s] Evento detectado en %s: %s (ID=%s)\n",
			ev.TimestampUTC.Format("15:04:05"), ev.CashRegisterID, ev.Type, ev.ID)

		if cfg.Watch.AutoSyncDrive && cfg.GoogleDrive.Enabled {
			if _, err := driveMgr.SyncAll(ctx); err != nil {
				return err
			}
		}
		if cfg.Watch.AutoSyncHub && cfg.Hub.Enabled && ev.Document != nil && ev.Type == events.EventSaleCreated {
			_, _ = hubClient.SubmitSale(ctx, *ev.Document)
		}
		if cfg.Watch.AutoEmitFiscal && cfg.Fiscal.Enabled && ev.Document != nil && ev.Type == events.EventSaleCreated {
			_, _ = fiscalProv.SubmitDocument(ctx, *ev.Document)
		}
		return nil
	})

	watcher := events.NewWatcher(cfg, posConn, stateConn, bus)
	if *once {
		count, err := watcher.PollOnce(context.Background())
		if err != nil {
			return err
		}
		_, err = driveMgr.SyncAll(context.Background())
		fmt.Printf("[✓] Ciclo watch --once completado (%d eventos nuevos emitidos).\n", count)
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Printf("[*] Aronium Utility Watcher iniciado en vivo sobre %s (Caja: %s)...\n", cfg.PosDBPath, cfg.CashRegisterID)
	err = watcher.Run(ctx)
	if err == context.Canceled {
		return nil
	}
	return err
}

// 6. service (TODO-016)
func runService(args []string) error {
	action := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action = strings.ToLower(args[0])
		args = args[1:]
	}
	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Ruta a aronium-util.yaml")
	jsonOut := fs.Bool("json", false, "Salida en formato JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	mgr := service.NewManager(*cfgPath)
	switch action {
	case "install":
		msg, err := mgr.Install()
		if err != nil {
			return err
		}
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{"action": "install", "result": msg})
		}
		fmt.Println("[✓]", msg)
	case "start":
		msg, err := mgr.Start()
		if err != nil {
			return err
		}
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{"action": "start", "result": msg})
		}
		fmt.Println("[✓]", msg)
	case "stop":
		msg, err := mgr.Stop()
		if err != nil {
			return err
		}
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{"action": "stop", "result": msg})
		}
		fmt.Println("[✓]", msg)
	case "status":
		st := mgr.Status()
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{"status": st, "log_path": mgr.LogPath})
		}
		fmt.Println("Estado del servicio:", st)
	case "logs":
		logs, err := mgr.Logs(8192)
		if err != nil {
			return err
		}
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{"logs": logs})
		}
		fmt.Println(logs)
	default:
		return fmt.Errorf("acción de servicio desconocida '%s' (usa: install, start, stop, status, logs)", action)
	}
	return nil
}

// 7. credit (TODO-017..021 — Customer Credit Accounts, Notes, Limits & Statements)
func runCredit(args []string) error {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = strings.ToLower(args[0])
		args = args[1:]
	}

	switch sub {
	case "list":
		fs := flag.NewFlagSet("credit list", flag.ContinueOnError)
		customer := fs.String("customer", "", "Filtrar por nombre o ID de cliente")
		overdueDays := fs.Int("overdue-days", 0, "Filtrar clientes con vencimiento mayor o igual a N días")
		jsonOut := fs.Bool("json", false, "Salida en formato JSON")
		cfg, _, err := loadConfigWithFlags(fs, args)
		if err != nil {
			return err
		}
		posConn, err := db.OpenPosDBReadOnly(cfg.PosDBPath)
		if err != nil {
			return err
		}
		defer posConn.Close()
		stateConn, err := db.OpenStateDB(cfg.StateDBPath)
		if err != nil {
			return err
		}
		defer stateConn.Close()

		eng := credit.NewEngine(cfg, posConn, stateConn)
		rep, err := eng.BuildLedger(*customer, *overdueDays)
		if err != nil {
			return err
		}
		if *jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(rep)
		}

		fmt.Println("================================================================================")
		fmt.Printf("  CUENTAS DE CRÉDITO DE CLIENTES (CUENTAS POR COBRAR) — %s (%s)\n", cfg.StoreName, cfg.CashRegisterID)
		fmt.Printf("  Saldo Total Pendiente: $%.2f | Clientes con Saldo: %d | Límite Excedido: %d\n",
			rep.TotalPendingPortfolio, rep.ActiveDebtorCount, rep.OverLimitCount)
		fmt.Println("================================================================================")
		for _, acc := range rep.Accounts {
			fmt.Printf("• %-26s | Crédito: $%-10.2f | Pagado: $%-10.2f | SALDO: $%-10.2f | [%s]\n",
				acc.CustomerName, acc.TotalCreditIssued, acc.TotalPaymentsPaid, acc.PendingBalance, acc.AccountStatus)
			for _, tx := range acc.Transactions {
				fmt.Printf("    -> [%s] Factura #%s | %s | Saldo: $%.2f\n",
					tx.Date, tx.DocumentNumber, tx.ItemsSummary, tx.PendingBalance)
				if tx.CombinedNotes != "" {
					fmt.Printf("       Nota: %s\n", tx.CombinedNotes)
				}
			}
		}
		return nil

	case "note":
		if len(args) > 0 && strings.ToLower(args[0]) == "add" {
			args = args[1:]
		}
		fs := flag.NewFlagSet("credit note add", flag.ContinueOnError)
		promise := fs.String("promise", "", "Fecha de compromiso de pago (YYYY-MM-DD)")
		jsonOut := fs.Bool("json", false, "Salida en formato JSON")
		cfg, _, err := loadConfigWithFlags(fs, args)
		if err != nil {
			return err
		}
		rem := fs.Args()
		if len(rem) < 2 {
			return fmt.Errorf("uso: aronium-util credit note add <cliente_o_documento> \"<nota>\"")
		}
		target := rem[0]
		noteText := strings.Join(rem[1:], " ")

		stateConn, err := db.OpenStateDB(cfg.StateDBPath)
		if err != nil {
			return err
		}
		defer stateConn.Close()

		note := db.CreditNote{
			CustomerName: target,
			Note:         noteText,
			DuePromise:   *promise,
			CreatedBy:    cfg.CashRegisterID,
		}
		if idNum, err := strconv.ParseInt(target, 10, 64); err == nil {
			note.CustomerID = idNum
		}
		id, err := db.AddCreditNote(stateConn, note)
		if err != nil {
			return err
		}
		note.ID = id
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(note)
		}
		fmt.Printf("[✓] Anotación de cuenta #%d guardada en aronium_utility_state.db para '%s': %s\n", id, target, noteText)
		return nil

	case "limit":
		fs := flag.NewFlagSet("credit limit", flag.ContinueOnError)
		jsonOut := fs.Bool("json", false, "Salida en formato JSON")
		cfg, _, err := loadConfigWithFlags(fs, args)
		if err != nil {
			return err
		}
		rem := fs.Args()
		if len(rem) < 2 {
			return fmt.Errorf("uso: aronium-util credit limit <id_cliente> <monto_limite>")
		}
		cid, err := strconv.ParseInt(rem[0], 10, 64)
		if err != nil {
			return fmt.Errorf("id_cliente inválido: %w", err)
		}
		limitVal, err := strconv.ParseFloat(rem[1], 64)
		if err != nil {
			return fmt.Errorf("monto de límite inválido: %w", err)
		}
		stateConn, err := db.OpenStateDB(cfg.StateDBPath)
		if err != nil {
			return err
		}
		defer stateConn.Close()
		if err := db.SetCustomerCreditLimit(stateConn, cid, fmt.Sprintf("Cliente #%d", cid), limitVal); err != nil {
			return err
		}
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"customer_id": cid, "credit_limit": limitVal})
		}
		fmt.Printf("[✓] Límite máximo de crédito para cliente #%d actualizado a $%.2f\n", cid, limitVal)
		return nil

	case "statement":
		fs := flag.NewFlagSet("credit statement", flag.ContinueOnError)
		jsonOut := fs.Bool("json", false, "Salida en formato JSON")
		cfg, _, err := loadConfigWithFlags(fs, args)
		if err != nil {
			return err
		}
		rem := fs.Args()
		if len(rem) < 1 {
			return fmt.Errorf("uso: aronium-util credit statement \"<nombre_o_id_cliente>\"")
		}
		customerQuery := strings.Join(rem, " ")
		posConn, err := db.OpenPosDBReadOnly(cfg.PosDBPath)
		if err != nil {
			return err
		}
		defer posConn.Close()
		stateConn, err := db.OpenStateDB(cfg.StateDBPath)
		if err != nil {
			return err
		}
		defer stateConn.Close()

		eng := credit.NewEngine(cfg, posConn, stateConn)
		rep, err := eng.BuildLedger(customerQuery, 0)
		if err != nil {
			return err
		}
		if len(rep.Accounts) == 0 {
			return fmt.Errorf("no se encontraron cuentas de crédito para el cliente '%s'", customerQuery)
		}
		stmt := credit.GenerateStatement(rep.Accounts[0], cfg.StoreName, cfg.Inventory.DualCurrencyCode, cfg.Inventory.DualCurrencyRate)
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{
				"account":   rep.Accounts[0],
				"statement": stmt,
			})
		}
		fmt.Println(stmt)
		return nil

	case "sync":
		return runSync(args)

	default:
		return fmt.Errorf("subcomando de credit desconocido '%s' (usa: list, note add, limit, statement, sync)", sub)
	}
}

// 8. inventory (TODO-022..024, TODO-048, TODO-051)
func runInventory(args []string) error {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = strings.ToLower(args[0])
		args = args[1:]
	}

	fs := flag.NewFlagSet("inventory", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "Salida en formato JSON")
	expireDays := fs.Int("expire-days", 30, "Días de anticipación para alerta de vencimiento (7, 15, 30)")
	listName := fs.String("list-name", "Mayorista", "Nombre de lista de precios (Mayorista, Minorista, VIP)")
	cfg, _, err := loadConfigWithFlags(fs, args)
	if err != nil {
		return err
	}

	posConn, err := db.OpenPosDBReadOnly(cfg.PosDBPath)
	if err != nil {
		return err
	}
	defer posConn.Close()
	stateConn, err := db.OpenStateDB(cfg.StateDBPath)
	if err != nil {
		return err
	}
	defer stateConn.Close()

	eng := inventory.NewEngine(cfg, posConn, stateConn)

	switch sub {
	case "list":
		rep, err := eng.BuildReport(*expireDays)
		if err != nil {
			return err
		}
		if *jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(rep)
		}
		fmt.Println("================================================================================")
		fmt.Printf("  INVENTARIO VIVO Y VALORIZACIÓN — %s\n", cfg.StoreName)
		fmt.Print("  " + inventory.FormatAlertsSummary(rep))
		fmt.Println("================================================================================")
		for _, it := range rep.Items {
			fmt.Printf("• [%-8s] %-28s | Stock: %6.2f %-3s | Costo: $%8.2f | Venta: $%8.2f | [%s]\n",
				it.Code, it.Name, it.StockQuantity, it.MeasurementUnit, it.Cost, it.SalePrice, it.Status)
		}
		return nil

	case "alerts":
		rep, err := eng.BuildReport(*expireDays)
		if err != nil {
			return err
		}
		if *jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(map[string]any{"stock_alerts": rep.Alerts, "expiring_soon": rep.ExpiringSoon})
		}
		fmt.Println("=== ALERTAS DE STOCK BAJO / AGOTADO / NEGATIVO ===")
		for _, a := range rep.Alerts {
			fmt.Printf("  [!] %-26s | Stock Actual: %.2f (Mín: %.2f) -> %s\n", a.Name, a.StockQuantity, a.MinStock, a.Status)
		}
		if len(rep.ExpiringSoon) > 0 {
			fmt.Println("=== ALERTAS DE PRÓXIMO VENCIMIENTO ===")
			for _, x := range rep.ExpiringSoon {
				fmt.Printf("  [!] %-26s | Vence: %s (%d días) | Lote: %s\n", x.Name, x.ExpirationDate, x.DaysToExpire, x.BatchLot)
			}
		}
		return nil

	case "kardex":
		_, _ = eng.SyncKardexFromDocuments()
		entries, err := eng.ListKardex(50)
		if err != nil {
			return err
		}
		if *jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(entries)
		}
		fmt.Println("=== KARDEX DE MOVIMIENTOS DE INVENTARIO ===")
		for _, k := range entries {
			fmt.Printf("  [%s] Doc #%s (%s) | %-24s | Delta: %+6.2f | Stock Final: %6.2f\n",
				k.RecordedAt, k.DocumentNumber, k.RegisterID, k.ProductName, k.DeltaQty, k.ResultingQty)
		}
		return nil

	case "pricelists":
		items, err := eng.GetPriceList(*listName)
		if err != nil {
			return err
		}
		if *jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(items)
		}
		fmt.Printf("=== LISTA DE PRECIOS: %s (Moneda Dual %s @ %.2f) ===\n", strings.ToUpper(*listName), cfg.Inventory.DualCurrencyCode, cfg.Inventory.DualCurrencyRate)
		for _, it := range items {
			fmt.Printf("  • [%-8s] %-26s | Base: $%8.2f | %s: $%8.2f (%.2f %s)\n",
				it.ProductCode, it.ProductName, it.BasePrice, it.ListName, it.CustomPrice, it.DualPrice, cfg.Inventory.DualCurrencyCode)
		}
		return nil

	default:
		return fmt.Errorf("subcomando de inventory desconocido '%s' (usa: list, alerts, kardex, pricelists)", sub)
	}
}

// 9. registers (TODO-029..031)
func runRegisters(args []string) error {
	sub := "sales"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = strings.ToLower(args[0])
		args = args[1:]
	}

	fs := flag.NewFlagSet("registers", flag.ContinueOnError)
	fromDate := fs.String("from", "", "Fecha inicial (YYYY-MM-DD)")
	toDate := fs.String("to", "", "Fecha final (YYYY-MM-DD)")
	filterReg := fs.String("filter-register", "", "Filtrar por caja registradora (ej. POS-01)")
	force := fs.Bool("force", false, "Forzar asignación de identidad de caja")
	jsonOut := fs.Bool("json", false, "Salida en formato JSON")
	cfg, _, err := loadConfigWithFlags(fs, args)
	if err != nil {
		return err
	}

	posConn, err := db.OpenPosDBReadOnly(cfg.PosDBPath)
	if err != nil {
		return err
	}
	defer posConn.Close()
	stateConn, err := db.OpenStateDB(cfg.StateDBPath)
	if err != nil {
		return err
	}
	defer stateConn.Close()

	mgr := registers.NewManager(posConn, stateConn, cfg.StoreID)

	switch sub {
	case "claim":
		id, err := mgr.ClaimIdentity(cfg.CashRegisterID, cfg.CashRegisterName, "", *force)
		if err != nil {
			return err
		}
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(id)
		}
		fmt.Printf("[✓] Estación registrada como caja '%s' en host '%s'\n", id.RegisterID, id.Hostname)
		return nil

	case "list":
		list, err := mgr.ListRegisters()
		if err != nil {
			return err
		}
		if *jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(list)
		}
		for _, r := range list {
			fmt.Printf("• %-10s | %-24s | Host: %-16s | Último latido: %s\n", r.RegisterID, r.RegisterName, r.Hostname, r.LastHeartbeat)
		}
		return nil

	case "sales":
		summaries, err := mgr.QuerySales(cfg.CashRegisterID, *filterReg, *fromDate, *toDate)
		if err != nil {
			return err
		}
		if *jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(summaries)
		}
		fmt.Println("=== VENTAS Y TRAZABILIDAD POR CAJA REGISTRADORA (PRO-FR-004) ===")
		for _, s := range summaries {
			fmt.Printf("• Caja %-10s | Transacciones: %-4d | Total: $%-10.2f | Contado: $%-10.2f | Crédito: $%-10.2f\n",
				s.RegisterID, s.TransactionCount, s.TotalSales, s.CashSales, s.CreditSales)
		}
		return nil

	default:
		return fmt.Errorf("subcomando de registers desconocido '%s' (usa: sales, list, claim)", sub)
	}
}

// 10. hub (TODO-032..036)
func runHub(args []string) error {
	sub := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = strings.ToLower(args[0])
		args = args[1:]
	}
	fs := flag.NewFlagSet("hub", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8443", "Dirección de escucha del Hub Central")
	cfg, _, err := loadConfigWithFlags(fs, args)
	if err != nil {
		return err
	}

	stateConn, err := db.OpenStateDB(cfg.StateDBPath)
	if err != nil {
		return err
	}
	defer stateConn.Close()

	srv, err := hub.NewServer(stateConn, cfg.StoreID, config.LoadSecret("hub_api_token", "ARONIUM_HUB_TOKEN"))
	if err != nil {
		return err
	}

	switch sub {
	case "serve":
		fmt.Printf("[*] Hub Central Multi-Estación escuchando en http://%s ...\n", *addr)
		fmt.Println("    " + hub.CatalogCacheWarning())
		return http.ListenAndServe(*addr, srv.Handler())
	case "status":
		st, err := srv.Status()
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(st)
	case "discover":
		found := hub.DiscoverLAN(cfg.Hub.DiscoveryPort, cfg.Hub.ServerURL, 500*time.Millisecond)
		fmt.Printf("Hub Central detectado/configurado en: %s\n", found)
		return nil
	default:
		return fmt.Errorf("subcomando de hub desconocido '%s' (usa: serve, status, discover)", sub)
	}
}

// 11. backup (TODO-038..041)
func runBackup(args []string) error {
	sub := "create"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = strings.ToLower(args[0])
		args = args[1:]
	}
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	passphrase := fs.String("passphrase", os.Getenv("ARONIUM_BACKUP_PASSPHRASE"), "Frase de cifrado AES-256-GCM (opcional)")
	fileArg := fs.String("file", "", "Ruta del archivo de respaldo para verify o restore")
	keep := fs.Int("keep", 7, "Cantidad de respaldos a conservar en rotación")
	jsonOut := fs.Bool("json", false, "Salida en formato JSON")
	cfg, _, err := loadConfigWithFlags(fs, args)
	if err != nil {
		return err
	}

	mgr := backup.NewManager(cfg)
	switch sub {
	case "create":
		manifest, err := mgr.Create(context.Background(), *passphrase)
		if err != nil {
			return err
		}
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(manifest)
		}
		fmt.Printf("[✓] Respaldo en caliente creado exitosamente:\n")
		fmt.Printf("    • Archivo:  %s (%d bytes)\n", manifest.ArchivePath, manifest.SizeBytes)
		fmt.Printf("    • SHA-256:  %s\n", manifest.SHA256)
		fmt.Printf("    • Cifrado:  %v\n", manifest.Encrypted)
		return nil

	case "verify":
		target := *fileArg
		if target == "" && len(fs.Args()) > 0 {
			target = fs.Args()[0]
		}
		if target == "" {
			return fmt.Errorf("especifica el archivo a verificar con --file <ruta>")
		}
		res, err := mgr.Verify(target, *passphrase)
		if err != nil {
			return err
		}
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"verified": true, "details": res})
		}
		fmt.Printf("[✓] Verificación de respaldo exitosa: %s\n", res)
		return nil

	case "restore":
		target := *fileArg
		if target == "" && len(fs.Args()) > 0 {
			target = fs.Args()[0]
		}
		if target == "" {
			return fmt.Errorf("especifica el archivo a restaurar con --file <ruta>")
		}
		safety, err := mgr.Restore(target, cfg.PosDBPath, *passphrase, false)
		if err != nil {
			return err
		}
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"restored": cfg.PosDBPath, "safety_backup": safety})
		}
		fmt.Printf("[✓] Base de datos restaurada exitosamente en: %s\n", cfg.PosDBPath)
		if safety != "" {
			fmt.Printf("    • Respaldo de seguridad previo guardado en: %s\n", safety)
		}
		return nil

	case "prune":
		removed, err := mgr.ApplyRetention(*keep)
		if err != nil {
			return err
		}
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"removed": removed})
		}
		fmt.Printf("[✓] Rotación completada (%d respaldos antiguos eliminados).\n", len(removed))
		return nil

	default:
		return fmt.Errorf("subcomando de backup desconocido '%s' (usa: create, verify, restore, prune)", sub)
	}
}

// 12. migrate (TODO-042..045)
func runMigrate(args []string) error {
	sub := "export"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = strings.ToLower(args[0])
		args = args[1:]
	}
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	format := fs.String("format", "sqlserver", "Formato de destino: json, csv, postgres, sqlserver")
	outDir := fs.String("out", "", "Carpeta de salida para la migración")
	jsonOut := fs.Bool("json", false, "Salida en formato JSON")
	cfg, _, err := loadConfigWithFlags(fs, args)
	if err != nil {
		return err
	}

	posConn, err := db.OpenPosDBReadOnly(cfg.PosDBPath)
	if err != nil {
		return err
	}
	defer posConn.Close()

	eng := migrate.NewEngine(posConn, cfg.StoreID, cfg.CashRegisterID)
	targetDir := *outDir
	if targetDir == "" {
		targetDir = filepath.Join(config.DefaultAppDataDir(), "migrations")
	}

	switch sub {
	case "export":
		files, bundle, err := eng.Export(*format, targetDir)
		if err != nil {
			return err
		}
		verRes, err := eng.Verify(bundle)
		if err != nil {
			return err
		}
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"files": files, "verification": verRes})
		}
		fmt.Printf("[✓] Migración / Exportación (%s) completada y verificada (MIG-004/005 Passed=%v):\n", *format, verRes.Passed)
		for _, f := range files {
			fmt.Printf("    • %s\n", f)
		}
		return nil

	case "checklist":
		bundle, err := eng.BuildBundle()
		if err != nil {
			return err
		}
		fmt.Println(migrate.GeneratePostMigrationChecklist(cfg.StoreID, bundle.Metrics))
		return nil

	default:
		return fmt.Errorf("subcomando de migrate desconocido '%s' (usa: export, checklist)", sub)
	}
}

// 13. reports (TODO-047..050)
func runReports(args []string) error {
	sub := "daily"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = strings.ToLower(args[0])
		args = args[1:]
	}
	fs := flag.NewFlagSet("reports", flag.ContinueOnError)
	dateFilter := fs.String("date", "", "Fecha del reporte (YYYY-MM-DD, vacío para consolidado)")
	deliver := fs.Bool("deliver", true, "Guardar copia automática en carpeta de Google Drive / Reportes")
	jsonOut := fs.Bool("json", false, "Salida en formato JSON")
	cfg, _, err := loadConfigWithFlags(fs, args)
	if err != nil {
		return err
	}

	posConn, err := db.OpenPosDBReadOnly(cfg.PosDBPath)
	if err != nil {
		return err
	}
	defer posConn.Close()
	stateConn, err := db.OpenStateDB(cfg.StateDBPath)
	if err != nil {
		return err
	}
	defer stateConn.Close()

	eng := reports.NewEngine(cfg, posConn, stateConn)
	switch sub {
	case "daily", "schedule":
		rep, err := eng.BuildDailyReport(*dateFilter)
		if err != nil {
			return err
		}
		if *deliver {
			_, _ = eng.SaveAndDeliver(context.Background(), rep)
		}
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(rep)
		}
		fmt.Print(eng.FormatDailyReportText(rep))
		return nil
	default:
		return fmt.Errorf("subcomando de reports desconocido '%s' (usa: daily, schedule)", sub)
	}
}

// 14. fiscal (TODO-052..056)
func runFiscal(args []string) error {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = strings.ToLower(args[0])
		args = args[1:]
	}
	fs := flag.NewFlagSet("fiscal", flag.ContinueOnError)
	docID := fs.Int64("doc-id", 0, "ID de documento en Aronium pos.db a emitir electrónicamente")
	reason := fs.String("reason", "Devolución / Anulación", "Motivo para nota de crédito")
	jsonOut := fs.Bool("json", false, "Salida en formato JSON")
	cfg, _, err := loadConfigWithFlags(fs, args)
	if err != nil {
		return err
	}

	posConn, err := db.OpenPosDBReadOnly(cfg.PosDBPath)
	if err != nil {
		return err
	}
	defer posConn.Close()
	stateConn, err := db.OpenStateDB(cfg.StateDBPath)
	if err != nil {
		return err
	}
	defer stateConn.Close()

	prov := fiscal.NewStandardUBLProvider(cfg, stateConn)

	switch sub {
	case "emit":
		docs, err := db.ListDocuments(posConn, 0, cfg.CashRegisterID)
		if err != nil {
			return err
		}
		var results []*fiscal.FiscalSubmissionResult
		for _, d := range docs {
			if *docID != 0 && d.ID != *docID {
				continue
			}
			res, err := prov.SubmitDocument(context.Background(), d)
			results = append(results, res)
			if !*jsonOut {
				if err != nil {
					fmt.Printf("[!] Doc #%s (%s) en cola fiscal: %v\n", d.Number, d.CustomerName, err)
					continue
				}
				fmt.Printf("[✓] Documento Electrónico emitido: %s | Estado: %s\n", res.FiscalNumber, res.Status)
				fmt.Printf("    • UUID: %s\n", res.DocumentUUID)
				fmt.Printf("    • XML:  %s\n", res.XMLPath)
				fmt.Printf("    • PDF:  %s\n", res.PDFPath)
			}
		}
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(results)
		}
		return nil

	case "credit-note":
		docs, err := db.ListDocuments(posConn, 0, cfg.CashRegisterID)
		if err != nil {
			return err
		}
		for _, d := range docs {
			if d.ID == *docID || (*docID == 0 && len(docs) > 0) {
				res, err := prov.EmitCreditNote(context.Background(), d, *reason)
				if err != nil {
					return err
				}
				if *jsonOut {
					return json.NewEncoder(os.Stdout).Encode(res)
				}
				fmt.Printf("[✓] Nota de Crédito emitida: %s | UUID: %s\n", res.FiscalNumber, res.DocumentUUID)
				break
			}
		}
		return nil

	case "list":
		list, err := prov.ListFiscalDocuments()
		if err != nil {
			return err
		}
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(list)
		}
		fmt.Println("=== COLA Y ESTADO DE DOCUMENTOS ELECTRÓNICOS (UBL 2.1) ===")
		for _, r := range list {
			fmt.Printf("• %-18s (Aronium #%s) | Estado: %-12s | UUID: %s...\n",
				r.FiscalNumber, r.AroniumDocumentNumber, r.Status, truncateUUID(r.DocumentUUID))
		}
		return nil

	default:
		return fmt.Errorf("subcomando de fiscal desconocido '%s' (usa: emit, list, credit-note)", sub)
	}
}

// 15. admin (TODO-037)
func runAdmin(args []string) error {
	fs := flag.NewFlagSet("admin", flag.ContinueOnError)
	serveWeb := fs.Bool("serve", false, "Levantar panel administrativo web local de solo lectura en localhost:8080")
	addr := fs.String("addr", "127.0.0.1:8080", "Dirección del servidor web administrativo local")
	filterReg := fs.String("filter-register", "", "Filtrar por caja registradora")
	jsonOut := fs.Bool("json", false, "Salida en formato JSON para consumo por Aronium Utility UI (Tauri 2 + Svelte)")
	cfg, _, err := loadConfigWithFlags(fs, args)
	if err != nil {
		return err
	}

	posConn, err := db.OpenPosDBReadOnly(cfg.PosDBPath)
	if err != nil {
		return err
	}
	defer posConn.Close()
	stateConn, err := db.OpenStateDB(cfg.StateDBPath)
	if err != nil {
		return err
	}
	defer stateConn.Close()

	console := admin.NewConsole(cfg, posConn, stateConn)
	if *jsonOut {
		snap, err := console.BuildSnapshot(*filterReg)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(snap)
	}

	tui, err := console.RenderTerminalTUI(*filterReg)
	if err != nil {
		return err
	}
	fmt.Print(tui)

	if *serveWeb {
		fmt.Printf("[*] Panel Administrativo Web Local (Solo Lectura) activo en http://%s ...\n", *addr)
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return console.ServeHTTP(ctx, *addr)
	}
	return nil
}

// 16. demo-db (TODO-057)
func runDemoDB(args []string) error {
	fs := flag.NewFlagSet("demo-db", flag.ContinueOnError)
	out := fs.String("out", "pos.db", "Ruta donde crear la base pos.db sintética")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := testutil.CreateSyntheticPosDB(*out); err != nil {
		return err
	}
	fmt.Printf("[✓] Base de datos Aronium Lite sintética creada exitosamente en: %s\n", *out)
	return nil
}

func truncateUUID(s string) string {
	if len(s) <= 24 {
		return s
	}
	return s[:24]
}
