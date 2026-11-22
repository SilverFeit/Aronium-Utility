package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/SilverFeit/Aronium-Utility/internal/config"
	"github.com/SilverFeit/Aronium-Utility/internal/service"
	"github.com/SilverFeit/Aronium-Utility/internal/updater"
)

const AppVersion = "1.2.8"

//go:embed all:dist
var embeddedFrontend embed.FS

type uiInstanceInfo struct {
	PID       int    `json:"pid"`
	Port      int    `json:"port"`
	URL       string `json:"url"`
	Version   string `json:"version"`
	StartedAt string `json:"started_at"`
}

func instanceLockPath() string {
	return filepath.Join(config.DefaultAppDataDir(), "ui-instance.json")
}

// notifyExistingInstance checks whether Aronium Utility UI is already running in the system tray.
// If it is alive, it asks it to open/focus its window (unless headless) and returns true so no duplicate tray icon is created.
func notifyExistingInstance(openWindow bool) bool {
	raw, err := os.ReadFile(instanceLockPath())
	if err != nil {
		return false
	}
	var inst uiInstanceInfo
	if err := json.Unmarshal(raw, &inst); err != nil || inst.URL == "" {
		return false
	}

	client := &http.Client{Timeout: 1200 * time.Millisecond}
	resp, err := client.Get(strings.TrimRight(inst.URL, "/") + "/api/health")
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}

	if openWindow {
		reqResp, err := client.Post(strings.TrimRight(inst.URL, "/")+"/api/window/open", "application/json", bytes.NewReader([]byte(`{}`)))
		if err == nil {
			_ = reqResp.Body.Close()
		}
	}
	return true
}

