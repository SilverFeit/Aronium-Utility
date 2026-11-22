package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultGitHubRepo      = "SilverFeit/Aronium-Utility"
	DefaultLatestReleaseEP = "https://api.github.com/repos/SilverFeit/Aronium-Utility/releases/latest"
)

// UpdateCheckResult contains the version comparison and asset metadata from GitHub Releases.
type UpdateCheckResult struct {
	CheckedAt            time.Time `json:"checked_at"`
	Repository           string    `json:"repository"`
	CurrentVersion       string    `json:"current_version"`
	LatestVersion        string    `json:"latest_version"`
	TagName              string    `json:"tag_name"`
	UpdateAvailable      bool      `json:"update_available"`
	ReleaseTitle         string    `json:"release_title"`
	ReleaseNotes         string    `json:"release_notes"`
	ReleaseURL           string    `json:"release_url"`
	PublishedAt          string    `json:"published_at"`
	InstallerAssetName   string    `json:"installer_asset_name"`
	InstallerDownloadURL string    `json:"installer_download_url"`
	InstallerSizeBytes   int64     `json:"installer_size_bytes"`
	SHA256SumsURL        string    `json:"sha256_sums_url,omitempty"`
}

// UpdateApplyResult reports the outcome of downloading, verifying, and launching a GitHub Release installer.
type UpdateApplyResult struct {
	AppliedAt          time.Time `json:"applied_at"`
	CurrentVersion     string    `json:"current_version"`
	TargetVersion      string    `json:"target_version"`
	UpdateAvailable    bool      `json:"update_available"`
	Downloaded         bool      `json:"downloaded"`
	InstallerPath      string    `json:"installer_path"`
	SHA256             string    `json:"sha256"`
	VerifiedSHA256     bool      `json:"verified_sha256"`
	LaunchedInstaller  bool      `json:"launched_installer"`
	Message            string    `json:"message"`
}

