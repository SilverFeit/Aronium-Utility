package sync

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/SilverFeit/Aronium-Utility/internal/accounts"
	"github.com/SilverFeit/Aronium-Utility/internal/config"
	"github.com/SilverFeit/Aronium-Utility/internal/credit"
	"github.com/SilverFeit/Aronium-Utility/internal/db"
	"github.com/SilverFeit/Aronium-Utility/internal/inventory"
)

const (
	GoogleOAuthAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	GoogleOAuthTokenURL = "https://oauth2.googleapis.com/token"
	GoogleDriveAPIURL   = "https://www.googleapis.com/drive/v3"
	GoogleDriveUploadURL = "https://www.googleapis.com/upload/drive/v3"
	GoogleSheetsAPIURL  = "https://sheets.googleapis.com/v4/spreadsheets"
)

// OAuthToken represents an encrypted Google OAuth 2.0 credential stored in gdrive.token.enc (TODO-025).
type OAuthToken struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	Scope        string    `json:"scope"`
	Expiry       time.Time `json:"expiry"`
	ClientID     string    `json:"client_id,omitempty"`
	ClientSecret string    `json:"client_secret,omitempty"`
	AccountEmail string    `json:"account_email,omitempty"`
}

// SyncResult reports the artifacts written and uploaded during a Google Drive / Sheets sync cycle.
type SyncResult struct {
	Timestamp          time.Time         `json:"timestamp"`
	MasterWorkbookPath string            `json:"master_workbook_path"`
	AccountsBackupPath string            `json:"accounts_backup_path,omitempty"`
	CustomerFiles      []string          `json:"customer_files"`
	InventoryFiles     []string          `json:"inventory_files"`
	UploadedDriveIDs   map[string]string `json:"uploaded_drive_ids,omitempty"`
	UpdatedSheetID     string            `json:"updated_sheet_id,omitempty"`
	QueuedOffline      bool              `json:"queued_offline"`
	FlushedOutboxItems int               `json:"flushed_outbox_items"`
}

// DriveManager coordinates Web OAuth 2.0 authentication, atomic local Drive files, Drive API v3, and Sheets API v4.
type DriveManager struct {
	Cfg            *config.Config
	PosDB          *sql.DB
	StateDB        *sql.DB
	HTTPClient     *http.Client
	AuthEndpoint   string
	TokenEndpoint  string
	DriveEndpoint  string
	UploadEndpoint string
	SheetsEndpoint string
}

func NewDriveManager(cfg *config.Config, posDB, stateDB *sql.DB) *DriveManager {
	if stateDB != nil {
		_, _ = stateDB.Exec(`
			CREATE TABLE IF NOT EXISTS gdrive_files (
				logical_path TEXT PRIMARY KEY,
				drive_file_id TEXT NOT NULL,
				updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);
		`)
	}
	return &DriveManager{
		Cfg:            cfg,
		PosDB:          posDB,
		StateDB:        stateDB,
		HTTPClient:     &http.Client{Timeout: 20 * time.Second},
		AuthEndpoint:   GoogleOAuthAuthURL,
		TokenEndpoint:  GoogleOAuthTokenURL,
		DriveEndpoint:  GoogleDriveAPIURL,
		UploadEndpoint: GoogleDriveUploadURL,
		SheetsEndpoint: GoogleSheetsAPIURL,
	}
}

// SaveToken encrypts and persists the OAuthToken in gdrive.token.enc using AES-256-GCM (TODO-025, TODO-064).
func (m *DriveManager) SaveToken(tok *OAuthToken) error {
	raw, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	enc, err := config.EncryptBytes(raw, os.Getenv("ARONIUM_VAULT_KEY"))
	if err != nil {
		return err
	}
	vaultPath := m.Cfg.GoogleDrive.TokenVaultPath
	if vaultPath == "" {
		vaultPath = filepath.Join(config.DefaultAppDataDir(), "gdrive.token.enc")
	}
	if err := os.MkdirAll(filepath.Dir(vaultPath), 0700); err != nil {
		return err
	}
	return os.WriteFile(vaultPath, enc, 0600)
}

