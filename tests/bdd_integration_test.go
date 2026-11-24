package tests

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SilverFeit/Aronium-Utility/internal/accounts"
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
	gosync "github.com/SilverFeit/Aronium-Utility/internal/sync"
	"github.com/SilverFeit/Aronium-Utility/internal/testutil"
	"github.com/SilverFeit/Aronium-Utility/internal/updater"
)

func setupTestEnv(t *testing.T) (*config.Config, string) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("ARONIUM_UTIL_HOME", tmpDir)
	posDBPath := filepath.Join(tmpDir, "pos.db")
	stateDBPath := filepath.Join(tmpDir, "aronium_utility_state.db")

	if err := testutil.CreateSyntheticPosDB(posDBPath); err != nil {
		t.Fatalf("CreateSyntheticPosDB failed: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.StoreID = "STORE-TEST-01"
	cfg.StoreName = "Minimercado Central"
	cfg.CashRegisterID = "POS-01"
	cfg.PosDBPath = posDBPath
	cfg.LocalPosCopyPath = filepath.Join(tmpDir, "pos_local_copy.db")
	cfg.AccountsDBPath = filepath.Join(tmpDir, "cuentas.db")
	cfg.StateDBPath = stateDBPath
	cfg.GoogleDrive.TokenVaultPath = filepath.Join(tmpDir, "gdrive.token.enc")
	cfg.GoogleDrive.LocalDriveSyncDir = filepath.Join(tmpDir, "GoogleDriveSync")
	cfg.Backup.BackupDir = filepath.Join(tmpDir, "backups")
	return cfg, tmpDir
}

// BDD Scenario 1 & 3: Customer Credit Account Routing by Name, Split Payments, Partial Payments & CLI Notes (TODO-017..021, TODO-059)
func TestBDD_CustomerCreditRoutingAndPaymentRecalculation(t *testing.T) {
	cfg, _ := setupTestEnv(t)

	posConn, err := db.OpenPosDBReadOnly(cfg.PosDBPath)
	if err != nil {
		t.Fatalf("OpenPosDBReadOnly: %v", err)
	}
	defer posConn.Close()

	stateConn, err := db.OpenStateDB(cfg.StateDBPath)
	if err != nil {
		t.Fatalf("OpenStateDB: %v", err)
	}
	defer stateConn.Close()

	// Test ATTACH DATABASE read-only cross-query (TODO-011)
	if err := db.AttachPosDBReadOnly(stateConn, cfg.PosDBPath); err != nil {
		t.Fatalf("AttachPosDBReadOnly: %v", err)
	}
	var crossCount int
	if err := stateConn.QueryRow(`SELECT COUNT(*) FROM aronium_pos.Customer;`).Scan(&crossCount); err != nil || crossCount < 3 {
		t.Fatalf("expected >= 3 customers via ATTACH DATABASE, got %d (err=%v)", crossCount, err)
	}
	_ = db.DetachPosDB(stateConn)

	// Add CLI note and custom credit limit for Pedro Pérez (CustomerId=2)
	_, err = db.AddCreditNote(stateConn, db.CreditNote{
		CustomerID:   2,
		CustomerName: "Pedro Pérez",
		Note:         "Compromiso de pago el viernes",
		DuePromise:   "2026-11-20",
	})
	if err != nil {
		t.Fatalf("AddCreditNote: %v", err)
	}
	if err := db.SetCustomerCreditLimit(stateConn, 2, "Pedro Pérez", 25000); err != nil {
		t.Fatalf("SetCustomerCreditLimit: %v", err)
	}

	eng := credit.NewEngine(cfg, posConn, stateConn)
	ledger, err := eng.BuildLedger("", 0)
	if err != nil {
		t.Fatalf("BuildLedger: %v", err)
	}

	if ledger.TotalPendingPortfolio != 50000 {
		t.Fatalf("expected initial portfolio $50000, got %.2f", ledger.TotalPendingPortfolio)
	}

	// Pass a new credit bill for Pedro Pérez ($11000 Aceite) -> pushes balance to $31000 (> $25000 limit -> LIMITE_EXCEDIDO)
	newDocID, err := testutil.SimulateNewCreditSale(cfg.PosDBPath, "20-200-000004", 2, 2, 1, 11000, "1 litro de aceite a crédito", "POS-01")
	if err != nil {
		t.Fatalf("SimulateNewCreditSale: %v", err)
	}

	ledgerAfterSale, err := eng.BuildLedger("Pedro", 0)
	if err != nil || len(ledgerAfterSale.Accounts) != 1 {
		t.Fatalf("BuildLedger for Pedro failed: %v", err)
	}
	pedro := ledgerAfterSale.Accounts[0]
	if pedro.PendingBalance != 31000 {
		t.Fatalf("expected Pedro balance $31000, got %.2f", pedro.PendingBalance)
	}
	if !pedro.ExceedsCreditLimit || pedro.AccountStatus != "LIMITE_EXCEDIDO" {
		t.Fatalf("expected Pedro status LIMITE_EXCEDIDO, got %s", pedro.AccountStatus)
	}

	// Simulate partial payments by Pedro (TODO-019)
	if err := testutil.SimulateCustomerPayment(cfg.PosDBPath, newDocID, 15000, false); err != nil {
		t.Fatalf("SimulateCustomerPayment: %v", err)
	}
	if err := testutil.SimulateCustomerPayment(cfg.PosDBPath, 2, 10000, false); err != nil {
		t.Fatalf("SimulateCustomerPayment Doc 2: %v", err)
	}

	ledgerAfterPay, err := eng.BuildLedger("Pedro", 0)
	if err != nil {
		t.Fatalf("BuildLedger after payment: %v", err)
	}
	pedroAfter := ledgerAfterPay.Accounts[0]
	if pedroAfter.PendingBalance != 10000 {
		t.Fatalf("expected Pedro balance $10000 after payments, got %.2f", pedroAfter.PendingBalance)
	}

	stmt := credit.GenerateStatement(pedroAfter, cfg.StoreName, "EUR", 1.08)
	if !strings.Contains(stmt, "Pedro Pérez") || !strings.Contains(stmt, "Compromiso de pago el viernes") {
		t.Fatalf("statement missing customer details or CLI note:\n%s", stmt)
	}
}

// BDD Scenario 2: Direct Web OAuth 2.0 Authentication & Unified Inventory + Credit Accounts Table Sync to Google Drive & Sheets (TODO-025..028)
func TestBDD_GoogleDriveWebOAuthAndUnifiedTableSync(t *testing.T) {
	cfg, _ := setupTestEnv(t)

	posConn, err := db.OpenPosDBReadOnly(cfg.PosDBPath)
	if err != nil {
		t.Fatalf("OpenPosDBReadOnly: %v", err)
	}
	defer posConn.Close()

	stateConn, err := db.OpenStateDB(cfg.StateDBPath)
	if err != nil {
		t.Fatalf("OpenStateDB: %v", err)
	}
	defer stateConn.Close()

	uploadedFiles := make(map[string]int)
	var sheetBatchUpdates int
	var mu sync.Mutex

	mockGoogle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasPrefix(r.URL.Path, "/token"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "ya29.mock_access_token_123",
				"refresh_token": "1//mock_refresh_token_456",
				"token_type":    "Bearer",
				"expires_in":    3600,
				"scope":         "https://www.googleapis.com/auth/drive.file https://www.googleapis.com/auth/spreadsheets",
			})
		case strings.HasPrefix(r.URL.Path, "/upload/drive/v3/files"):
			uploadedFiles[r.Method]++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": fmt.Sprintf("drive_file_id_%d", len(uploadedFiles)),
			})
		case strings.Contains(r.URL.Path, "/values:batchUpdate"):
			sheetBatchUpdates++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"totalUpdatedCells": 120})
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockGoogle.Close()

	cfg.GoogleDrive.MasterSpreadsheetID = "sheet_master_123"
	cfg.GoogleDrive.OAuthCallbackPort = 18765
	driveMgr := gosync.NewDriveManager(cfg, posConn, stateConn)
	driveMgr.TokenEndpoint = mockGoogle.URL + "/token"
	driveMgr.UploadEndpoint = mockGoogle.URL + "/upload/drive/v3"
	driveMgr.SheetsEndpoint = mockGoogle.URL + "/v4/spreadsheets"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Verify ParseGoogleCredentialsJSON for both "installed" and "web" Google Cloud credential formats
	if cid, csec, pErr := gosync.ParseGoogleCredentialsJSON([]byte(`{"installed":{"client_id":"inst-123.apps.googleusercontent.com","client_secret":"GOCSPX-inst"}}`)); pErr != nil || cid != "inst-123.apps.googleusercontent.com" || csec != "GOCSPX-inst" {
		t.Fatalf("ParseGoogleCredentialsJSON (installed) failed: cid=%q sec=%q err=%v", cid, csec, pErr)
	}
	if cid, csec, pErr := gosync.ParseGoogleCredentialsJSON([]byte(`{"web":{"client_id":"web-456.apps.googleusercontent.com","client_secret":"GOCSPX-web"}}`)); pErr != nil || cid != "web-456.apps.googleusercontent.com" || csec != "GOCSPX-web" {
		t.Fatalf("ParseGoogleCredentialsJSON (web) failed: cid=%q sec=%q err=%v", cid, csec, pErr)
	}

	doneCh := make(chan error, 1)
	go func() {
		_, _, err := driveMgr.AuthenticateViaWeb(ctx, "test-client-id.apps.googleusercontent.com", "GOCSPX-test-secret", false)
		doneCh <- err
	}()

	time.Sleep(120 * time.Millisecond)

	// Verify simplified portal HTML: direct Cloud Console/Playground links, "Copiar URI", credentials.json upload, collapsible Opción B & 1-click redirect
	portalResp, err := http.Get("http://127.0.0.1:18765/")
	if err != nil {
		t.Fatalf("GET / webAuthPortalHTML failed: %v", err)
	}
	portalBytes, _ := io.ReadAll(portalResp.Body)
	portalResp.Body.Close()
	portalHTML := string(portalBytes)
	for _, want := range []string{
		"https://console.cloud.google.com/apis/credentials",
		"https://console.cloud.google.com/apis/library/drive.googleapis.com",
		"https://console.cloud.google.com/apis/library/sheets.googleapis.com",
		"https://developers.google.com/oauthplayground/",
		"Copiar URI",
		"http://127.0.0.1:18765/oauth2/callback",
		"¿Solo quieres probar rápido? (Avanzado)",
		"Cargar credentials.json",
		"Conectar con Google Drive (1 Clic)",
	} {
		if !strings.Contains(portalHTML, want) {
			t.Fatalf("webAuthPortalHTML missing expected simplified element %q", want)
		}
	}

	noRedirectClient := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	dirResp, err := noRedirectClient.Get("http://127.0.0.1:18765/?direct_redirect=1")
	if err != nil {
		t.Fatalf("GET /?direct_redirect=1 failed: %v", err)
	}
	dirResp.Body.Close()
	if dirResp.StatusCode != http.StatusFound || !strings.Contains(dirResp.Header.Get("Location"), "test-client-id.apps.googleusercontent.com") {
		t.Fatalf("expected 302 redirect to Google OAuth for ?direct_redirect=1, got status=%d location=%q", dirResp.StatusCode, dirResp.Header.Get("Location"))
	}

	resp, err := http.PostForm("http://127.0.0.1:18765/oauth2/callback", url.Values{
		"code":          {"4/0MockAuthCodeFromBrowser"},
		"account_email": {"tienda@comercio.org"},
	})
	if err != nil {
		t.Fatalf("OAuth web callback request failed: %v", err)
	}
	resp.Body.Close()

	if err := <-doneCh; err != nil {
		t.Fatalf("AuthenticateViaWeb returned error: %v", err)
	}

	tok, err := driveMgr.LoadToken()
	if err != nil || tok.AccessToken != "ya29.mock_access_token_123" {
		t.Fatalf("failed to load encrypted OAuth token: %v (tok=%+v)", err, tok)
	}

	syncRes, err := driveMgr.SyncAll(context.Background())
	if err != nil {
		t.Fatalf("SyncAll failed: %v", err)
	}

	if _, err := os.Stat(syncRes.MasterWorkbookPath); err != nil {
		t.Fatalf("master workbook not created at %s: %v", syncRes.MasterWorkbookPath, err)
	}

	rawXLSX, err := os.ReadFile(syncRes.MasterWorkbookPath)
	if err != nil {
		t.Fatalf("ReadFile MasterWorkbookPath: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(rawXLSX), int64(len(rawXLSX)))
	if err != nil {
		t.Fatalf("MasterWorkbookPath is not a valid OpenXML zip: %v", err)
	}
	var foundWorkbookXML bool
	for _, f := range zr.File {
		if f.Name == "xl/workbook.xml" {
			foundWorkbookXML = true
		}
	}
	if !foundWorkbookXML {
		t.Fatalf("xl/workbook.xml missing from generated .xlsx")
	}

	if len(syncRes.CustomerFiles) < 2 {
		t.Fatalf("expected individual customer files in /Cuentas_Credito/Clientes/, got %v", syncRes.CustomerFiles)
	}
	if len(syncRes.UploadedDriveIDs) == 0 {
		t.Fatalf("expected files uploaded to Google Drive API v3")
	}
	if syncRes.UpdatedSheetID != "sheet_master_123" || sheetBatchUpdates != 1 {
		t.Fatalf("expected Google Sheets API v4 batchUpdate to run once, got %d", sheetBatchUpdates)
	}

	// Second SyncAll verifies HTTP PATCH with stable file_ids
	_, err = driveMgr.SyncAll(context.Background())
	if err != nil {
		t.Fatalf("second SyncAll failed: %v", err)
	}
	mu.Lock()
	patchCount := uploadedFiles[http.MethodPatch]
	mu.Unlock()
	if patchCount == 0 {
		t.Fatalf("expected second SyncAll to use HTTP PATCH with stable file_ids, got 0 PATCH calls")
	}
}

// BDD Scenario 4 & 5: Multi-Register Identity (TC-029), Hub Consolidation, Last-Unit Conflict (TC-021), Catalog Edit Conflict (TC-022), and Offline Reconnection (TC-015..016, TC-023..024)
func TestBDD_MultiStationHubConflictsAndOfflineResilience(t *testing.T) {
	cfg, tmpDir := setupTestEnv(t)

	posConn, err := db.OpenPosDBReadOnly(cfg.PosDBPath)
	if err != nil {
		t.Fatalf("OpenPosDBReadOnly: %v", err)
	}
	defer posConn.Close()

	stateConn, err := db.OpenStateDB(cfg.StateDBPath)
	if err != nil {
		t.Fatalf("OpenStateDB: %v", err)
	}
	defer stateConn.Close()

	regMgr := registers.NewManager(posConn, stateConn, cfg.StoreID)
	if _, err := regMgr.ClaimIdentity("POS-01", "Caja Principal", "PC-CAJA-01", false); err != nil {
		t.Fatalf("ClaimIdentity POS-01 on PC-CAJA-01 failed: %v", err)
	}
	if _, err := regMgr.ClaimIdentity("POS-01", "Caja Duplicada", "PC-CAJA-02", false); err == nil {
		t.Fatalf("expected TC-029 duplicate cash register identity conflict error, got nil")
	}

	pos01Sales, err := regMgr.QuerySales("POS-01", "POS-01", "", "")
	if err != nil || len(pos01Sales) != 1 || pos01Sales[0].TransactionCount != 2 {
		t.Fatalf("expected 2 sales for POS-01, got %+v (err=%v)", pos01Sales, err)
	}
	pos02Sales, err := regMgr.QuerySales("POS-01", "POS-02", "", "")
	if err != nil || len(pos02Sales) != 1 || pos02Sales[0].TransactionCount != 1 {
		t.Fatalf("expected 1 sale for POS-02, got %+v (err=%v)", pos02Sales, err)
	}

	hubDBPath := filepath.Join(tmpDir, "hub_central.db")
	hubDB, err := db.OpenStateDB(hubDBPath)
	if err != nil {
		t.Fatalf("OpenStateDB hub: %v", err)
	}
	defer hubDB.Close()

	hubSrv, err := hub.NewServer(hubDB, cfg.StoreID, "secret-hub-token-2026")
	if err != nil {
		t.Fatalf("hub.NewServer: %v", err)
	}
	if err := hubSrv.SeedMasterStock(3, "Leche Entera 1L", 1); err != nil {
		t.Fatalf("SeedMasterStock: %v", err)
	}

	httpHub := httptest.NewServer(hubSrv.Handler())
	defer httpHub.Close()

	clientPOS1 := hub.NewClient(httpHub.URL, "secret-hub-token-2026", cfg.StoreID, "POS-01", "PC-CAJA-01", stateConn)
	clientPOS2 := hub.NewClient(httpHub.URL, "secret-hub-token-2026", cfg.StoreID, "POS-02", "PC-CAJA-02", stateConn)

	salePOS1 := db.Document{
		ID: 101, Number: "POS1-101", CustomerName: "Cliente 1", Total: 4200,
		Items: []db.DocumentItem{{ProductID: 3, ProductName: "Leche Entera 1L", Quantity: 1, Price: 4200, Total: 4200}},
	}
	salePOS2 := db.Document{
		ID: 202, Number: "POS2-202", CustomerName: "Cliente 2", Total: 4200,
		Items: []db.DocumentItem{{ProductID: 3, ProductName: "Leche Entera 1L", Quantity: 1, Price: 4200, Total: 4200}},
	}

	if queued, err := clientPOS1.SubmitSale(context.Background(), salePOS1); err != nil || queued {
		t.Fatalf("POS-01 SubmitSale failed: queued=%v err=%v", queued, err)
	}
	if queued, err := clientPOS2.SubmitSale(context.Background(), salePOS2); err != nil || queued {
		t.Fatalf("POS-02 SubmitSale failed: queued=%v err=%v", queued, err)
	}
	if _, err := clientPOS1.SubmitSale(context.Background(), salePOS1); err != nil {
		t.Fatalf("duplicate SubmitSale failed: %v", err)
	}

	_, err = hubSrv.ProcessCatalogEdit(hub.CatalogEditPayload{
		RegisterID: "POS-01", ProductID: 3, Code: "LEC-003", Name: "Leche Entera 1L", Price: 4300, Cost: 3300,
		UpdatedAtUTC: time.Now().UTC().Add(-2 * time.Second),
	})
	if err != nil {
		t.Fatalf("ProcessCatalogEdit POS-01: %v", err)
	}
	conflictCat, err := hubSrv.ProcessCatalogEdit(hub.CatalogEditPayload{
		RegisterID: "POS-02", ProductID: 3, Code: "LEC-003", Name: "Leche Entera 1L Oferta", Price: 4500, Cost: 3300,
		UpdatedAtUTC: time.Now().UTC(),
	})
	if err != nil || conflictCat == nil || conflictCat.ConflictType != "CONCURRENT_CATALOG_EDIT" {
		t.Fatalf("expected CONCURRENT_CATALOG_EDIT conflict (TC-022), got %+v (err=%v)", conflictCat, err)
	}

	st, err := hubSrv.Status()
	if err != nil {
		t.Fatalf("hubSrv.Status: %v", err)
	}
	if st.TotalDocuments != 2 || st.MasterStock[3] != -1 || len(st.Conflicts) < 2 {
		t.Fatalf("unexpected Hub status: %+v", st)
	}

	offlineClient := hub.NewClient("http://127.0.0.1:59999", "secret-hub-token-2026", cfg.StoreID, "POS-01", "PC-CAJA-01", stateConn)
	offlineSale := db.Document{ID: 303, Number: "POS1-OFFLINE-303", CustomerName: "Pedro Pérez", Total: 15000}
	queued, err := offlineClient.SubmitSale(context.Background(), offlineSale)
	if err != nil || !queued {
		t.Fatalf("expected offline sale to be queued in sync_outbox (queued=%v, err=%v)", queued, err)
	}

	offlineClient.ServerURL = httpHub.URL
	reconciled, err := offlineClient.ReconcileOutbox(context.Background())
	if err != nil || reconciled != 1 {
		t.Fatalf("expected 1 offline sale reconciled upon Hub reconnection, got %d (err=%v)", reconciled, err)
	}
}

// BDD Scenario 6 & 7: Concurrency Stress (Live Writes + Watcher + Encrypted Hot Backup), Tamper Detection & Verified Restore (TODO-038..041, TODO-060, TC-017, TC-025)
func TestBDD_ConcurrentWritesWatcherAndVerifiedHotBackupRestore(t *testing.T) {
	cfg, tmpDir := setupTestEnv(t)

	posConn, err := db.OpenPosDBReadOnly(cfg.PosDBPath)
	if err != nil {
		t.Fatalf("OpenPosDBReadOnly: %v", err)
	}
	defer posConn.Close()

	stateConn, err := db.OpenStateDB(cfg.StateDBPath)
	if err != nil {
		t.Fatalf("OpenStateDB: %v", err)
	}
	defer stateConn.Close()

	bus := events.NewBus(cfg.Hooks)
	var eventCount int
	var evMu sync.Mutex
	bus.SubscribeAll(func(ctx context.Context, ev events.Event) error {
		evMu.Lock()
		eventCount++
		evMu.Unlock()
		return nil
	})
	watcher := events.NewWatcher(cfg, posConn, stateConn, bus)
	backupMgr := backup.NewManager(cfg)

	var wg sync.WaitGroup
	errCh := make(chan error, 30)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; i <= 10; i++ {
			docNum := fmt.Sprintf("20-200-STRESS-%03d", i)
			if _, err := testutil.SimulateNewCreditSale(cfg.PosDBPath, docNum, 2, 1, 1, 4500, "Venta concurrente", "POS-01"); err != nil {
				errCh <- fmt.Errorf("concurrent write %d failed: %w", i, err)
				return
			}
			time.Sleep(15 * time.Millisecond)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 6; i++ {
			if _, err := watcher.PollOnce(context.Background()); err != nil {
				errCh <- fmt.Errorf("concurrent watcher poll failed: %w", err)
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(40 * time.Millisecond)
		if _, err := backupMgr.Create(context.Background(), "clave-secreta-tienda-2026"); err != nil {
			errCh <- fmt.Errorf("concurrent hot backup failed: %w", err)
			return
		}
	}()

	wg.Wait()
	close(errCh)
	for e := range errCh {
		t.Fatalf("concurrency stress error (TODO-060): %v", e)
	}

	_, _ = watcher.PollOnce(context.Background())
	if eventCount == 0 {
		t.Fatalf("expected watcher to emit events during concurrent sales")
	}

	finalManifest, err := backupMgr.Create(context.Background(), "clave-secreta-tienda-2026")
	if err != nil || finalManifest == nil {
		t.Fatalf("final backup create failed: %v", err)
	}

	verMsg, err := backupMgr.Verify(finalManifest.ArchivePath, "clave-secreta-tienda-2026")
	if err != nil || !strings.Contains(verMsg, "OK") {
		t.Fatalf("backup Verify failed: %v (%s)", err, verMsg)
	}
	if _, err := backupMgr.Verify(finalManifest.ArchivePath, "clave-incorrecta"); err == nil {
		t.Fatalf("expected backup verification with wrong passphrase to fail")
	}

	restoreTarget := filepath.Join(tmpDir, "restored_pos.db")
	_ = os.WriteFile(restoreTarget, []byte("old-dummy-content"), 0644)

	safetyPath, err := backupMgr.Restore(finalManifest.ArchivePath, restoreTarget, "clave-secreta-tienda-2026", true)
	if err != nil || safetyPath == "" {
		t.Fatalf("backup Restore failed: %v", err)
	}

	restoredConn, err := db.OpenPosDBReadOnly(restoreTarget)
	if err != nil {
		t.Fatalf("open restored pos.db failed: %v", err)
	}
	defer restoredConn.Close()
	docs, err := db.ListDocuments(restoredConn, 0, "POS-01")
	if err != nil || len(docs) != 13 {
		t.Fatalf("expected 13 documents in restored pos.db, got %d (err=%v)", len(docs), err)
	}
}

// BDD Scenario 8: Migration Verifier (MIG-004..006), Electronic Invoicing (TODO-052..056), Inventory Alerts, Reports, Admin TUI/JSON for Tauri UI & PII Masking
func TestBDD_MigrationFiscalReportsAdminAndSecurity(t *testing.T) {
	cfg, tmpDir := setupTestEnv(t)

	posConn, err := db.OpenPosDBReadOnly(cfg.PosDBPath)
	if err != nil {
		t.Fatalf("OpenPosDBReadOnly: %v", err)
	}
	defer posConn.Close()

	stateConn, err := db.OpenStateDB(cfg.StateDBPath)
	if err != nil {
		t.Fatalf("OpenStateDB: %v", err)
	}
	defer stateConn.Close()

	migEng := migrate.NewEngine(posConn, cfg.StoreID, cfg.CashRegisterID)
	files, bundle, err := migEng.Export("sqlserver", filepath.Join(tmpDir, "mig_out"))
	if err != nil || len(files) < 2 {
		t.Fatalf("migrate Export sqlserver failed: %v", err)
	}
	verRes, err := migEng.Verify(bundle)
	if err != nil || !verRes.Passed {
		t.Fatalf("migrate Verify failed: %+v (err=%v)", verRes, err)
	}

	fiscalProv := fiscal.NewStandardUBLProvider(cfg, stateConn)
	docs, _ := db.ListDocuments(posConn, 0, cfg.CashRegisterID)
	subRes, err := fiscalProv.SubmitDocument(context.Background(), docs[1])
	if err != nil {
		t.Fatalf("Fiscal SubmitDocument failed: %v", err)
	}
	if subRes.Status != fiscal.StatusAccepted || len(subRes.DocumentUUID) != 96 {
		t.Fatalf("expected ACCEPTED status and 96-char SHA-384 UUID, got status=%s len=%d", subRes.Status, len(subRes.DocumentUUID))
	}
	if _, err := os.Stat(subRes.XMLPath); err != nil {
		t.Fatalf("expected UBL 2.1 XML artifact at %s: %v", subRes.XMLPath, err)
	}
	cnRes, err := fiscalProv.EmitCreditNote(context.Background(), docs[1], "Devolución parcial")
	if err != nil || cnRes.DocumentKind != "NOTA_CREDITO" {
		t.Fatalf("EmitCreditNote failed: %v", err)
	}

	invEng := inventory.NewEngine(cfg, posConn, stateConn)
	soonDate := time.Now().AddDate(0, 0, 10).Format("2006-01-02")
	if err := invEng.SetProductExpiration(3, "Leche Entera 1L", "LOTE-2026-A", soonDate, 5); err != nil {
		t.Fatalf("SetProductExpiration: %v", err)
	}
	invRep, err := invEng.BuildReport(15)
	if err != nil || len(invRep.ExpiringSoon) != 1 {
		t.Fatalf("expected 1 product expiring within 15 days, got %d (err=%v)", len(invRep.ExpiringSoon), err)
	}
	priceList, err := invEng.GetPriceList("Mayorista")
	if err != nil || len(priceList) != 5 {
		t.Fatalf("GetPriceList Mayorista failed: %v", err)
	}

	repEng := reports.NewEngine(cfg, posConn, stateConn)
	daily, err := repEng.BuildDailyReport("")
	if err != nil || daily.TotalSales <= 0 || daily.TotalSalesDual <= 0 {
		t.Fatalf("BuildDailyReport failed: %+v (err=%v)", daily, err)
	}
	ticket := reports.BuildESCPOSTicket("Cocina", docs[0], docs[0].Items)
	if len(ticket) < 20 || ticket[0] != 0x1B {
		t.Fatalf("expected valid ESC/POS byte stream starting with 0x1B")
	}

	adm := admin.NewConsole(cfg, posConn, stateConn)
	snap, err := adm.BuildSnapshot("")
	if err != nil || snap.CreditPortfolio == nil || snap.InventorySummary == nil {
		t.Fatalf("BuildSnapshot for Tauri 2 + Svelte UI failed: %v", err)
	}
	tui, err := adm.RenderTerminalTUI("")
	if err != nil || !strings.Contains(tui, "CARTERA TOTAL DE CUENTAS DE CRÉDITO") {
		t.Fatalf("RenderTerminalTUI failed: %v", err)
	}
	docRep, err := db.RunDoctor(context.Background(), cfg.PosDBPath, cfg.StateDBPath, cfg.CashRegisterID, cfg.GoogleDrive.TokenVaultPath)
	if err != nil || !docRep.Healthy || docRep.QuickCheckResult != "ok" {
		t.Fatalf("RunDoctor reported unhealthy: %+v (err=%v)", docRep, err)
	}

	redacted := config.RedactPII("Cliente pedro.perez@correo.org con ID 79123456 y teléfono 5551234567")
	if strings.Contains(redacted, "79123456") || strings.Contains(redacted, "5551234567") || strings.Contains(redacted, "pedro.perez@") {
		t.Fatalf("RedactPII leaked sensitive PII: %s", redacted)
	}
}

// BDD Scenario 9: High-Volume Catalog & Document Latency Benchmark on Low-Spec Profile (TC-026, TODO-063)
func TestBDD_HighVolumePerformanceBenchmark_TC026(t *testing.T) {
	cfg, _ := setupTestEnv(t)

	wConn, err := testutil.OpenWritablePosDB(cfg.PosDBPath)
	if err != nil {
		t.Fatalf("OpenWritablePosDB: %v", err)
	}
	tx, err := wConn.Begin()
	if err != nil {
		t.Fatalf("Begin tx: %v", err)
	}
	for i := 100; i < 1100; i++ {
		_, _ = tx.Exec(`INSERT INTO Product (Id, ProductGroupId, Name, Code, Price, Cost) VALUES (?, 1, ?, ?, 5000, 3500);`,
			i, fmt.Sprintf("Producto Masivo #%d", i), fmt.Sprintf("SKU-%d", i))
		_, _ = tx.Exec(`INSERT INTO Stock (ProductId, WarehouseId, Quantity) VALUES (?, 1, 25);`, i)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit bulk products: %v", err)
	}
	_ = wConn.Close()

	posConn, err := db.OpenPosDBReadOnly(cfg.PosDBPath)
	if err != nil {
		t.Fatalf("OpenPosDBReadOnly: %v", err)
	}
	defer posConn.Close()
	stateConn, err := db.OpenStateDB(cfg.StateDBPath)
	if err != nil {
		t.Fatalf("OpenStateDB: %v", err)
	}
	defer stateConn.Close()

	start := time.Now()
	invEng := inventory.NewEngine(cfg, posConn, stateConn)
	rep, err := invEng.BuildReport(30)
	elapsed := time.Since(start)
	if err != nil || rep.TotalProducts < 1000 {
		t.Fatalf("expected >= 1000 products in TC-026 benchmark, got %d (err=%v)", rep.TotalProducts, err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("TC-026 inventory valuation took too long (%s > 3s)", elapsed)
	}
}

// BDD Scenario 10 (v1.2.0): Dual-Database Isolation (cuentas.db WAL + pos.db Read-Only),
// HID Barcode Scanner, Frozen Price Snapshots, Strict Credit Limits, Net Inventory & Drive Export
func TestBDD_DedicatedAccountsDB_Scanner_FrozenPrices_And_NetInventory(t *testing.T) {
	cfg, tmpDir := setupTestEnv(t)
	cfg.AccountsDBPath = filepath.Join(tmpDir, "cuentas.db")
	cfg.Credit.CreditLimitPolicy = "strict"

	// Seed 5 additional catalog items + barcodes in pos.db so we scan 10 distinct barcodes end-to-end
	wConn, err := testutil.OpenWritablePosDB(cfg.PosDBPath)
	if err != nil {
		t.Fatalf("OpenWritablePosDB: %v", err)
	}
	for i := 6; i <= 10; i++ {
		if _, err := wConn.Exec(`INSERT INTO Product (Id, ProductGroupId, Name, Code, MeasurementUnit, Price, Cost) VALUES (?, 1, ?, ?, 'UND', ?, 2000);`,
			i, fmt.Sprintf("Producto Escáner #%d", i), fmt.Sprintf("SCN-00%d", i), int64(i*1000)); err != nil {
			t.Fatalf("insert extra product %d: %v", i, err)
		}
		if _, err := wConn.Exec(`INSERT INTO Barcode (ProductId, Value) VALUES (?, ?);`, i, fmt.Sprintf("77012345000%d0", i)); err != nil {
			t.Fatalf("insert extra barcode %d: %v", i, err)
		}
		if _, err := wConn.Exec(`INSERT INTO Stock (ProductId, WarehouseId, Quantity) VALUES (?, 1, 20);`, i); err != nil {
			t.Fatalf("insert extra stock %d: %v", i, err)
		}
	}
	var initialDocCount, initialPayCount int
	_ = wConn.QueryRow(`SELECT COUNT(*) FROM Document;`).Scan(&initialDocCount)
	_ = wConn.QueryRow(`SELECT COUNT(*) FROM Payment;`).Scan(&initialPayCount)
	_ = wConn.Close()

	posConn, err := db.OpenPosDBReadOnly(cfg.PosDBPath)
	if err != nil {
		t.Fatalf("OpenPosDBReadOnly: %v", err)
	}
	defer posConn.Close()

	stateConn, err := db.OpenStateDB(cfg.StateDBPath)
	if err != nil {
		t.Fatalf("OpenStateDB: %v", err)
	}
	defer stateConn.Close()

	accMgr, err := accounts.NewManager(cfg.AccountsDBPath)
	if err != nil {
		t.Fatalf("accounts.NewManager: %v", err)
	}
	defer accMgr.Close()

	// 1. Simulate HID Barcode Scanner capturing 10 distinct barcodes from pos.db (zero write to pos.db)
	scannedBarcodes := []string{
		"7701234500011", // Arroz 4500 (stock 45)
		"7701234500028", // Aceite 11000 (stock 18)
		"7701234500035", // Leche 4200
		"7701234500042", // Café 16500
		"7701234500059", // Menú 28000
		"7701234500060", // Producto #6 6000
		"7701234500070", // Producto #7 7000
		"7701234500080", // Producto #8 8000
		"7701234500090", // Producto #9 9000
		"77012345000100", // Wait: i=10 formatted as 77012345000100
	}
	var items []accounts.MovementItemInput
	var expectedTotal int64
	for idx, bc := range scannedBarcodes {
		match, err := db.SearchCatalogForScanner(posConn, cfg.PosDBPath, "", bc, 20)
		if err != nil || match == nil || match.ExactBarcodeMatch == nil {
			t.Fatalf("barcode %q lookup failed (idx=%d): match=%+v err=%v", bc, idx, match, err)
		}
		p := match.ExactBarcodeMatch
		qty := 1.0
		if p.ID == 1 {
			qty = 3.0 // 3 units of Arroz to verify NetStockQuantity subtraction
		}
		unitPrice := int64(p.Price)
		items = append(items, accounts.MovementItemInput{
			AroniumProductID:  p.ID,
			Barcode:           bc,
			NameSnapshot:      p.Name,
			UnitPriceSnapshot: unitPrice,
			Quantity:          qty,
		})
		expectedTotal += int64(float64(unitPrice) * qty)
	}
	// expectedTotal = 3*4500 + 11000 + 4200 + 16500 + 28000 + 6000 + 7000 + 8000 + 9000 + 10000 = 113200
	if expectedTotal != 113200 {
		t.Fatalf("unexpected fixture sum %d", expectedTotal)
	}

	created, err := accMgr.CreateOrUpdateAccountWithItems(accounts.CreateAccountRequest{
		ID:            "1020304050",
		Name:          "Carlos Mendoza",
		Phone:         "3105559988",
		PaymentMethod: "Efectivo",
		CreditLimit:   150000,
		Description:   "Cliente corporativo con corte quincenal",
		StrictLimit:   true,
		Items:         items,
	})
	if err != nil {
		t.Fatalf("CreateOrUpdateAccountWithItems failed: %v", err)
	}
	if created.CurrentBalance != expectedTotal || created.CreditLimit != 150000 || created.Phone != "3105559988" {
		t.Fatalf("unexpected created account summary: %+v", created)
	}

	// 2. Price Drift Isolation: Double Arroz & Aceite prices in pos.db AFTER transaction
	time.Sleep(25 * time.Millisecond)
	wConn2, err := testutil.OpenWritablePosDB(cfg.PosDBPath)
	if err != nil {
		t.Fatalf("re-open pos.db writable for price drift check: %v", err)
	}
	if _, err := wConn2.Exec(`UPDATE Product SET Price = 9900 WHERE Id = 1; UPDATE Product SET Price = 25000 WHERE Id = 2;`); err != nil {
		t.Fatalf("update pos.db prices: %v", err)
	}
	_ = wConn2.Close()

	liveMatch, err := db.SearchCatalogForScanner(posConn, cfg.PosDBPath, "", "7701234500011", 5)
	if err != nil || liveMatch.ExactBarcodeMatch == nil || liveMatch.ExactBarcodeMatch.Price != 9900 {
		t.Fatalf("expected live scanner cache to reflect new pos.db price 9900, got %+v (err=%v)", liveMatch, err)
	}

	reloadedAcc, err := accMgr.GetAccount("1020304050")
	if err != nil {
		t.Fatalf("GetAccount failed: %v", err)
	}
	if reloadedAcc.CurrentBalance != expectedTotal {
		t.Fatalf("frozen snapshot violated! balance drifted from %d to %d after pos.db price change", expectedTotal, reloadedAcc.CurrentBalance)
	}
	if len(reloadedAcc.Movements) != 1 || len(reloadedAcc.Movements[0].Items) != 10 {
		t.Fatalf("expected 1 movement with 10 frozen items, got %+v", reloadedAcc.Movements)
	}
	for _, it := range reloadedAcc.Movements[0].Items {
		if it.AroniumProductID == 1 && it.UnitPriceSnapshot != 4500 {
			t.Fatalf("expected frozen unit price 4500 for product 1, got %d", it.UnitPriceSnapshot)
		}
		if it.AroniumProductID == 2 && it.UnitPriceSnapshot != 11000 {
			t.Fatalf("expected frozen unit price 11000 for product 2, got %d", it.UnitPriceSnapshot)
		}
	}

	// 3. Strict Credit Limit Policy blocks charge exceeding available credit (limit 150000 - balance 113200 = 36800 available)
	_, limitErr := accMgr.RecordMovement(accounts.MovementRequest{
		AccountID:     "1020304050",
		Type:          accounts.MovementCharge,
		Amount:        40000,
		PaymentMethod: "Efectivo",
		Note:          "Cargo sobre tope",
		StrictLimit:   true,
	})
	if limitErr == nil {
		t.Fatalf("expected strict credit limit error when charging 40000 over 36800 available")
	}

	// 4. Net Inventory Calculation: Stock_Aronium - Committed_In_Cuentas (Ruta A)
	invEng := inventory.NewEngine(cfg, posConn, stateConn)
	invRep, err := invEng.BuildReport(30)
	if err != nil {
		t.Fatalf("inventory BuildReport failed: %v", err)
	}
	var foundProduct1 bool
	for _, item := range invRep.Items {
		if item.ProductID == 1 {
			foundProduct1 = true
			if item.AroniumStock != 45 || item.CommittedInAccountsQty != 3 || item.NetStockQuantity != 42 {
				t.Fatalf("unexpected Net Inventory for product 1: AroniumStock=%.2f Committed=%.2f Net=%.2f",
					item.AroniumStock, item.CommittedInAccountsQty, item.NetStockQuantity)
			}
		}
	}
	if !foundProduct1 {
		t.Fatalf("product 1 not found in InventoryReport")
	}

	// 5. Partial Payment (ABONO) in cuentas.db must NEVER touch pos.db Document/Payment/Stock tables
	afterPay, err := accMgr.RecordMovement(accounts.MovementRequest{
		AccountID:     "1020304050",
		Type:          accounts.MovementPayment,
		Amount:        50000,
		PaymentMethod: "Transferencia",
		Note:          "Abono quincenal",
	})
	if err != nil {
		t.Fatalf("RecordMovement ABONO failed: %v", err)
	}
	if afterPay.CurrentBalance != expectedTotal-50000 {
		t.Fatalf("expected balance %d after 50000 abono, got %d", expectedTotal-50000, afterPay.CurrentBalance)
	}

	var docCountAfter, payCountAfter int
	var stock1After float64
	_ = posConn.QueryRow(`SELECT COUNT(*) FROM Document;`).Scan(&docCountAfter)
	_ = posConn.QueryRow(`SELECT COUNT(*) FROM Payment;`).Scan(&payCountAfter)
	_ = posConn.QueryRow(`SELECT Quantity FROM Stock WHERE ProductId = 1;`).Scan(&stock1After)
	if docCountAfter != initialDocCount || payCountAfter != initialPayCount || stock1After != 45 {
		t.Fatalf("pos.db was unexpectedly mutated! docs=%d/%d pays=%d/%d stock1=%.2f/45",
			docCountAfter, initialDocCount, payCountAfter, initialPayCount, stock1After)
	}

	// 6. Google Drive Sync exports 4-tab XLSX (Cuentas, Movimientos, Detalle_Articulos, Inventario_Neto) + VACUUM INTO backup
	mockGoogle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/upload/drive/v3/files"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "drive_acc_file_1"})
		case strings.Contains(r.URL.Path, "/values:batchUpdate"):
			_ = json.NewEncoder(w).Encode(map[string]any{"totalUpdatedCells": 200})
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockGoogle.Close()

	driveMgr := gosync.NewDriveManager(cfg, posConn, stateConn)
	driveMgr.UploadEndpoint = mockGoogle.URL + "/upload/drive/v3"
	driveMgr.SheetsEndpoint = mockGoogle.URL + "/v4/spreadsheets"
	_ = driveMgr.SaveToken(&gosync.OAuthToken{
		AccessToken: "ya29.test_accounts_sync",
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(time.Hour),
	})

	syncRes, err := driveMgr.SyncAll(context.Background())
	if err != nil {
		t.Fatalf("DriveManager.SyncAll failed: %v", err)
	}

	zr, err := zip.OpenReader(syncRes.MasterWorkbookPath)
	if err != nil {
		t.Fatalf("open MasterWorkbookPath zip: %v", err)
	}
	defer zr.Close()
	var workbookXML string
	for _, f := range zr.File {
		if f.Name == "xl/workbook.xml" {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			_ = rc.Close()
			workbookXML = string(b)
		}
	}
	for _, tab := range []string{"Cuentas", "Movimientos", "Detalle_Articulos", "Inventario_Neto"} {
		if !strings.Contains(workbookXML, tab) {
			t.Fatalf("expected sheet tab %q in xl/workbook.xml, got: %s", tab, workbookXML)
		}
	}

	var backupDBFound string
	_ = filepath.WalkDir(cfg.GoogleDrive.LocalDriveSyncDir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.EqualFold(d.Name(), "cuentas_backup.db") {
			backupDBFound = path
		}
		return nil
	})
	if backupDBFound == "" {
		t.Fatalf("expected VACUUM INTO cuentas_backup.db inside %s", cfg.GoogleDrive.LocalDriveSyncDir)
	}
	bakMgr, err := accounts.NewManager(backupDBFound)
	if err != nil {
		t.Fatalf("open backed-up cuentas_backup.db: %v", err)
	}
	defer bakMgr.Close()
	bakAcc, err := bakMgr.GetAccount("1020304050")
	if err != nil || bakAcc.CurrentBalance != expectedTotal-50000 {
		t.Fatalf("expected backed-up account balance %d, got %+v (err=%v)", expectedTotal-50000, bakAcc, err)
	}
}

// BDD Scenario 11 (v1.2.1): Icon Brightness Regression Check, Lossless v1->v2 Schema Migration,
// Compound Shared IDs (Texto-Numero), Optional Fields, QR Payment Method & Quick Punctual Values
func TestBDD_CompoundSharedAccountID_QuickValues_IconBrightness_And_SchemaMigration(t *testing.T) {
	// 1. Verify docs/assets/app-icon.ico is vibrant (not black) via its embedded 256x256 PNG frame
	icoPath := filepath.Join("..", "docs", "assets", "app-icon.ico")
	if icoRaw, err := os.ReadFile(icoPath); err == nil && len(icoRaw) > 64 {
		entrySize := int(binary.LittleEndian.Uint32(icoRaw[6+8 : 6+12]))
		entryOffset := int(binary.LittleEndian.Uint32(icoRaw[6+12 : 6+16]))
		if entryOffset+entrySize <= len(icoRaw) {
			frame := icoRaw[entryOffset : entryOffset+entrySize]
			if bytes.HasPrefix(frame, []byte("\x89PNG\r\n\x1a\n")) {
				img, pErr := png.Decode(bytes.NewReader(frame))
				if pErr != nil {
					t.Fatalf("decode 256px PNG in app-icon.ico: %v", pErr)
				}
				b := img.Bounds()
				var sum float64
				var cnt float64
				for y := b.Min.Y; y < b.Max.Y; y++ {
					for x := b.Min.X; x < b.Max.X; x++ {
						r, g, bl, a := img.At(x, y).RGBA()
						if a > 0x8000 {
							sum += float64((r>>8)+(g>>8)+(bl>>8)) / 3.0
							cnt++
						}
					}
				}
				avgBrightness := sum / cnt
				if cnt == 0 || avgBrightness < 100 {
					t.Fatalf("app-icon.ico appears too dark (avgBrightness=%.1f/255, opaque=%d)", avgBrightness, int(cnt))
				}
			}
		}
	}

	// 2. Construct a legacy v1 cuentas.db (id TEXT PRIMARY KEY without account_key) and migrate losslessly to v2
	tmpDir := t.TempDir()
	accDBPath := filepath.Join(tmpDir, "cuentas.db")
	legacyConn, err := sql.Open("sqlite", accDBPath)
	if err != nil {
		t.Fatalf("open legacy v1 sqlite: %v", err)
	}
	legacyDDL := `
	CREATE TABLE cuentas (
		id TEXT PRIMARY KEY,
		nombre TEXT NOT NULL,
		telefono TEXT NOT NULL DEFAULT '',
		metodo_pago TEXT NOT NULL DEFAULT 'Efectivo',
		tope_credito INTEGER NOT NULL DEFAULT 0,
		descripcion TEXT NOT NULL DEFAULT '',
		estado TEXT NOT NULL DEFAULT 'ACTIVA',
		creado_en DATETIME DEFAULT CURRENT_TIMESTAMP,
		actualizado_en DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE movimientos (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		cuenta_id TEXT NOT NULL REFERENCES cuentas(id) ON DELETE CASCADE,
		tipo TEXT NOT NULL,
		monto INTEGER NOT NULL,
		metodo_pago_usado TEXT NOT NULL DEFAULT '',
		nota TEXT NOT NULL DEFAULT '',
		fecha DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE movimiento_items (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		movimiento_id INTEGER NOT NULL REFERENCES movimientos(id) ON DELETE CASCADE,
		aronium_product_id INTEGER NOT NULL DEFAULT 0,
		codigo_barras TEXT NOT NULL DEFAULT '',
		nombre_snapshot TEXT NOT NULL,
		precio_unitario_snapshot INTEGER NOT NULL,
		cantidad REAL NOT NULL DEFAULT 1,
		subtotal INTEGER NOT NULL
	);
	INSERT INTO cuentas (id, nombre, telefono, metodo_pago, tope_credito, descripcion)
		VALUES ('Torre-110', 'Cliente Previo', '', 'QR', 200000, 'Torre A, Apartamento 110');
	INSERT INTO movimientos (id, cuenta_id, tipo, monto, metodo_pago_usado, nota)
		VALUES (7, 'Torre-110', 'CARGO', 15000, 'QR', 'Saldo heredado v1');
	INSERT INTO movimiento_items (movimiento_id, aronium_product_id, codigo_barras, nombre_snapshot, precio_unitario_snapshot, cantidad, subtotal)
		VALUES (7, 1, '7701234500011', 'Arroz v1', 7500, 2, 15000);
	`
	if _, err := legacyConn.Exec(legacyDDL); err != nil {
		t.Fatalf("seed legacy v1 schema: %v", err)
	}
	_ = legacyConn.Close()

	migRep, err := accounts.MigrateAccountsDB(accDBPath)
	if err != nil {
		t.Fatalf("MigrateAccountsDB failed: %v", err)
	}
	if !migRep.Migrated || migRep.PreviousVersion != 1 || migRep.CurrentVersion != accounts.CurrentSchemaVersion ||
		migRep.AccountsPreserved != 1 || migRep.MovementsPreserved != 1 || migRep.ItemsPreserved != 1 {
		t.Fatalf("unexpected migration report: %+v", migRep)
	}
	if _, err := os.Stat(migRep.BackupPath); err != nil {
		t.Fatalf("expected v1 pre-migration backup at %s: %v", migRep.BackupPath, err)
	}

	accMgr, err := accounts.NewManager(accDBPath)
	if err != nil {
		t.Fatalf("NewManager post-migration failed: %v", err)
	}
	defer accMgr.Close()

	// Ensure migrated v1 customer balance survived cleanly
	prevAcc, err := accMgr.GetAccountByIDAndName("Torre-110", "Cliente Previo")
	if err != nil || prevAcc.CurrentBalance != 15000 {
		t.Fatalf("migrated account balance mismatch: %+v (err=%v)", prevAcc, err)
	}

	// 3. Create 2 additional accounts sharing the SAME compound ID Torre-110 (separated by nombre)
	// with empty optional phone & description, QR payment method, and quick punctual values
	anaAcc, err := accMgr.CreateOrUpdateAccountWithItems(accounts.CreateAccountRequest{
		IDPrefix:      "Torre",
		IDNumber:      "110",
		Name:          "Cliente A",
		Phone:         "", // optional
		PaymentMethod: "QR",
		CreditLimit:   300000,
		Description:   "", // optional
		Items: []accounts.MovementItemInput{
			{AroniumProductID: 1, Barcode: "7701234500011", NameSnapshot: "Arroz 1kg", UnitPriceSnapshot: 4500, Quantity: 1},
			{AroniumProductID: 0, Barcode: "PUNTUAL", NameSnapshot: "Valor puntual", UnitPriceSnapshot: 2100, Quantity: 1}, // $2.000 + $100 quick value
		},
	})
	if err != nil {
		t.Fatalf("create Cliente A on shared Torre-110 failed: %v", err)
	}
	if anaAcc.ID != "Torre-110" || anaAcc.IDPrefix != "Torre" || anaAcc.IDNumber != "110" ||
		anaAcc.PaymentMethod != "QR" || anaAcc.CurrentBalance != 6600 {
		t.Fatalf("unexpected Cliente A account state: %+v", anaAcc)
	}

	carlosAcc, err := accMgr.CreateOrUpdateAccountWithItems(accounts.CreateAccountRequest{
		IDPrefix:      "Torre",
		IDNumber:      "110",
		Name:          "Cliente B",
		PaymentMethod: "QR",
		CreditLimit:   300000,
		Items: []accounts.MovementItemInput{
			{AroniumProductID: 0, Barcode: "PUNTUAL", NameSnapshot: "Consumo varios", UnitPriceSnapshot: 5000, Quantity: 1},
		},
	})
	if err != nil {
		t.Fatalf("create Cliente B on shared Torre-110 failed: %v", err)
	}
	if carlosAcc.CurrentBalance != 5000 {
		t.Fatalf("unexpected Cliente B balance: %+v", carlosAcc)
	}

	// 4. Listing by ID Torre-110 must surface all accounts sharing that apartment ID
	sharedList, err := accMgr.ListAccountsByID("Torre-110")
	if err != nil || len(sharedList) != 3 {
		t.Fatalf("expected 3 accounts sharing Torre-110, got %d (err=%v)", len(sharedList), err)
	}
	for _, s := range sharedList {
		if s.SharedIDCount != 3 {
			t.Fatalf("expected SharedIDCount=3 for %s, got %d", s.Name, s.SharedIDCount)
		}
	}

	// 5. Ambiguous payment on shared ID without selecting person must error, while targeted payment succeeds
	if _, err := accMgr.RecordMovement(accounts.MovementRequest{
		AccountID:     "Torre-110",
		Type:          accounts.MovementPayment,
		Amount:        3000,
		PaymentMethod: "QR",
	}); err == nil {
		t.Fatalf("expected ambiguity error when paying Torre-110 without customer disambiguation")
	}

	paidAna, err := accMgr.RecordMovement(accounts.MovementRequest{
		AccountID:     "Torre-110",
		CustomerName:  "Cliente A",
		Type:          accounts.MovementPayment,
		Amount:        3000,
		PaymentMethod: "QR",
		Note:          "Abono vía QR",
	})
	if err != nil || paidAna.CurrentBalance != 3600 {
		t.Fatalf("expected Cliente A balance 3600 after 3000 QR payment, got %+v (err=%v)", paidAna, err)
	}

	// Cliente B and Cliente Previo must remain untouched
	reloadedCarlos, _ := accMgr.GetAccountByIDAndName("Torre-110", "Cliente B")
	reloadedPrev, _ := accMgr.GetAccountByIDAndName("Torre-110", "Cliente Previo")
	if reloadedCarlos.CurrentBalance != 5000 || reloadedPrev.CurrentBalance != 15000 {
		t.Fatalf("shared accounts corrupted after paying Cliente A: carlos=%d prev=%d", reloadedCarlos.CurrentBalance, reloadedPrev.CurrentBalance)
	}

	// Verify 0 SQLite foreign key violations after migration and multi-account writes
	fkRows, err := accMgr.DB.Query(`PRAGMA foreign_key_check;`)
	if err != nil {
		t.Fatalf("PRAGMA foreign_key_check: %v", err)
	}
	defer fkRows.Close()
	if fkRows.Next() {
		t.Fatalf("unexpected foreign_key_check violation after v1->v2 migration")
	}
	_ = fkRows.Close()
	_ = accMgr.Close()

	// 6. Simulate program uninstall / deletion of cuentas.db + reinstall recovery from automatic snapshot
	_ = os.Remove(accDBPath)
	_ = os.Remove(accDBPath + "-wal")
	_ = os.Remove(accDBPath + "-shm")

	recoveredMgr, err := accounts.NewManager(accDBPath)
	if err != nil {
		t.Fatalf("NewManager after simulated uninstall/reinstall failed: %v", err)
	}
	defer recoveredMgr.Close()

	recAna, err := recoveredMgr.GetAccountByIDAndName("Torre-110", "Cliente A")
	if err != nil || recAna.CurrentBalance != 3600 {
		t.Fatalf("expected Cliente A balance 3600 to be preserved/recovered after reinstall, got %+v (err=%v)", recAna, err)
	}
	recCarlos, err := recoveredMgr.GetAccountByIDAndName("Torre-110", "Cliente B")
	if err != nil || recCarlos.CurrentBalance != 5000 {
		t.Fatalf("expected Carlos balance 5000 to be preserved/recovered after reinstall, got %+v (err=%v)", recCarlos, err)
	}

	// 7. Verify Direct GitHub Releases Auto-Updater (update check + update apply with SHA-256 verification)
	fakeInstallerPayload := []byte("MZ-FAKE-ARONIUM-SETUP-INSTALLER-V1.3.0-PAYLOAD")
	sumBytes := sha256.Sum256(fakeInstallerPayload)
	expectedHex := hex.EncodeToString(sumBytes[:])

	var ghSrv *httptest.Server
	ghSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/SilverFeit/Aronium-Utility/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name":     "v1.3.0",
				"name":         "Aronium Utility v1.3.0",
				"body":         "Actualización directa desde GitHub Releases",
				"html_url":     ghSrv.URL + "/releases/tag/v1.3.0",
				"published_at": "2026-11-22T06:00:00Z",
				"assets": []map[string]any{
					{
						"name":                 "aronium-util-setup-x64.exe",
						"browser_download_url": ghSrv.URL + "/download/aronium-util-setup-x64.exe",
						"size":                 len(fakeInstallerPayload),
					},
					{
						"name":                 "SHA256SUMS.txt",
						"browser_download_url": ghSrv.URL + "/download/SHA256SUMS.txt",
						"size":                 128,
					},
				},
			})
		case "/download/aronium-util-setup-x64.exe":
			_, _ = w.Write(fakeInstallerPayload)
		case "/download/SHA256SUMS.txt":
			fmt.Fprintf(w, "%s  aronium-util-setup-x64.exe\n", expectedHex)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ghSrv.Close()

	upd := updater.NewUpdater("1.2.3")
	upd.ReleaseAPIURL = ghSrv.URL + "/repos/SilverFeit/Aronium-Utility/releases/latest"
	upd.DryRunLaunch = true

	chkRes, err := upd.Check(context.Background())
	if err != nil || !chkRes.UpdateAvailable || chkRes.LatestVersion != "1.3.0" {
		t.Fatalf("updater.Check failed: %+v (err=%v)", chkRes, err)
	}

	appRes, err := upd.DownloadAndApply(context.Background(), false, true)
	if err != nil || !appRes.Downloaded || !appRes.VerifiedSHA256 || appRes.SHA256 != expectedHex {
		t.Fatalf("updater.DownloadAndApply failed: %+v (err=%v)", appRes, err)
	}
	_ = os.Remove(appRes.InstallerPath)

	// 8. Delete Paid Account strictly enforced: blocked while balance > 0, allowed once settled to $0
	if _, err := recoveredMgr.DeletePaidAccount("Torre-110", "Cliente A"); err == nil {
		t.Fatalf("expected DeletePaidAccount to fail while Cliente A still owes 3600")
	}

	settledAna, err := recoveredMgr.RecordMovement(accounts.MovementRequest{
		AccountID:     "Torre-110",
		CustomerName:  "Cliente A",
		Type:          accounts.MovementPayment,
		Amount:        3600,
		PaymentMethod: "QR",
		Note:          "Liquidación total",
	})
	if err != nil || settledAna.CurrentBalance != 0 {
		t.Fatalf("expected Cliente A balance 0 after settling, got %+v (err=%v)", settledAna, err)
	}

	deletedAna, err := recoveredMgr.DeletePaidAccount("Torre-110", "Cliente A")
	if err != nil || deletedAna == nil || deletedAna.Name != "Cliente A" {
		t.Fatalf("DeletePaidAccount failed after zero balance: %+v (err=%v)", deletedAna, err)
	}

	if _, err := recoveredMgr.GetAccountByIDAndName("Torre-110", "Cliente A"); err == nil {
		t.Fatalf("expected deleted account Cliente A to be absent from cuentas.db")
	}
	carlosStillThere, err := recoveredMgr.GetAccountByIDAndName("Torre-110", "Cliente B")
	if err != nil || carlosStillThere.CurrentBalance != 5000 {
		t.Fatalf("expected sibling account Cliente B to remain intact after deleting Cliente A: %+v (err=%v)", carlosStillThere, err)
	}
}

func TestBDD_TrayAutostartPreference_And_ServiceManagement(t *testing.T) {
	tmpAppDir := filepath.Join(t.TempDir(), "AroniumUtilityApp")
	if err := os.MkdirAll(tmpAppDir, 0755); err != nil {
		t.Fatalf("mkdir temp app dir: %v", err)
	}

	mockUIBin := filepath.Join(t.TempDir(), "aronium-utility-ui")
	if err := os.WriteFile(mockUIBin, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("write mock ui bin: %v", err)
	}
	t.Setenv("ARONIUM_UI_BIN", mockUIBin)

	if resolved := service.ResolveUIBinaryPath(""); resolved != mockUIBin {
		t.Fatalf("expected ResolveUIBinaryPath to return %s, got %s", mockUIBin, resolved)
	}

	mgr := &service.Manager{
		AppDir:     tmpAppDir,
		BinaryPath: filepath.Join(filepath.Dir(mockUIBin), "aronium-util"),
		ConfigPath: filepath.Join(tmpAppDir, "aronium-util.yaml"),
		LogPath:    filepath.Join(tmpAppDir, "logs", "service.log"),
	}

	initial := mgr.UIAutostartStatus()
	if initial.Enabled {
		t.Fatalf("expected initial autostart to be disabled in fresh temp dir, got %+v", initial)
	}

	enabledSt, err := mgr.SetUIAutostart(true, mockUIBin)
	if err != nil || !enabledSt.Enabled || mgr.ReadAutostartPreference() != "enabled" {
		t.Fatalf("expected autostart enabled after SetUIAutostart(true), got %+v (err=%v)", enabledSt, err)
	}

	disabledSt, err := mgr.SetUIAutostart(false, mockUIBin)
	if err != nil || disabledSt.Enabled || mgr.ReadAutostartPreference() != "disabled" {
		t.Fatalf("expected autostart disabled after SetUIAutostart(false), got %+v (err=%v)", disabledSt, err)
	}
}

func TestBDD_LocalTimezoneTimestamps_And_V2UTCToV3Migration(t *testing.T) {
	t.Setenv("ARONIUM_TIMEZONE", "America/Bogota")

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cuentas.db")

	// Seed a v2 cuentas.db storing legacy UTC CURRENT_TIMESTAMP values (e.g., 07:36:36 UTC when local PC time is 02:36:36 -05:00)
	v2Conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open v2 sqlite: %v", err)
	}
	v2DDL := `
	CREATE TABLE cuentas (
		account_key TEXT PRIMARY KEY,
		id TEXT NOT NULL,
		id_prefix TEXT NOT NULL DEFAULT '',
		id_number TEXT NOT NULL DEFAULT '',
		nombre TEXT NOT NULL,
		telefono TEXT NOT NULL DEFAULT '',
		metodo_pago TEXT NOT NULL DEFAULT 'Efectivo',
		tope_credito INTEGER NOT NULL DEFAULT 0,
		descripcion TEXT NOT NULL DEFAULT '',
		estado TEXT NOT NULL DEFAULT 'ACTIVA',
		creado_en DATETIME DEFAULT CURRENT_TIMESTAMP,
		actualizado_en DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE movimientos (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		cuenta_id TEXT NOT NULL REFERENCES cuentas(account_key) ON DELETE CASCADE,
		tipo TEXT NOT NULL,
		monto INTEGER NOT NULL,
		metodo_pago_usado TEXT NOT NULL DEFAULT '',
		nota TEXT NOT NULL DEFAULT '',
		fecha DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE movimiento_items (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		movimiento_id INTEGER NOT NULL REFERENCES movimientos(id) ON DELETE CASCADE,
		aronium_product_id INTEGER NOT NULL DEFAULT 0,
		codigo_barras TEXT NOT NULL DEFAULT '',
		nombre_snapshot TEXT NOT NULL,
		precio_unitario_snapshot INTEGER NOT NULL,
		cantidad REAL NOT NULL DEFAULT 1,
		subtotal INTEGER NOT NULL
	);
	INSERT INTO cuentas (account_key, id, id_prefix, id_number, nombre, telefono, metodo_pago, tope_credito, creado_en, actualizado_en)
		VALUES ('abc-123::cliente prueba', 'ABC-123', 'ABC', '123', 'Cliente Prueba', '', 'Otro', 1000000, '2026-11-22 06:52:36', '2026-11-22 07:36:36');
	INSERT INTO movimientos (id, cuenta_id, tipo, monto, metodo_pago_usado, nota, fecha)
		VALUES (1, 'abc-123::cliente prueba', 'CARGO', 50000, 'Otro', 'Anotación de 1 concepto(s)', '2026-11-22 06:52:36');
	INSERT INTO movimientos (id, cuenta_id, tipo, monto, metodo_pago_usado, nota, fecha)
		VALUES (2, 'abc-123::cliente prueba', 'CARGO', 15500, 'Otro', 'Anotación de 2 concepto(s)', '2026-11-22 07:36:36');
	PRAGMA user_version = 2;
	`
	if _, err := v2Conn.Exec(v2DDL); err != nil {
		t.Fatalf("seed v2 db: %v", err)
	}
	_ = v2Conn.Close()

	// Migrating v2 -> v3 should convert existing UTC timestamps to America/Bogota (-05:00) once
	migRep, err := accounts.MigrateAccountsDB(dbPath)
	if err != nil {
		t.Fatalf("MigrateAccountsDB v2->v3 failed: %v", err)
	}
	if !migRep.Migrated || migRep.PreviousVersion != 2 || migRep.CurrentVersion != 3 {
		t.Fatalf("expected v2->v3 migration report, got %+v", migRep)
	}

	mgr, err := accounts.NewManager(dbPath)
	if err != nil {
		t.Fatalf("NewManager post v3 migration failed: %v", err)
	}
	defer mgr.Close()

	acc, err := mgr.GetAccountByIDAndName("ABC-123", "Cliente Prueba")
	if err != nil || len(acc.Movements) != 2 {
		t.Fatalf("GetAccountByIDAndName failed: %+v (err=%v)", acc, err)
	}
	if acc.Movements[0].Date != "2026-11-22 02:36:36" || acc.Movements[1].Date != "2026-11-22 01:52:36" {
		t.Fatalf("expected converted local Bogota timestamps [02:36:36, 01:52:36], got [%s, %s]",
			acc.Movements[0].Date, acc.Movements[1].Date)
	}

	// Second migration check must be idempotent (never double-shift local timestamps)
	migRep2, err := accounts.MigrateAccountsDB(dbPath)
	if err != nil || migRep2.Migrated {
		t.Fatalf("expected idempotent v3 re-check without re-migration, got %+v (err=%v)", migRep2, err)
	}

	// New movement must record exact America/Bogota local wall-clock time matching statement header
	before := accounts.LocalNow().Truncate(time.Second)
	updated, err := mgr.RecordMovement(accounts.MovementRequest{
		AccountID:     "ABC-123",
		CustomerName:  "Cliente Prueba",
		Type:          accounts.MovementCharge,
		Amount:        2000,
		PaymentMethod: "QR",
		Note:          "Compra con cámara sincronizada",
	})
	after := accounts.LocalNow().Add(time.Second)
	if err != nil {
		t.Fatalf("RecordMovement failed: %v", err)
	}
	newestDate, err := time.ParseInLocation("2006-01-02 15:04:05", updated.Movements[0].Date, accounts.LocalLocation())
	if err != nil {
		t.Fatalf("parse new movement local date %q: %v", updated.Movements[0].Date, err)
	}
	if newestDate.Before(before.Add(-time.Second)) || newestDate.After(after.Add(time.Second)) {
		t.Fatalf("expected new movement timestamp in local range [%s, %s], got %s", before, after, newestDate)
	}

	stmt := accounts.GenerateStatement(*updated, "Mi Comercio Aronium")
	if !strings.Contains(stmt, updated.Movements[0].Date[:13]) {
		t.Fatalf("expected statement header and movement date hour prefix %q to match:\n%s", updated.Movements[0].Date[:13], stmt)
	}
}