type githubReleasePayload struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	HTMLURL     string `json:"html_url"`
	PublishedAt string `json:"published_at"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

// Updater checks and downloads releases from GitHub Releases.
type Updater struct {
	CurrentVersion string
	ReleaseAPIURL  string
	HTTPClient     *http.Client
	DryRunLaunch   bool // when true (e.g. in tests), skips spawning the detached OS installer process
}

// NewUpdater constructs an Updater for the given current version.
func NewUpdater(currentVersion string) *Updater {
	return &Updater{
		CurrentVersion: NormalizeVersion(currentVersion),
		ReleaseAPIURL:  DefaultLatestReleaseEP,
		HTTPClient:     &http.Client{Timeout: 45 * time.Second},
	}
}

// NormalizeVersion strips leading 'v'/'V' and whitespace.
func NormalizeVersion(v string) string {
	s := strings.TrimSpace(v)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	return s
}

// CompareSemver returns -1 if a < b, 0 if a == b, and +1 if a > b.
func CompareSemver(a, b string) int {
	pa := parseSemverParts(a)
	pb := parseSemverParts(b)
	for i := 0; i < 3; i++ {
		if pa[i] < pb[i] {
			return -1
		}
		if pa[i] > pb[i] {
			return 1
		}
	}
	return 0
}

func parseSemverParts(v string) [3]int {
	var out [3]int
	clean := NormalizeVersion(v)
	if idx := strings.IndexAny(clean, "-+"); idx >= 0 {
		clean = clean[:idx]
	}
	parts := strings.Split(clean, ".")
	for i := 0; i < len(parts) && i < 3; i++ {
		if n, err := strconv.Atoi(strings.TrimSpace(parts[i])); err == nil {
			out[i] = n
		}
	}
	return out
}

func preferredInstallerAssetName() string {
	if runtime.GOARCH == "386" {
		return "aronium-util-setup-x86.exe"
	}
	return "aronium-util-setup-x64.exe"
}

// Check queries GitHub Releases API for the latest release and compares it with CurrentVersion.
func (u *Updater) Check(ctx context.Context) (*UpdateCheckResult, error) {
	endpoint := u.ReleaseAPIURL
	if strings.TrimSpace(endpoint) == "" {
		endpoint = DefaultLatestReleaseEP
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Aronium-Utility/"+u.CurrentVersion)

	resp, err := u.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("no se pudo conectar con GitHub Releases: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("GitHub Releases respondió HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var rel githubReleasePayload
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("respuesta inválida de GitHub Releases: %w", err)
	}

	latestVer := NormalizeVersion(rel.TagName)
	wantAsset := preferredInstallerAssetName()

	res := &UpdateCheckResult{
		CheckedAt:      time.Now().UTC(),
		Repository:     DefaultGitHubRepo,
		CurrentVersion: u.CurrentVersion,
		LatestVersion:  latestVer,
		TagName:        rel.TagName,
		UpdateAvailable: CompareSemver(u.CurrentVersion, latestVer) < 0,
		ReleaseTitle:   rel.Name,
		ReleaseNotes:   rel.Body,
		ReleaseURL:     rel.HTMLURL,
		PublishedAt:    rel.PublishedAt,
	}

	for _, a := range rel.Assets {
		switch {
		case strings.EqualFold(a.Name, wantAsset):
			res.InstallerAssetName = a.Name
			res.InstallerDownloadURL = a.BrowserDownloadURL
			res.InstallerSizeBytes = a.Size
		case strings.EqualFold(a.Name, "SHA256SUMS.txt"):
			res.SHA256SumsURL = a.BrowserDownloadURL
		}
	}

	// Fallback to x64 setup asset if exact arch wasn't matched
	if res.InstallerDownloadURL == "" {
		for _, a := range rel.Assets {
			if strings.HasPrefix(strings.ToLower(a.Name), "aronium-util-setup-") && strings.HasSuffix(strings.ToLower(a.Name), ".exe") {
				res.InstallerAssetName = a.Name
				res.InstallerDownloadURL = a.BrowserDownloadURL
				res.InstallerSizeBytes = a.Size
				break
			}
		}
	}

	return res, nil
}

// DownloadAndApply checks for a newer release (unless force=true), downloads the unified Windows installer,
// verifies its SHA-256 against SHA256SUMS.txt when available, and launches the installer in detached mode.
func (u *Updater) DownloadAndApply(ctx context.Context, force bool, launchUI bool) (*UpdateApplyResult, error) {
	chk, err := u.Check(ctx)
	if err != nil {
		return nil, err
	}

	out := &UpdateApplyResult{
		AppliedAt:       time.Now().UTC(),
		CurrentVersion:  chk.CurrentVersion,
		TargetVersion:   chk.LatestVersion,
		UpdateAvailable: chk.UpdateAvailable,
	}

	if !chk.UpdateAvailable && !force {
		out.Message = fmt.Sprintf("Ya tienes instalada la versión más reciente (v%s).", chk.CurrentVersion)
		return out, nil
	}

	if chk.InstallerDownloadURL == "" {
		return nil, fmt.Errorf("la release %s no contiene el instalador '%s'", chk.TagName, preferredInstallerAssetName())
	}

	destDir := filepath.Join(os.TempDir(), "AroniumUtilityUpdate")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return nil, err
	}
	assetName := chk.InstallerAssetName
	if assetName == "" {
		assetName = preferredInstallerAssetName()
	}
	installerPath := filepath.Join(destDir, fmt.Sprintf("aronium-util-setup-%s.exe", chk.LatestVersion))

	shaHex, err := u.downloadFileWithSHA256(ctx, chk.InstallerDownloadURL, installerPath)
	if err != nil {
		return nil, fmt.Errorf("error descargando instalador desde GitHub: %w", err)
	}
	out.Downloaded = true
	out.InstallerPath = installerPath
	out.SHA256 = shaHex

	if chk.SHA256SumsURL != "" {
		expected, err := u.fetchExpectedSHA256(ctx, chk.SHA256SumsURL, assetName)
		if err == nil && expected != "" {
			if !strings.EqualFold(expected, shaHex) {
				_ = os.Remove(installerPath)
				return nil, fmt.Errorf("verificación SHA-256 falló para %s: esperado %s, recibido %s", assetName, expected, shaHex)
			}
			out.VerifiedSHA256 = true
		}
	}

	if u.DryRunLaunch {
		out.Message = fmt.Sprintf("Instalador v%s descargado y verificado en %s (modo prueba).", chk.LatestVersion, installerPath)
		return out, nil
	}

	if err := launchDetachedInstaller(installerPath, launchUI); err != nil {
		return nil, fmt.Errorf("instalador descargado en %s, pero no se pudo iniciar automáticamente: %w", installerPath, err)
	}

	out.LaunchedInstaller = true
	out.Message = fmt.Sprintf("Actualización a v%s iniciada. El instalador cerrará esta ventana, actualizará los ejecutables preservando tus cuentas y volverá a abrir Aronium Utility UI.", chk.LatestVersion)
	return out, nil
}

func (u *Updater) downloadFileWithSHA256(ctx context.Context, urlStr, destPath string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Aronium-Utility/"+u.CurrentVersion)

	client := u.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d al descargar %s", resp.StatusCode, urlStr)
	}

	tmpPath := destPath + ".part"
	_ = os.Remove(tmpPath)
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return "", err
	}

	hasher := sha256.New()
	mw := io.MultiWriter(f, hasher)
	if _, err := io.Copy(mw, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}

	_ = os.Remove(destPath)
	if err := os.Rename(tmpPath, destPath); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func (u *Updater) fetchExpectedSHA256(ctx context.Context, sumsURL, assetName string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sumsURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Aronium-Utility/"+u.CurrentVersion)

	resp, err := u.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 {
			fname := strings.TrimPrefix(fields[1], "*")
			if strings.EqualFold(filepath.Base(fname), assetName) {
				return strings.ToLower(fields[0]), nil
			}
		}
	}
	return "", nil
}

func launchDetachedInstaller(installerPath string, launchUI bool) error {
	args := []string{"--silent"}
	if launchUI {
		args = append(args, "--launch-ui")
	}

	if runtime.GOOS == "windows" {
		// Launch after a 1-second delay in an independent process so the CLI sidecar returns its JSON
		// response to Aronium Utility UI before the installer terminates aronium-utility-ui.exe.
		argStr := strings.Join(args, " ")
		cmdLine := fmt.Sprintf(`ping 127.0.0.1 -n 2 >nul && "%s" %s`, installerPath, argStr)
		cmd := exec.Command("cmd.exe", "/C", "start", "", "/B", "cmd.exe", "/C", cmdLine)
		return cmd.Start()
	}

	cmd := exec.Command(installerPath, args...)
	return cmd.Start()
}