func main() {
	portFlag := flag.Int("port", 0, "Local IPC & WebView port (0 = ephemeral)")
	headlessFlag := flag.Bool("headless", false, "Do not launch desktop WebView window automatically")
	trayOnlyFlag := flag.Bool("tray-only", false, "Start minimized in Windows System Tray without opening the UI window")
	ensureWatchFlag := flag.Bool("ensure-watch", true, "Ensure background aronium-util watch daemon is running")
	flag.Parse()

	shouldOpenInitially := !*headlessFlag && !*trayOnlyFlag

	// Single-instance handoff: avoid duplicate tray icons & duplicate background watchers
	if *portFlag == 0 && notifyExistingInstance(shouldOpenInitially) {
		return
	}

	cliBin := locateAroniumCLISidecar()
	selfExe, _ := os.Executable()
	svcMgr := service.NewManager("")
	if cliBin != "" {
		svcMgr.BinaryPath = cliBin
	}

	// Ensure background live transaction watcher is running invisibly when UI starts
	var (
		procMu      sync.Mutex
		watchCmd    *exec.Cmd
		webViewCmds []*exec.Cmd
		updateMu    sync.Mutex
		latestTag   string
	)

	if *ensureWatchFlag && cliBin != "" {
		watchCmd = exec.Command(cliBin, "watch")
		service.HideChildWindow(watchCmd)
		if err := watchCmd.Start(); err != nil {
			watchCmd = nil
		} else {
			go func(c *exec.Cmd) { _ = c.Wait() }(watchCmd)
		}
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *portFlag))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start local UI IPC listener: %v\n", err)
		os.Exit(1)
	}
	defer ln.Close()

	actualPort := ln.Addr().(*net.TCPAddr).Port
	uiURL := fmt.Sprintf("http://127.0.0.1:%d", actualPort)

	_ = os.MkdirAll(filepath.Dir(instanceLockPath()), 0755)
	if instBytes, mErr := json.Marshal(uiInstanceInfo{
		PID:       os.Getpid(),
		Port:      actualPort,
		URL:       uiURL,
		Version:   AppVersion,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}); mErr == nil {
		_ = os.WriteFile(instanceLockPath(), instBytes, 0644)
	}
	defer func() {
		_ = os.Remove(instanceLockPath())
	}()

	openUIWindow := func() {
		cmd, err := launchNativeOSWebViewWindow(uiURL)
		if err == nil && cmd != nil {
			go func(c *exec.Cmd) { _ = c.Wait() }(cmd)
			procMu.Lock()
			webViewCmds = append(webViewCmds, cmd)
			procMu.Unlock()
		}
	}

	distFS, err := fs.Sub(embeddedFrontend, "dist")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load embedded Svelte frontend: %v\n", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"version": AppVersion,
			"pid":     os.Getpid(),
		})
	})

	mux.HandleFunc("/api/window/open", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		go openUIWindow()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"opened":true}`))
	})

	// IPC Endpoint: Spawns `aronium-util` CLI sidecar and returns its JSON stdout (Zero duplicated business logic)
	mux.HandleFunc("/api/ipc/cli", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Args []string `json:"args"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, cliBin, req.Args...)
		service.HideChildWindow(cmd)
		out, err := cmd.CombinedOutput()
		if err != nil {
			http.Error(w, fmt.Sprintf("CLI sidecar error (%v): %s", err, strings.TrimSpace(string(out))), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(out)
	})

	mux.Handle("/", http.FileServer(http.FS(distFS)))

	srv := &http.Server{Handler: mux}
	go func() {
		_ = srv.Serve(ln)
	}()

	var exitOnce sync.Once
	shutdownAll := func() {
		exitOnce.Do(func() {
			_ = os.Remove(instanceLockPath())

			procMu.Lock()
			wCmd := watchCmd
			views := append([]*exec.Cmd(nil), webViewCmds...)
			procMu.Unlock()

			if wCmd != nil && wCmd.Process != nil {
				_ = wCmd.Process.Kill()
			}
			for _, vc := range views {
				if vc != nil && vc.Process != nil {
					_ = vc.Process.Kill()
				}
			}

			shutCtx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
			_ = srv.Shutdown(shutCtx)
			cancel()
		})
	}
	defer shutdownAll()

	// Graceful OS signal cleanup
	sigCtx, sigStop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer sigStop()
	go func() {
		<-sigCtx.Done()
		shutdownAll()
		stopDesktopTrayLoop()
	}()

	// Non-intrusive background update check so tray menu advertises available GitHub releases
	go func() {
		select {
		case <-time.After(10 * time.Second):
		case <-sigCtx.Done():
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if res, err := updater.NewUpdater(AppVersion).Check(ctx); err == nil && res != nil && res.UpdateAvailable {
			updateMu.Lock()
			latestTag = res.TagName
			if latestTag == "" && res.LatestVersion != "" {
				latestTag = "v" + res.LatestVersion
			}
			updateMu.Unlock()
		}
	}()

	if shouldOpenInitially {
		openUIWindow()
	}

	if *headlessFlag {
		<-sigCtx.Done()
		return
	}

	_ = runDesktopTrayLoop(trayCallbacks{
		OnOpenUI: openUIWindow,
		IsAutostartOn: func() bool {
			return svcMgr.UIAutostartStatus().Enabled
		},
		OnToggleAutostart: func() (bool, string, error) {
			cur := svcMgr.UIAutostartStatus().Enabled
			next := !cur
			st, err := svcMgr.SetUIAutostart(next, selfExe)
			if err != nil {
				return cur, "", err
			}
			if st.Enabled {
				return true, "Acceso directo de inicio automático registrado.", nil
			}
			return false, "Se desactivó el inicio automático al encender el PC.", nil
		},
		GetAvailableTag: func() string {
			updateMu.Lock()
			defer updateMu.Unlock()
			return latestTag
		},
		OnCheckAndUpdate: func(showBalloon func(title, msg string, flags uint32), confirm func(title, msg string) bool) {
			showBalloon("Aronium Utility", "Consultando última versión en GitHub Releases...", niifInfo)
			upd := updater.NewUpdater(AppVersion)

			chkCtx, chkCancel := context.WithTimeout(context.Background(), 30*time.Second)
			res, err := upd.Check(chkCtx)
			chkCancel()
			if err != nil {
				showBalloon("Error al buscar actualizaciones", err.Error(), niifError)
				notifyDialog("Aronium Utility — Actualización", fmt.Sprintf("No se pudo consultar GitHub Releases:\n\n%v", err), true)
				return
			}

			if !res.UpdateAvailable {
				showBalloon("Aronium Utility al día", fmt.Sprintf("Ya tienes la última versión instalada (v%s).", res.CurrentVersion), niifInfo)
				notifyDialog("Aronium Utility — Actualización", fmt.Sprintf("Ya tienes instalada la última versión disponible (v%s).", res.CurrentVersion), false)
				return
			}

			tag := res.TagName
			if tag == "" {
				tag = "v" + res.LatestVersion
			}
			updateMu.Lock()
			latestTag = tag
			updateMu.Unlock()

			question := fmt.Sprintf(
				"¡Nueva versión disponible: %s! (Instalada: v%s)\n\n¿Deseas descargar e instalar ahora?\nTus cuentas en cuentas.db y tu configuración se conservarán intactas.",
				tag, res.CurrentVersion,
			)
			if !confirm("Actualización disponible — Aronium Utility", question) {
				return
			}

			showBalloon("Actualizando Aronium Utility", fmt.Sprintf("Descargando y verificando instalador %s...", tag), niifInfo)
			applyCtx, applyCancel := context.WithTimeout(context.Background(), 5*time.Minute)
			applyRes, err := upd.DownloadAndApply(applyCtx, false, true)
			applyCancel()
			if err != nil {
				showBalloon("Error al actualizar", err.Error(), niifError)
				notifyDialog("Aronium Utility — Error de actualización", fmt.Sprintf("No se pudo completar la actualización:\n\n%v", err), true)
				return
			}
			showBalloon("Instalando actualización", applyRes.Message, niifInfo)
		},
		OnExitAll: shutdownAll,
	})
}