func TestBDD_V2_SchemaAdaptivePosDB_LocalSyncToggle_PinSecurity_ResetAndRepair(t *testing.T) {
	tmpDir := t.TempDir()
	minimalPosPath := filepath.Join(tmpDir, "pos.db")
	accDBPath := filepath.Join(tmpDir, "cuentas.db")
	stateDBPath := filepath.Join(tmpDir, "state.db")

	// 1. Construct a real-world Aronium Lite pos.db WITHOUT optional columns (no p.Markup, no p.PLU, no p.Cost, no di.ProductCost)
	posConn, err := sql.Open("sqlite", minimalPosPath)
	if err != nil {
		t.Fatalf("open minimal pos.db: %v", err)
	}
	minimalPosDDL := `
	CREATE TABLE ProductGroup (Id INTEGER PRIMARY KEY, Name TEXT);
	CREATE TABLE Product (Id INTEGER PRIMARY KEY, ProductGroupId INTEGER, Name TEXT, Code TEXT, Price REAL, IsEnabled INTEGER);
	CREATE TABLE Barcode (Id INTEGER PRIMARY KEY, ProductId INTEGER, Value TEXT);
	CREATE TABLE Stock (Id INTEGER PRIMARY KEY, ProductId INTEGER, Quantity REAL);
	CREATE TABLE Customer (Id INTEGER PRIMARY KEY, Name TEXT);
	CREATE TABLE PaymentType (Id INTEGER PRIMARY KEY, Name TEXT);
	CREATE TABLE Document (Id INTEGER PRIMARY KEY, Number TEXT, CustomerId INTEGER, Total REAL, PaidStatus INTEGER, Date TEXT);
	CREATE TABLE DocumentItem (Id INTEGER PRIMARY KEY, DocumentId INTEGER, ProductId INTEGER, Quantity REAL, Price REAL);
	CREATE TABLE Payment (Id INTEGER PRIMARY KEY, DocumentId INTEGER, PaymentTypeId INTEGER, Amount REAL);

	INSERT INTO ProductGroup (Id, Name) VALUES (1, 'Snacks');
	INSERT INTO Product (Id, ProductGroupId, Name, Code, Price, IsEnabled) VALUES (101, 1, 'MANIMOTO LIMON', 'MNL-01', 2000, 1);
	INSERT INTO Barcode (Id, ProductId, Value) VALUES (1, 101, '7702189058508');
	INSERT INTO Stock (Id, ProductId, Quantity) VALUES (1, 101, 12);
	`
	if _, err := posConn.Exec(minimalPosDDL); err != nil {
		t.Fatalf("seed minimal pos.db: %v", err)
	}
	_ = posConn.Close()

	roPos, err := db.OpenPosDBReadOnly(minimalPosPath)
	if err != nil {
		t.Fatalf("OpenPosDBReadOnly minimal pos.db failed: %v", err)
	}
	defer roPos.Close()

	prods, err := db.ListProductsWithStock(roPos)
	if err != nil || len(prods) != 1 || prods[0].Name != "MANIMOTO LIMON" || prods[0].Barcode != "7702189058508" {
		t.Fatalf("schema-adaptive ListProductsWithStock failed: %+v (err=%v)", prods, err)
	}

	match, err := db.SearchCatalogForScanner(roPos, minimalPosPath, "", "7702189058508", 20)
	if err != nil || match.ExactBarcodeMatch == nil || match.ExactBarcodeMatch.Name != "MANIMOTO LIMON" {
		t.Fatalf("scanner barcode 7702189058508 failed: %+v (err=%v)", match, err)
	}

	docRep, err := db.RunDoctor(context.Background(), minimalPosPath, stateDBPath, "POS-01", "")
	if err != nil || !docRep.Healthy || docRep.PosDBProductCount != 1 {
		t.Fatalf("RunDoctor expected Healthy with PosDBProductCount=1, got %+v (err=%v)", docRep, err)
	}

	// 2. Verify DefaultConfig sets local instant saves (AutoSyncDrive=false) & 4:3 720p friendly scale (90%)
	defCfg := config.DefaultConfig()
	if defCfg.Watch.AutoSyncDrive {
		t.Fatalf("expected AutoSyncDrive=false by default for instant local annotation")
	}
	if defCfg.UI.ScalePercent != 90 || !defCfg.UI.CompactMode || defCfg.UI.Theme != "dark" {
		t.Fatalf("expected 4:3/720p UI defaults ScalePercent=90, CompactMode=true & Theme=dark, got %+v", defCfg.UI)
	}

	// 3. Uppercase account ID & name (e.g., PARQUES-18502 / DANIEL) + clean table reset & recreation
	mgr, err := accounts.NewManager(accDBPath)
	if err != nil {
		t.Fatalf("accounts.NewManager: %v", err)
	}
	defer mgr.Close()

	created, err := mgr.CreateOrUpdateAccountWithItems(accounts.CreateAccountRequest{
		IDPrefix:      "PARQUES",
		IDNumber:      "18502",
		Name:          "DANIEL",
		PaymentMethod: "Otro",
		CreditLimit:   500000,
		Items: []accounts.MovementItemInput{
			{
				AroniumProductID:  101,
				Barcode:           "7702189058508",
				NameSnapshot:      "MANIMOTO LIMON",
				UnitPriceSnapshot: 2000,
				Quantity:          1,
			},
		},
	})
	if err != nil || created.ID != "PARQUES-18502" || created.Name != "DANIEL" || created.CurrentBalance != 2000 {
		t.Fatalf("uppercase account creation failed: %+v (err=%v)", created, err)
	}

	// 4. 5-digit PIN validation & verification
	if _, err := config.HashPIN("123"); err == nil {
		t.Fatalf("expected 3-digit PIN to fail validation")
	}
	if _, err := config.HashPIN("12ab5"); err == nil {
		t.Fatalf("expected non-numeric PIN to fail validation")
	}
	pinHash, err := config.HashPIN("54321")
	if err != nil {
		t.Fatalf("HashPIN valid 5-digit failed: %v", err)
	}
	if !config.VerifyPIN("54321", pinHash) || config.VerifyPIN("99999", pinHash) {
		t.Fatalf("VerifyPIN mismatch")
	}

	// 5. Drop and recreate cuentas.db tables cleanly with automatic prior backup
	bakPath, err := mgr.ResetAndRecreateAccountsDB()
	if err != nil {
		t.Fatalf("ResetAndRecreateAccountsDB failed: %v", err)
	}
	if bakPath == "" {
		t.Fatalf("expected pre-reset backup path")
	}
	if _, stErr := os.Stat(bakPath); stErr != nil {
		t.Fatalf("expected pre-reset backup file at %s: %v", bakPath, stErr)
	}
	afterReset, err := mgr.ListAccounts("")
	if err != nil || afterReset.TotalAccountsCount != 0 {
		t.Fatalf("expected 0 accounts after table reset, got %+v (err=%v)", afterReset, err)
	}
}