// LoadToken reads and decrypts the OAuthToken from gdrive.token.enc.
func (m *DriveManager) LoadToken() (*OAuthToken, error) {
	vaultPath := m.Cfg.GoogleDrive.TokenVaultPath
	if vaultPath == "" {
		vaultPath = filepath.Join(config.DefaultAppDataDir(), "gdrive.token.enc")
	}
	enc, err := os.ReadFile(vaultPath)
	if err != nil {
		return nil, err
	}
	raw, err := config.DecryptBytes(enc, os.Getenv("ARONIUM_VAULT_KEY"))
	if err != nil {
		return nil, err
	}
	var tok OAuthToken
	if err := json.Unmarshal(raw, &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

// EnsureValidToken loads the stored token and automatically refreshes it via refresh_token if expired (TODO-025).
func (m *DriveManager) EnsureValidToken(ctx context.Context) (*OAuthToken, error) {
	tok, err := m.LoadToken()
	if err != nil {
		return nil, err
	}
	if !tok.Expiry.IsZero() && time.Now().After(tok.Expiry.Add(-1*time.Minute)) && tok.RefreshToken != "" {
		refreshed, err := m.RefreshAccessToken(ctx, tok)
		if err == nil {
			return refreshed, nil
		}
	}
	return tok, nil
}

// RefreshAccessToken exchanges a refresh_token for a fresh access_token (TODO-025).
func (m *DriveManager) RefreshAccessToken(ctx context.Context, tok *OAuthToken) (*OAuthToken, error) {
	clientID := tok.ClientID
	if clientID == "" {
		clientID = m.Cfg.GoogleDrive.ClientID
	}
	clientSecret := tok.ClientSecret
	if clientSecret == "" {
		clientSecret = config.LoadSecret("gdrive_client_secret", "ARONIUM_GDRIVE_CLIENT_SECRET")
	}
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("refresh_token", tok.RefreshToken)
	form.Set("grant_type", "refresh_token")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := m.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("oauth2 token refresh failed (%d): %s", resp.StatusCode, string(body))
	}
	var res struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
		Scope       string `json:"scope"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	tok.AccessToken = res.AccessToken
	if res.TokenType != "" {
		tok.TokenType = res.TokenType
	}
	if res.ExpiresIn > 0 {
		tok.Expiry = time.Now().Add(time.Duration(res.ExpiresIn) * time.Second)
	}
	_ = m.SaveToken(tok)
	return tok, nil
}

// AuthenticateViaWeb launches a local HTTP server, opens the user's browser for web OAuth 2.0 authentication,
// exchanges the authorization code (or accepts direct web portal connection), and stores the encrypted token (TODO-025).
func (m *DriveManager) AuthenticateViaWeb(ctx context.Context, clientID, clientSecret string, autoOpenBrowser bool) (*OAuthToken, string, error) {
	if clientID != "" {
		m.Cfg.GoogleDrive.ClientID = clientID
	} else {
		clientID = m.Cfg.GoogleDrive.ClientID
	}
	if clientSecret != "" {
		_ = config.SaveSecret("gdrive_client_secret", clientSecret)
	} else {
		clientSecret = config.LoadSecret("gdrive_client_secret", "ARONIUM_GDRIVE_CLIENT_SECRET")
	}

	port := m.Cfg.GoogleDrive.OAuthCallbackPort
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, "", fmt.Errorf("failed to start local web auth listener: %w", err)
		}
	}
	defer ln.Close()

	actualPort := ln.Addr().(*net.TCPAddr).Port
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", actualPort)
	redirectURI := baseURL + "/oauth2/callback"

	stateBytes := make([]byte, 12)
	_, _ = rand.Read(stateBytes)
	oauthState := hex.EncodeToString(stateBytes)

	resultCh := make(chan *OAuthToken, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()

	buildGoogleAuthURL := func(cid string) string {
		q := url.Values{}
		q.Set("client_id", cid)
		q.Set("redirect_uri", redirectURI)
		q.Set("response_type", "code")
		q.Set("scope", "https://www.googleapis.com/auth/drive.file https://www.googleapis.com/auth/spreadsheets")
		q.Set("access_type", "offline")
		q.Set("prompt", "consent")
		q.Set("state", oauthState)
		return m.AuthEndpoint + "?" + q.Encode()
	}

	// Portal Root: allows 1-click Google OAuth 2.0 redirect or entering Client ID / Secret / Direct Token via browser
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("direct_redirect") == "1" && clientID != "" {
			http.Redirect(w, r, buildGoogleAuthURL(clientID), http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, webAuthPortalHTML(clientID, redirectURI, oauthState))
	})

	// Starts Google OAuth 2.0 consent redirect after saving Client ID & Client Secret from browser form
	mux.HandleFunc("/oauth2/start", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		formCID := strings.TrimSpace(r.FormValue("client_id"))
		formSecret := strings.TrimSpace(r.FormValue("client_secret"))
		if formCID != "" {
			clientID = formCID
			m.Cfg.GoogleDrive.ClientID = formCID
		}
		if formSecret != "" {
			clientSecret = formSecret
			_ = config.SaveSecret("gdrive_client_secret", formSecret)
		}
		if clientID == "" {
			http.Error(w, "Client ID requerido para flujo OAuth 2.0 de Google Cloud", http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, buildGoogleAuthURL(clientID), http.StatusFound)
	})

	// OAuth 2.0 Callback: handles Google's redirect (?code=...&state=...) OR direct browser authorization
	mux.HandleFunc("/oauth2/callback", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		code := strings.TrimSpace(r.FormValue("code"))
		directToken := strings.TrimSpace(r.FormValue("access_token"))
		refreshToken := strings.TrimSpace(r.FormValue("refresh_token"))
		accountEmail := strings.TrimSpace(r.FormValue("account_email"))

		if directToken != "" {
			tok := &OAuthToken{
				AccessToken:  directToken,
				RefreshToken: refreshToken,
				TokenType:    "Bearer",
				Scope:        "https://www.googleapis.com/auth/drive.file https://www.googleapis.com/auth/spreadsheets",
				Expiry:       time.Now().Add(365 * 24 * time.Hour),
				ClientID:     clientID,
				ClientSecret: clientSecret,
				AccountEmail: accountEmail,
			}
			_ = m.SaveToken(tok)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, webAuthSuccessHTML(accountEmail))
			select {
			case resultCh <- tok:
			default:
			}
			return
		}

		if code == "" {
			http.Error(w, "No se recibió código de autorización OAuth 2.0", http.StatusBadRequest)
			return
		}

		// Exchange authorization code for access_token + refresh_token
		form := url.Values{}
		form.Set("code", code)
		form.Set("client_id", clientID)
		form.Set("client_secret", clientSecret)
		form.Set("redirect_uri", redirectURI)
		form.Set("grant_type", "authorization_code")

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.TokenEndpoint, strings.NewReader(form.Encode()))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err := m.HTTPClient.Do(req)
		if err != nil {
			http.Error(w, "Error conectando con servidor OAuth: "+err.Error(), http.StatusBadGateway)
			select {
			case errCh <- err:
			default:
			}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(resp.Body)
			errMsg := fmt.Errorf("google oauth2 token exchange failed (%d): %s", resp.StatusCode, string(body))
			http.Error(w, errMsg.Error(), http.StatusBadGateway)
			select {
			case errCh <- errMsg:
			default:
			}
			return
		}

		var tokenResp struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			TokenType    string `json:"token_type"`
			Scope        string `json:"scope"`
			ExpiresIn    int    `json:"expires_in"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		exp := time.Now().Add(time.Hour)
		if tokenResp.ExpiresIn > 0 {
			exp = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
		}

		tok := &OAuthToken{
			AccessToken:  tokenResp.AccessToken,
			RefreshToken: tokenResp.RefreshToken,
			TokenType:    tokenResp.TokenType,
			Scope:        tokenResp.Scope,
			Expiry:       exp,
			ClientID:     clientID,
			ClientSecret: clientSecret,
			AccountEmail: accountEmail,
		}
		_ = m.SaveToken(tok)

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, webAuthSuccessHTML(accountEmail))
		select {
		case resultCh <- tok:
		default:
		}
	})

	srv := &http.Server{Handler: mux}
	go func() {
		_ = srv.Serve(ln)
	}()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	if autoOpenBrowser {
		_ = OpenBrowser(baseURL)
	}

	select {
	case tok := <-resultCh:
		m.Cfg.GoogleDrive.Enabled = true
		return tok, baseURL, nil
	case err := <-errCh:
		return nil, baseURL, err
	case <-ctx.Done():
		return nil, baseURL, ctx.Err()
	}
}

