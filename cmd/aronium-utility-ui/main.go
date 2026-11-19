package main

import (
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
	"syscall"
	"time"
)

//go:embed all:dist
var embeddedFrontend embed.FS

func main() {
	portFlag := flag.Int("port", 0, "Local IPC & WebView port (0 = ephemeral)")
	headlessFlag := flag.Bool("headless", false, "Do not launch desktop WebView window automatically")
	ensureWatchFlag := flag.Bool("ensure-watch", true, "Ensure background aronium-util watch daemon is running")
	flag.Parse()

	cliBin := locateAroniumCLISidecar()

	// Ensure background live transaction watcher is running when UI starts
	var watchCmd *exec.Cmd
	if *ensureWatchFlag && cliBin != "" {
		watchCmd = exec.Command(cliBin, "watch")
		if err := watchCmd.Start(); err == nil {
			defer func() {
				if watchCmd.Process != nil {
					_ = watchCmd.Process.Kill()
				}
			}()
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

	distFS, err := fs.Sub(embeddedFrontend, "dist")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load embedded Svelte frontend: %v\n", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()

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

	if !*headlessFlag {
		_ = launchNativeOSWebViewWindow(uiURL)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
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

// launchNativeOSWebViewWindow opens a chromeless desktop app window using the OS WebView2 / Edge App engine on Windows.
func launchNativeOSWebViewWindow(appURL string) error {
	if runtime.GOOS == "windows" {
		edgeCandidates := []string{
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
		}
		for _, edge := range edgeCandidates {
			if _, err := os.Stat(edge); err == nil {
				return exec.Command(edge, "--app="+appURL, "--window-size=1260,820").Start()
			}
		}
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", appURL).Start()
	}
	if runtime.GOOS == "darwin" {
		return exec.Command("open", appURL).Start()
	}
	return exec.Command("xdg-open", appURL).Start()
}