func TestBDD_V202_BulkListDocuments_AllDBsBackup_UninstallerDocumentsCopy_And_DebugLogs(t *testing.T) {
	cfg, tmpDir := setupTestEnv(t)

	// 1. Verify bulk db.ListDocuments preserves items & payments properly without N+1 loop
	posConn, err := db.OpenPosDBReadOnly(cfg.PosDBPath)
	if err != nil {
		t.Fatalf("OpenPosDBReadOnly: %v", err)
	}
	defer posConn.Close()
	docs, err := db.ListDocuments(posConn, 0, cfg.CashRegisterID)
	if err != nil || len(docs) == 0 {
		t.Fatalf("bulk ListDocuments failed: len=%d err=%v", len(docs), err)
	}
	hasItems := false
	hasPayments := false
	for _, d := range docs {
		if len(d.Items) > 0 {
			hasItems = true
		}
		if len(d.Payments) > 0 {
			hasPayments = true
		}
	}
	if !hasItems || !hasPayments {
		t.Fatalf("expected bulk ListDocuments to hydrate Items and Payments, got hasItems=%v hasPayments=%v", hasItems, hasPayments)
	}

	// 2. Verify CreateAllDatabasesSnapshot snapshots pos.db, cuentas.db, state.db, config + Documents backup
	docsBackupDir := filepath.Join(tmpDir, "FakeDocumentsBackup")
	t.Setenv("ARONIUM_DOCS_BACKUP_DIR", docsBackupDir)
	t.Setenv("ARONIUM_UTIL_HOME", tmpDir)

	accMgr, err := accounts.NewManager(cfg.AccountsDBPath)
	if err != nil {
		t.Fatalf("accounts.NewManager: %v", err)
	}
	if _, err := accMgr.CreateOrUpdateAccountWithItems(accounts.CreateAccountRequest{
		IDPrefix:      "Torre",
		IDNumber:      "110",
		Name:          "Cliente Backup V202",
		PaymentMethod: "QR",
		CreditLimit:   100000,
	}); err != nil {
		t.Fatalf("CreateOrUpdateAccountWithItems: %v", err)
	}
	_ = accMgr.Close()

	stateConn, err := db.OpenStateDB(cfg.StateDBPath)
	if err != nil {
		t.Fatalf("OpenStateDB: %v", err)
	}
	_ = stateConn.Close()

	cfgPath := filepath.Join(tmpDir, "aronium-util.yaml")
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("cfg.Save: %v", err)
	}

	bMgr := backup.NewManager(cfg)
	allRes, err := bMgr.CreateAllDatabasesSnapshot(context.Background(), cfgPath)
	if err != nil {
		t.Fatalf("CreateAllDatabasesSnapshot: %v", err)
	}
	if len(allRes.Files) < 4 {
		t.Fatalf("expected at least 4 backed up files (pos, cuentas, state, yaml), got %d (%+v)", len(allRes.Files), allRes.Files)
	}
	if _, err := os.Stat(allRes.ZipArchivePath); err != nil {
		t.Fatalf("expected zip archive at %s: %v", allRes.ZipArchivePath, err)
	}
	if allRes.DocumentsAccountsCopy == "" {
		t.Fatalf("expected DocumentsAccountsCopy path in backup result")
	}

	// 3. Verify Complete Uninstaller backs up cuentas.db to Mis Documentos and wipes installDir + dataDir 100%
	fakeInstallDir := filepath.Join(tmpDir, "InstallPrograms")
	fakeDataDir := filepath.Join(tmpDir, "DataApp")
	_ = os.MkdirAll(fakeInstallDir, 0755)
	_ = os.MkdirAll(fakeDataDir, 0755)
	_ = os.WriteFile(filepath.Join(fakeInstallDir, "aronium-util.exe"), []byte("dummy-exe"), 0755)
	accRaw, _ := os.ReadFile(cfg.AccountsDBPath)
	_ = os.WriteFile(filepath.Join(fakeDataDir, "cuentas.db"), accRaw, 0644)

	docsBackupDir2 := filepath.Join(tmpDir, "FakeDocumentsUninstall")
	t.Setenv("ARONIUM_DOCS_BACKUP_DIR", docsBackupDir2)

	unRep, err := service.RunCompleteUninstall(fakeInstallDir, fakeDataDir, "")
	if err != nil {
		t.Fatalf("RunCompleteUninstall failed: %v", err)
	}
	if !unRep.AccountsBackedUp || !unRep.InstallDirRemoved || !unRep.DataDirRemoved {
		t.Fatalf("expected full uninstall + accounts backup, got %+v", unRep)
	}
	if _, err := os.Stat(filepath.Join(docsBackupDir2, "cuentas.db")); err != nil {
		t.Fatalf("expected cuentas.db backup inside Documents folder: %v", err)
	}
	if _, err := os.Stat(fakeInstallDir); !os.IsNotExist(err) {
		t.Fatalf("expected fakeInstallDir to be completely deleted")
	}
	if _, err := os.Stat(fakeDataDir); !os.IsNotExist(err) {
		t.Fatalf("expected fakeDataDir to be completely deleted")
	}

	// 4. Verify DebugLogger write / redaction / read / clear
	_ = os.MkdirAll(tmpDir, 0755)
	config.AppendDebugLog("UI-IPC", "doctor --json", "OK", 12, "healthy=true")
	dbgOut, err := config.ReadDebugLogs(8192)
	if err != nil || !strings.Contains(dbgOut, "doctor --json") {
		t.Fatalf("expected debug.log to contain doctor --json, got %q (err=%v)", dbgOut, err)
	}
	if err := config.ClearDebugLogs(); err != nil {
		t.Fatalf("ClearDebugLogs: %v", err)
	}
	dbgAfter, _ := config.ReadDebugLogs(8192)
	if !strings.Contains(dbgAfter, "limpiado") {
		t.Fatalf("expected cleared marker in debug.log, got %q", dbgAfter)
	}
}