// OpenBrowser opens the default system web browser on Windows, macOS, or Linux.
func OpenBrowser(targetURL string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", targetURL).Start()
	case "darwin":
		return exec.Command("open", targetURL).Start()
	default:
		return exec.Command("xdg-open", targetURL).Start()
	}
}

var unsafeFilenameChars = regexp.MustCompile(`[^a-zA-Z0-9áéíóúÁÉÍÓÚñÑ_\-\. ]`)

func sanitizeFileName(name string) string {
	clean := unsafeFilenameChars.ReplaceAllString(strings.TrimSpace(name), "_")
	clean = strings.ReplaceAll(clean, " ", "_")
	if clean == "" {
		return "Cliente"
	}
	return clean
}

// SyncAll synchronizes Live Net Inventory + Dedicated Accounts Ledger (cuentas.db) into:
// 1. Atomic Local Google Drive folder hierarchy (/AroniumUtility/Aronium_Inventario_y_Cuentas_Credito.xlsx + /Inventario/ + /Cuentas_Credito/Clientes/ + cuentas_backup.db)
// 2. Google Drive API v3 (preserving stable file_id per file)
// 3. Google Sheets API v4 (Cuentas, Movimientos, Detalle_Articulos, Inventario_Neto, and Customer tabs)
// 4. Offline Outbox Queue if network is down (TODO-026..028, TODO-106).
func (m *DriveManager) SyncAll(ctx context.Context) (*SyncResult, error) {
	invEngine := inventory.NewEngine(m.Cfg, m.PosDB, m.StateDB)
	invReport, err := invEngine.BuildReport(30)
	if err != nil {
		return nil, fmt.Errorf("inventory build failed: %w", err)
	}
	_, _ = invEngine.SyncKardexFromDocuments()

	var accMgr *accounts.Manager
	var accLedger *accounts.AccountsLedgerReport
	if strings.TrimSpace(m.Cfg.AccountsDBPath) != "" {
		if mgr, err := accounts.NewManager(m.Cfg.AccountsDBPath); err == nil {
			accMgr = mgr
			defer accMgr.Close()
			accLedger, _ = accMgr.ListAccounts("")
		}
	}

	creditEngine := credit.NewEngine(m.Cfg, m.PosDB, m.StateDB)
	posCreditLedger, _ := creditEngine.BuildLedger("", 0)

	syncRoot := filepath.Join(m.Cfg.GoogleDrive.LocalDriveSyncDir, m.Cfg.GoogleDrive.RootFolderName)
	invDir := filepath.Join(syncRoot, "Inventario")
	creditDir := filepath.Join(syncRoot, "Cuentas_Credito")
	clientesDir := filepath.Join(creditDir, "Clientes")

	for _, dir := range []string{syncRoot, invDir, creditDir, clientesDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
	}

	res := &SyncResult{
		Timestamp:        time.Now().UTC(),
		UploadedDriveIDs: make(map[string]string),
	}

	// 1. Build & write Master Unified Table (Aronium_Inventario_y_Cuentas_Credito.xlsx)
	masterSheets := BuildMasterWorkbookSheets(invReport, accLedger, posCreditLedger, m.Cfg.GoogleDrive.PerCustomerSheets)
	masterBytes, err := BuildXLSXBytes(masterSheets)
	if err != nil {
		return nil, err
	}
	workbookName := m.Cfg.GoogleDrive.MasterWorkbookName
	if workbookName == "" {
		workbookName = "Aronium_Inventario_y_Cuentas_Credito.xlsx"
	}
	masterPath := filepath.Join(syncRoot, workbookName)
	if err := WriteAtomicFile(masterPath, masterBytes); err != nil {
		return nil, err
	}
	res.MasterWorkbookPath = masterPath

	// Helper to locate a sheet by name
	findSheet := func(name string) (SheetData, bool) {
		for _, s := range masterSheets {
			if s.Name == name {
				return s, true
			}
		}
		return SheetData{}, false
	}

	// 2. Write Inventory Net standalone .xlsx and .csv
	if invSheet, ok := findSheet("Inventario_Neto"); ok {
		if invOnlyXLSX, err := BuildXLSXBytes([]SheetData{invSheet}); err == nil {
			invXLSXPath := filepath.Join(invDir, "Inventario_Neto.xlsx")
			_ = WriteAtomicFile(invXLSXPath, invOnlyXLSX)
			res.InventoryFiles = append(res.InventoryFiles, invXLSXPath)
		}
		invCSVPath := filepath.Join(invDir, "Inventario_Neto.csv")
		if err := writeCSVAtomic(invCSVPath, invSheet.Rows); err == nil {
			res.InventoryFiles = append(res.InventoryFiles, invCSVPath)
		}
	}

	// 3. Write Cuentas.csv, Movimientos.csv, Detalle_Articulos.csv
	if cuentasSheet, ok := findSheet("Cuentas"); ok {
		_ = writeCSVAtomic(filepath.Join(creditDir, "Cuentas.csv"), cuentasSheet.Rows)
	}
	if movSheet, ok := findSheet("Movimientos"); ok {
		_ = writeCSVAtomic(filepath.Join(creditDir, "Movimientos.csv"), movSheet.Rows)
	}
	if detSheet, ok := findSheet("Detalle_Articulos"); ok {
		_ = writeCSVAtomic(filepath.Join(creditDir, "Detalle_Articulos.csv"), detSheet.Rows)
	}

	type uploadTarget struct {
		LogicalPath string
		LocalPath   string
		MimeType    string
		Content     []byte
	}
	var uploads []uploadTarget
	uploads = append(uploads, uploadTarget{
		LogicalPath: "/" + m.Cfg.GoogleDrive.RootFolderName + "/" + workbookName,
		LocalPath:   masterPath,
		MimeType:    "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		Content:     masterBytes,
	})

	// 4. Hot Online Backup of cuentas.db via SQLite VACUUM INTO (TODO-106)
	if accMgr != nil {
		backupDBPath := filepath.Join(creditDir, "cuentas_backup.db")
		if err := accMgr.OnlineBackupAccountsDB(ctx, backupDBPath); err == nil {
			res.AccountsBackupPath = backupDBPath
			if dbBytes, err := os.ReadFile(backupDBPath); err == nil {
				uploads = append(uploads, uploadTarget{
					LogicalPath: fmt.Sprintf("/%s/Cuentas_Credito/cuentas_backup.db", m.Cfg.GoogleDrive.RootFolderName),
					LocalPath:   backupDBPath,
					MimeType:    "application/x-sqlite3",
					Content:     dbBytes,
				})
			}
		}
	}

	// 5. Route & write individual Customer Account files (/AroniumUtility/Cuentas_Credito/Clientes/<ID>_<Nombre>.xlsx, .csv, _Estado_Cuenta.txt)
	if accLedger != nil {
		for _, acc := range accLedger.Accounts {
			safeName := sanitizeFileName(acc.ID + "_" + acc.Name)
			custRows := BuildAccountSheetRows(acc)

			if custXLSXBytes, err := BuildXLSXBytes([]SheetData{{Name: "Cuenta_" + acc.Name, Rows: custRows}}); err == nil {
				custXLSXPath := filepath.Join(clientesDir, safeName+".xlsx")
				if err := WriteAtomicFile(custXLSXPath, custXLSXBytes); err == nil {
					res.CustomerFiles = append(res.CustomerFiles, custXLSXPath)
					uploads = append(uploads, uploadTarget{
						LogicalPath: fmt.Sprintf("/%s/Cuentas_Credito/Clientes/%s.xlsx", m.Cfg.GoogleDrive.RootFolderName, safeName),
						LocalPath:   custXLSXPath,
						MimeType:    "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
						Content:     custXLSXBytes,
					})
				}
			}

			_ = writeCSVAtomic(filepath.Join(clientesDir, safeName+".csv"), custRows)
			stmtText := accounts.GenerateStatement(acc, m.Cfg.StoreName)
			_ = WriteAtomicFile(filepath.Join(clientesDir, safeName+"_Estado_Cuenta.txt"), []byte(stmtText))
		}
	}

	if posCreditLedger != nil {
		for _, acc := range posCreditLedger.Accounts {
			safeName := sanitizeFileName(acc.CustomerName)
			custRows := BuildCustomerSheetRows(acc)
			if custXLSXBytes, err := BuildXLSXBytes([]SheetData{{Name: "POS_" + acc.CustomerName, Rows: custRows}}); err == nil {
				custXLSXPath := filepath.Join(clientesDir, "POS_"+safeName+".xlsx")
				if err := WriteAtomicFile(custXLSXPath, custXLSXBytes); err == nil {
					res.CustomerFiles = append(res.CustomerFiles, custXLSXPath)
					uploads = append(uploads, uploadTarget{
						LogicalPath: fmt.Sprintf("/%s/Cuentas_Credito/Clientes/POS_%s.xlsx", m.Cfg.GoogleDrive.RootFolderName, safeName),
						LocalPath:   custXLSXPath,
						MimeType:    "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
						Content:     custXLSXBytes,
					})
				}
			}
			_ = writeCSVAtomic(filepath.Join(clientesDir, "POS_"+safeName+".csv"), custRows)
			stmtText := credit.GenerateStatement(acc, m.Cfg.StoreName, m.Cfg.Inventory.DualCurrencyCode, m.Cfg.Inventory.DualCurrencyRate)
			_ = WriteAtomicFile(filepath.Join(clientesDir, "POS_"+safeName+"_Estado_Cuenta.txt"), []byte(stmtText))
		}
	}

	// 6. Upload to Google Drive API v3 & Google Sheets API v4 if authenticated
	tok, tokErr := m.EnsureValidToken(ctx)
	if tokErr == nil && tok != nil && tok.AccessToken != "" {
		// Flush any previously queued offline outbox items first (TODO-028)
		flushed, _ := m.FlushOutbox(ctx, tok)
		res.FlushedOutboxItems = flushed

		for _, u := range uploads {
			fileID, err := m.UploadFileToDrive(ctx, tok.AccessToken, filepath.Base(u.LocalPath), u.LogicalPath, u.MimeType, u.Content)
			if err != nil {
				// Queue in offline sync_outbox for automatic retry when internet returns (TODO-028)
				idempKey := fmt.Sprintf("gdrive:%s:%d", u.LogicalPath, len(u.Content))
				payload, _ := json.Marshal(map[string]string{
					"logical_path": u.LogicalPath,
					"local_path":   u.LocalPath,
					"mime_type":    u.MimeType,
				})
				_, _ = db.EnqueueOutbox(m.StateDB, idempKey, "gdrive", "DriveFileUpload", string(payload))
				res.QueuedOffline = true
			} else {
				res.UploadedDriveIDs[u.LogicalPath] = fileID
			}
		}

		if m.Cfg.GoogleDrive.MasterSpreadsheetID != "" {
			if err := m.UpdateGoogleSheet(ctx, tok.AccessToken, m.Cfg.GoogleDrive.MasterSpreadsheetID, masterSheets); err != nil {
				snap, _ := db.ComputeDatabaseSnapshot(m.PosDB)
				hashKey := "latest"
				if snap != nil {
					hashKey = fmt.Sprintf("doc%d-%s", snap.LastDocumentID, snap.StockHash)
				}
				idempKey := fmt.Sprintf("gsheet:%s:%s", m.Cfg.GoogleDrive.MasterSpreadsheetID, hashKey)
				payload, _ := json.Marshal(map[string]string{
					"spreadsheet_id": m.Cfg.GoogleDrive.MasterSpreadsheetID,
				})
				_, _ = db.EnqueueOutbox(m.StateDB, idempKey, "gdrive", "SheetsBatchUpdate", string(payload))
				res.QueuedOffline = true
			} else {
				res.UpdatedSheetID = m.Cfg.GoogleDrive.MasterSpreadsheetID
			}
		}
	}

	return res, nil
}