// locateAroniumCLISidecar finds the sibling `aronium-util.exe` / `aronium-util` sidecar binary.
func locateAroniumCLISidecar() string {
	if custom := os.Getenv("ARONIUM_CLI_BIN"); custom != "" {
		if _, err := os.Stat(custom); err == nil {
			return custom
		}
	}
	binName := "aronium-util"
	if runtime.GOOS == "windows" {
		binName = "aronium-util.exe"
	}
	if exe, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(exe), binName)
		if _, err := os.Stat(sibling); err == nil {
			return sibling
		}
	}
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		installed := filepath.Join(localAppData, "Programs", "AroniumUtility", binName)
		if _, err := os.Stat(installed); err == nil {
			return installed
		}
	}
	return binName
}

// launchNativeOSWebViewWindow opens a chromeless desktop app window using Microsoft Edge App mode on Windows.
func launchNativeOSWebViewWindow(appURL string) (*exec.Cmd, error) {
	if runtime.GOOS == "windows" {
		profileDir := filepath.Join(config.DefaultAppDataDir(), "EdgeAppProfile")
		_ = os.MkdirAll(profileDir, 0755)

		edgeCandidates := []string{
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
		}
		for _, edge := range edgeCandidates {
			if _, err := os.Stat(edge); err == nil {
				cmd := exec.Command(
					edge,
					"--app="+appURL,
					"--window-size=1260,820",
					"--user-data-dir="+profileDir,
					"--no-first-run",
					"--no-default-browser-check",
				)
				if err := cmd.Start(); err != nil {
					return nil, err
				}
				return cmd, nil
			}
		}
		cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", appURL)
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return cmd, nil
	}
	if runtime.GOOS == "darwin" {
		cmd := exec.Command("open", appURL)
		return cmd, cmd.Start()
	}
	cmd := exec.Command("xdg-open", appURL)
	return cmd, cmd.Start()
}