func TestBDD_V203_IsolatedPosDBLocalCopy_ZeroLiveLock_DailyBoxClose_And_TwoDBRestorePoint(t *testing.T) {
	cfg, tmpDir := setupTestEnv(t)
	t.Setenv("ARONIUM_UTIL_HOME", tmpDir)
	docsDir := filepath.Join(tmpDir, "DocsBackup203")
	t.Setenv("ARONIUM_DOCS_BACKUP_DIR", docsDir)

	localCopyDest := filepath.Join(tmpDir, "pos_local_copy.db")
	cfg.LocalPosCopyPath = localCopyDest

	// 1. Copy Aronium's pos.db -> pos_local_copy.db via pure OS byte copy (zero SQLite lock on original)
	cpRes, err := db.SyncPosDBToLocalCopy(cfg.PosDBPath, localCopyDest, true)
	if err != nil {
		t.Fatalf("SyncPosDBToLocalCopy: %v", err)
	}
	if cpRes.LocalCopyPath != localCopyDest || cpRes.SizeBytes <= 0 {
		t.Fatalf("unexpected PosCopyResult: %+v", cpRes)
	}

	// Verify EnsureWorkingPosDBCopy returns existing localCopyDest WITHOUT touching original pos.db even if origin is temporarily missing
	missingOrigin := filepath.Join(tmpDir, "nonexistent_aronium_dir", "pos.db")
	resolved, err := db.EnsureWorkingPosDBCopy(missingOrigin, localCopyDest)
	if err != nil || resolved != localCopyDest {
		t.Fatalf("expected EnsureWorkingPosDBCopy to reuse isolated copy cleanly, got resolved=%q err=%v", resolved, err)
	}

	// Verify isolated copy is 100% queryable for catalog & inventory
	isoConn, err := db.OpenPosDBReadOnly(resolved)
	if err != nil {
		t.Fatalf("OpenPosDBReadOnly on isolated copy: %v", err)
	}
	prods, err := db.ListProductsWithStock(isoConn)
	_ = isoConn.Close()
	if err != nil || len(prods) == 0 {
		t.Fatalf("expected products from isolated pos_local_copy.db, got %d (err=%v)", len(prods), err)
	}

	// 2. Daily Accounts Cash-Box Closure + 2-DB Restore Point + Drive sync toggle
	mgr, err := accounts.NewManager(cfg.AccountsDBPath)
	if err != nil {
		t.Fatalf("accounts.NewManager: %v", err)
	}
	defer mgr.Close()

	_, err = mgr.CreateOrUpdateAccountWithItems(accounts.CreateAccountRequest{
		IDPrefix:            "Torre",
		IDNumber:            "110",
		Name:                "Cliente Cierre Caja",
		PaymentMethod:       "QR",
		CreditLimit:         200000,
		InitialChargeAmount: 45000,
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	if _, err := mgr.RecordMovement(accounts.MovementRequest{
		AccountID:     "Torre-110",
		CustomerName:  "Cliente Cierre Caja",
		Type:          accounts.MovementPayment,
		Amount:        20000,
		PaymentMethod: "QR",
		Note:          "Abono del día",
	}); err != nil {
		t.Fatalf("record payment: %v", err)
	}

	closure, err := mgr.CloseDailyAccountsBox(context.Background(), accounts.DailyCloseRequest{
		Note:             "Cierre de prueba v2.0.3",
		StoreName:        cfg.StoreName,
		PosLocalCopyPath: localCopyDest,
		BackupDir:        cfg.Backup.BackupDir,
		ReportsDir:       filepath.Join(cfg.GoogleDrive.LocalDriveSyncDir, "AroniumUtility", "Reportes"),
		DocumentsDir:     docsDir,
		SyncDrive:        false,
	})
	if err != nil {
		t.Fatalf("CloseDailyAccountsBox: %v", err)
	}
	if closure.TotalChargesDay != 45000 || closure.TotalPaymentsDay != 20000 || closure.PaymentsQR != 20000 || closure.TotalPortfolioSaldo != 25000 {
		t.Fatalf("unexpected daily closure totals: %+v", closure)
	}
	if _, err := os.Stat(closure.PosRestorePath); err != nil {
		t.Fatalf("expected pos_local_copy restore point at %s: %v", closure.PosRestorePath, err)
	}
	if _, err := os.Stat(closure.CuentasRestorePath); err != nil {
		t.Fatalf("expected cuentas.db restore point at %s: %v", closure.CuentasRestorePath, err)
	}
	if _, err := os.Stat(filepath.Join(docsDir, "pos_local_copy.db")); err != nil {
		t.Fatalf("expected Documents pos_local_copy.db copy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(docsDir, "cuentas.db")); err != nil {
		t.Fatalf("expected Documents cuentas.db copy: %v", err)
	}

	closures, err := mgr.ListDailyClosures(10)
	if err != nil || len(closures) == 0 {
		t.Fatalf("expected persisted closure in cierres_caja, got len=%d err=%v", len(closures), err)
	}

	// 3. Verify DriveManager.SyncAll saves BOTH pos_backup.db & cuentas_backup.db as 2-DB restore points
	stateConn, err := db.OpenStateDB(cfg.StateDBPath)
	if err != nil {
		t.Fatalf("OpenStateDB: %v", err)
	}
	defer stateConn.Close()
	isoConn2, err := db.OpenPosDBReadOnly(localCopyDest)
	if err != nil {
		t.Fatalf("OpenPosDBReadOnly 2: %v", err)
	}
	defer isoConn2.Close()

	drvMgr := gosync.NewDriveManager(cfg, isoConn2, stateConn)
	syncRes, err := drvMgr.SyncAll(context.Background())
	if err != nil {
		t.Fatalf("SyncAll: %v", err)
	}
	if len(syncRes.TwoDBRestorePoints) < 2 || syncRes.PosRestorePointPath == "" || syncRes.AccountsBackupPath == "" {
		t.Fatalf("expected both DB restore points in SyncResult, got %+v", syncRes)
	}
}