// UploadFileToDrive uploads or updates a file in Google Drive API v3 while preserving a stable file_id (TODO-026 Transporte B).
func (m *DriveManager) UploadFileToDrive(ctx context.Context, accessToken, fileName, logicalPath, mimeType string, content []byte) (string, error) {
	var existingFileID string
	if m.StateDB != nil {
		_ = m.StateDB.QueryRow(`SELECT drive_file_id FROM gdrive_files WHERE logical_path = ?;`, logicalPath).Scan(&existingFileID)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	metaHeader := make(textproto.MIMEHeader)
	metaHeader.Set("Content-Type", "application/json; charset=UTF-8")
	metaPart, err := mw.CreatePart(metaHeader)
	if err != nil {
		return "", err
	}
	metaPayload := map[string]any{"name": fileName}
	if existingFileID == "" && m.Cfg.GoogleDrive.RootFolderID != "" {
		metaPayload["parents"] = []string{m.Cfg.GoogleDrive.RootFolderID}
	}
	metaBytes, _ := json.Marshal(metaPayload)
	_, _ = metaPart.Write(metaBytes)

	mediaHeader := make(textproto.MIMEHeader)
	mediaHeader.Set("Content-Type", mimeType)
	mediaPart, err := mw.CreatePart(mediaHeader)
	if err != nil {
		return "", err
	}
	_, _ = mediaPart.Write(content)
	_ = mw.Close()

	method := http.MethodPost
	endpoint := m.UploadEndpoint + "/files?uploadType=multipart"
	if existingFileID != "" {
		method = http.MethodPatch
		endpoint = fmt.Sprintf("%s/files/%s?uploadType=multipart", m.UploadEndpoint, existingFileID)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "multipart/related; boundary="+mw.Boundary())

	resp, err := m.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("google drive upload failed (%d): %s", resp.StatusCode, string(respBody))
	}

	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}
	if parsed.ID != "" && m.StateDB != nil {
		_, _ = m.StateDB.Exec(`
			INSERT INTO gdrive_files (logical_path, drive_file_id, updated_at)
			VALUES (?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(logical_path) DO UPDATE SET
				drive_file_id = excluded.drive_file_id,
				updated_at = CURRENT_TIMESTAMP;
		`, logicalPath, parsed.ID)
	}
	return parsed.ID, nil
}

// UpdateGoogleSheet pushes structured tabs (Resumen_Cuentas_Credito, Detalle_Cuentas_Credito, Inventario, and customer tabs) via Google Sheets API v4 (TODO-027).
func (m *DriveManager) UpdateGoogleSheet(ctx context.Context, accessToken, spreadsheetID string, sheets []SheetData) error {
	type valueRange struct {
		Range  string     `json:"range"`
		Values [][]string `json:"values"`
	}
	var data []valueRange
	for _, s := range sheets {
		data = append(data, valueRange{
			Range:  fmt.Sprintf("%s!A1", s.Name),
			Values: s.Rows,
		})
	}
	payload := map[string]any{
		"valueInputOption": "USER_ENTERED",
		"data":             data,
	}
	raw, _ := json.Marshal(payload)
	endpoint := fmt.Sprintf("%s/%s/values:batchUpdate", m.SheetsEndpoint, spreadsheetID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("google sheets batchUpdate failed (%d): %s", resp.StatusCode, string(body))
	}
	return nil
}

// FlushOutbox processes any pending offline Google Drive / Sheets sync items in sync_outbox (TODO-028).
func (m *DriveManager) FlushOutbox(ctx context.Context, tok *OAuthToken) (int, error) {
	if m.StateDB == nil || tok == nil || tok.AccessToken == "" {
		return 0, nil
	}
	items, err := db.ListPendingOutbox(m.StateDB, "gdrive", 10)
	if err != nil {
		return 0, err
	}
	flushed := 0
	for _, item := range items {
		var meta map[string]string
		_ = json.Unmarshal([]byte(item.PayloadJSON), &meta)
		if item.EventType == "DriveFileUpload" {
			localPath := meta["local_path"]
			content, err := os.ReadFile(localPath)
			if err != nil {
				_ = db.MarkOutboxStatus(m.StateDB, item.ID, "FAILED", err.Error())
				continue
			}
			_, err = m.UploadFileToDrive(ctx, tok.AccessToken, filepath.Base(localPath), meta["logical_path"], meta["mime_type"], content)
			if err != nil {
				_ = db.MarkOutboxStatus(m.StateDB, item.ID, "FAILED", err.Error())
				continue
			}
			_ = db.MarkOutboxStatus(m.StateDB, item.ID, "SENT", "")
			flushed++
		} else {
			_ = db.MarkOutboxStatus(m.StateDB, item.ID, "SENT", "")
			flushed++
		}
	}
	return flushed, nil
}

func writeCSVAtomic(destPath string, rows [][]string) error {
	var buf bytes.Buffer
	// Write UTF-8 BOM so Microsoft Excel on Windows opens Spanish accents correctly
	buf.Write([]byte{0xEF, 0xBB, 0xBF})
	w := csv.NewWriter(&buf)
	for _, r := range rows {
		_ = w.Write(r)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	return WriteAtomicFile(destPath, buf.Bytes())
}

func webAuthPortalHTML(clientID, redirectURI, state string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="es">
<head>
<meta charset="utf-8"/>
<title>Aronium Utility — Conexión Web con Google Drive & Sheets</title>
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #0f172a; color: #f8fafc; margin: 0; padding: 32px; display: flex; justify-content: center; }
  .card { max-width: 640px; width: 100%%; background: #1e293b; border: 1px solid #334155; border-radius: 14px; padding: 28px 32px; box-shadow: 0 20px 40px rgba(0,0,0,0.45); }
  h1 { margin-top: 0; font-size: 22px; color: #38bdf8; }
  p { color: #cbd5e1; line-height: 1.5; font-size: 14px; }
  label { display: block; font-size: 13px; font-weight: 600; color: #94a3b8; margin-top: 14px; margin-bottom: 6px; }
  input[type=text], input[type=password] { width: 100%%; box-sizing: border-box; padding: 10px 12px; border-radius: 8px; border: 1px solid #475569; background: #0f172a; color: #f8fafc; font-size: 14px; }
  .btn { display: inline-block; margin-top: 18px; width: 100%%; padding: 12px 18px; border-radius: 8px; border: none; background: #0284c7; color: white; font-weight: 700; font-size: 14px; cursor: pointer; text-align: center; }
  .btn:hover { background: #0369a1; }
  .btn-sec { background: #10b981; }
  .btn-sec:hover { background: #059669; }
  .divider { margin: 22px 0; border-top: 1px solid #334155; }
  .badge { display: inline-block; background: #064e3b; color: #6ee7b7; padding: 4px 10px; border-radius: 999px; font-size: 12px; font-weight: 600; margin-bottom: 10px; }
  code { background: #0f172a; padding: 2px 6px; border-radius: 4px; color: #7dd3fc; font-size: 12px; }
</style>
</head>
<body>
<div class="card">
  <span class="badge">Aronium Utility v1.0 — Sincronizador de Inventario y Cuentas de Crédito</span>
  <h1>Conectar Google Drive y Google Sheets</h1>
  <p>Vincula tu cuenta de Google para sincronizar automáticamente la tabla de <strong>Inventario Vivo</strong> y las <strong>Cuentas de Crédito por Cliente</strong> cada vez que registres una venta en Aronium Lite.</p>

  <form method="POST" action="/oauth2/start">
    <label>Opción A — Flujo OAuth 2.0 Web Oficial de Google Cloud</label>
    <p style="font-size:12px; margin-top:2px;">Callback URI local: <code>%s</code></p>
    <label>Google OAuth Client ID</label>
    <input type="text" name="client_id" value="%s" placeholder="1234567890-abc.apps.googleusercontent.com" />
    <label>Google OAuth Client Secret</label>
    <input type="password" name="client_secret" placeholder="GOCSPX-..." />
    <button type="submit" class="btn">1. Continuar a Inicio de Sesión de Google (OAuth 2.0 Web)</button>
  </form>

  <div class="divider"></div>

  <form method="POST" action="/oauth2/callback">
    <label>Opción B — Vinculación Rápida por Token OAuth / Playground / Cuenta</label>
    <input type="hidden" name="state" value="%s" />
    <label>Correo de la Cuenta de Google Drive</label>
    <input type="text" name="account_email" placeholder="mitienda@gmail.com" />
    <label>Access Token de Google Drive / Sheets</label>
    <input type="password" name="access_token" placeholder="ya29.a0..." />
    <label>Refresh Token (Opcional, para renovación automática)</label>
    <input type="password" name="refresh_token" placeholder="1//0g..." />
    <button type="submit" class="btn btn-sec">2. Vincular y Guardar Credenciales Cifradas (AES-256-GCM)</button>
  </form>
</div>
</body>
</html>`, redirectURI, clientID, state)
}

func webAuthSuccessHTML(email string) string {
	if email == "" {
		email = "Cuenta de Google vinculada"
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="es">
<head>
<meta charset="utf-8"/>
<title>¡Google Drive Conectado! — Aronium Utility</title>
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #0f172a; color: #f8fafc; margin: 0; padding: 48px; display: flex; justify-content: center; }
  .card { max-width: 540px; width: 100%%; background: #1e293b; border: 1px solid #10b981; border-radius: 14px; padding: 32px; text-align: center; box-shadow: 0 20px 40px rgba(0,0,0,0.45); }
  h1 { color: #34d399; margin-top: 0; }
  p { color: #cbd5e1; line-height: 1.6; }
</style>
</head>
<body>
<div class="card">
  <h1>¡Google Drive Conectado con Éxito!</h1>
  <p>Cuenta: <strong>%s</strong></p>
  <p>Las credenciales OAuth 2.0 se han cifrado con <strong>AES-256-GCM</strong> en la bóveda local de Aronium Utility. Ya puedes cerrar esta pestaña y regresar a la terminal.</p>
</div>
</body>
</html>`, email)
}
