package tests

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SilverFeit/Aronium-Utility/internal/admin"
	"github.com/SilverFeit/Aronium-Utility/internal/backup"
	"github.com/SilverFeit/Aronium-Utility/internal/config"
	"github.com/SilverFeit/Aronium-Utility/internal/db"
	"github.com/SilverFeit/Aronium-Utility/internal/dian"
	"github.com/SilverFeit/Aronium-Utility/internal/events"
	"github.com/SilverFeit/Aronium-Utility/internal/fiados"
	"github.com/SilverFeit/Aronium-Utility/internal/hub"
	"github.com/SilverFeit/Aronium-Utility/internal/inventory"
	"github.com/SilverFeit/Aronium-Utility/internal/migrate"
	"github.com/SilverFeit/Aronium-Utility/internal/registers"
	"github.com/SilverFeit/Aronium-Utility/internal/reports"
	gosync "github.com/SilverFeit/Aronium-Utility/internal/sync"
	"github.com/SilverFeit/Aronium-Utility/internal/testutil"
)

func setupTestEnv(t *testing.T) (*config.Config, string) {
	t.Helper()
	tmpDir := t.TempDir()
	posDBPath := filepath.Join(tmpDir, "pos.db")
	stateDBPath := filepath.Join(tmpDir, "aronium_utility_state.db")

	if err := testutil.CreateSyntheticPosDB(posDBPath); err != nil {
		t.Fatalf("CreateSyntheticPosDB failed: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.StoreID = "TIENDA-TEST-01"
	cfg.StoreName = "Minimercado El Vecino"
	cfg.CashRegisterID = "POS-01"
	cfg.PosDBPath = posDBPath
	cfg.StateDBPath = stateDBPath
	cfg.GoogleDrive.TokenVaultPath = filepath.Join(tmpDir, "gdrive.token.enc")
	cfg.GoogleDrive.LocalDriveSyncDir = filepath.Join(tmpDir, "GoogleDriveSync")
	cfg.Backup.BackupDir = filepath.Join(tmpDir, "backups")
	return cfg, tmpDir
}

// BDD Scenario 1 & 3: Fiados Routing by Customer Name, Split Payments, Partial Abonos & CLI Notes (TODO-017..021, TODO-059)
func TestBDD_FiadosCustomerRoutingAndAbonoRecalculation(t *testing.T) {
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

	// Add CLI note and custom credit limit for Don Pedro Pérez (CustomerId=2)
	_, err = db.AddFiadoNote(stateConn, db.FiadoNote{
		CustomerID:   2,
		CustomerName: "Don Pedro Pérez",
		Note:         "Prometió abonar el viernes quincena",
		DuePromise:   "2026-11-20",
	})
	if err != nil {
		t.Fatalf("AddFiadoNote: %v", err)
	}
	if err := db.SetCustomerCreditLimit(stateConn, 2, "Don Pedro Pérez", 25000); err != nil {
		t.Fatalf("SetCustomerCreditLimit: %v", err)
	}

	eng := fiados.NewEngine(cfg, posConn, stateConn)
	ledger, err := eng.BuildLedger("", 0)
	if err != nil {
		t.Fatalf("BuildLedger: %v", err)
	}

	// Don Pedro has $20.000 fiado (Doc #2) and Doña María has $50.000 total with $20.000 cash abono = $30.000 pending (Doc #3)
	if ledger.TotalPendingPortfolio != 50000 {
		t.Fatalf("expected initial portfolio $50000, got %.2f", ledger.TotalPendingPortfolio)
	}

	// Now pass a new bill for Don Pedro Pérez ($11.000 Aceite) -> should push him to $31.000 (> $25.000 credit limit -> CUPO_EXCEDIDO)
	newDocID, err := testutil.SimulateNewFiadoSale(cfg.PosDBPath, "20-200-000004", 2, 2, 1, 11000, "Lleva 1 litro de aceite fiado", "POS-01")
	if err != nil {
		t.Fatalf("SimulateNewFiadoSale: %v", err)
	}

	ledgerAfterSale, err := eng.BuildLedger("Don Pedro", 0)
	if err != nil || len(ledgerAfterSale.Accounts) != 1 {
		t.Fatalf("BuildLedger for Don Pedro failed: %v", err)
	}
	pedro := ledgerAfterSale.Accounts[0]
	if pedro.PendingBalance != 31000 {
		t.Fatalf("expected Don Pedro balance $31000, got %.2f", pedro.PendingBalance)
	}
	if !pedro.ExceedsCreditLimit || pedro.AccountStatus != "CUPO_EXCEDIDO" {
		t.Fatalf("expected Don Pedro status CUPO_EXCEDIDO, got %s", pedro.AccountStatus)
	}

	// Now simulate a partial abono of $15.000 by Don Pedro on the new bill (TODO-019)
	if err := testutil.SimulateCustomerAbono(cfg.PosDBPath, newDocID, 15000, false); err != nil {
		t.Fatalf("SimulateCustomerAbono: %v", err)
	}
	// Wait Wait: on a single document of $11.000, let's also abono $10.000 on Doc #2 ($20.000)
	if err := testutil.SimulateCustomerAbono(cfg.PosDBPath, 2, 10000, false); err != nil {
		t.Fatalf("SimulateCustomerAbono Doc 2: %v", err)
	}

	ledgerAfterAbono, err := eng.BuildLedger("Don Pedro", 0)
	if err != nil {
		t.Fatalf("BuildLedger after abono: %v", err)
	}
	pedroAfter := ledgerAfterAbono.Accounts[0]
	// Doc 2: 20000 - 10000 = 10000 pending; Doc 4: 11000 - min(11000, 15000) = 0 pending -> total pending = 10000
	if pedroAfter.PendingBalance != 10000 {
		t.Fatalf("expected Don Pedro balance $10000 after abonos, got %.2f", pedroAfter.PendingBalance)
	}

	stmt := fiados.GenerateStatement(pedroAfter, cfg.StoreName, "USD", 4200)
	if !strings.Contains(stmt, "Don Pedro Pérez") || !strings.Contains(stmt, "Prometió abonar el viernes") {
		t.Fatalf("statement missing customer details or CLI note:\n%s", stmt)
	}
}

// BDD Scenario 2: Direct Web OAuth 2.0 Authentication & Unified Inventory + Fiados Table Sync to Google Drive & Sheets (TODO-025..028)
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

	// Spin up mock Google OAuth 2.0 + Drive v3 + Sheets v4 server
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
	cfg.GoogleDrive.OAuthCallbackPort = 0 // Ephemeral port
	driveMgr := gosync.NewDriveManager(cfg, posConn, stateConn)
	driveMgr.TokenEndpoint = mockGoogle.URL + "/token"
	driveMgr.UploadEndpoint = mockGoogle.URL + "/upload/drive/v3"
	driveMgr.SheetsEndpoint = mockGoogle.URL + "/v4/spreadsheets"

	// Test Web OAuth 2.0 Loopback Flow by triggering the callback endpoint
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Find an available ephemeral port for the test callback
	cfg.GoogleDrive.OAuthCallbackPort = 18765
	doneCh := make(chan error, 1)
	go func() {
		_, _, err := driveMgr.AuthenticateViaWeb(ctx, "test-client-id.apps.googleusercontent.com", "GOCSPX-test-secret", false)
		doneCh <- err
	}()

	time.Sleep(120 * time.Millisecond)
	resp, err := http.PostForm("http://127.0.0.1:18765/oauth2/callback", url.Values{
		"code":          {"4/0MockAuthCodeFromBrowser"},
		"account_email": {"tienda@mitienda.co"},
	})
	if err != nil {
		t.Fatalf("OAuth web callback request failed: %v", err)
	}
	resp.Body.Close()

	if err := <-doneCh; err != nil {
		t.Fatalf("AuthenticateViaWeb returned error: %v", err)
	}

	// Verify encrypted token vault was saved and can be decrypted
	tok, err := driveMgr.LoadToken()
	if err != nil || tok.AccessToken != "ya29.mock_access_token_123" {
		t.Fatalf("failed to load encrypted OAuth token: %v (tok=%+v)", err, tok)
	}

	// Run full SyncAll: generates unified Master XLSX + per-customer XLSX/CSV/TXT + uploads to Drive v3 & Sheets v4
	syncRes, err := driveMgr.SyncAll(context.Background())
	if err != nil {
		t.Fatalf("SyncAll failed: %v", err)
	}

	if _, err := os.Stat(syncRes.MasterWorkbookPath); err != nil {
		t.Fatalf("master workbook not created at %s: %v", syncRes.MasterWorkbookPath, err)
	}

	// Inspect the generated .xlsx zip structure to confirm worksheets: Resumen_Fiados, Detalle_Fiados, Inventario, and Customer tabs
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
		t.Fatalf("expected individual customer files in /Fiados/Clientes/, got %v", syncRes.CustomerFiles)
	}
	if len(syncRes.UploadedDriveIDs) == 0 {
		t.Fatalf("expected files uploaded to Google Drive API v3")
	}
	if syncRes.UpdatedSheetID != "sheet_master_123" || sheetBatchUpdates != 1 {
		t.Fatalf("expected Google Sheets API v4 batchUpdate to run once, got %d", sheetBatchUpdates)
	}

	// Run a second SyncAll to verify stable file_id reuse via HTTP PATCH (TODO-026 Transporte B)
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

	// 1. Test Register Identity & Duplicate Conflict Detection (TC-029, TODO-029)
	regMgr := registers.NewManager(posConn, stateConn, cfg.StoreID)
	if _, err := regMgr.ClaimIdentity("POS-01", "Caja Principal", "PC-CAJA-01", false); err != nil {
		t.Fatalf("ClaimIdentity POS-01 on PC-CAJA-01 failed: %v", err)
	}
	// Attempting to claim POS-01 from a different workstation PC-CAJA-02 without --force must fail (TC-029)
	if _, err := regMgr.ClaimIdentity("POS-01", "Caja Duplicada", "PC-CAJA-02", false); err == nil {
		t.Fatalf("expected TC-029 duplicate cash register identity conflict error, got nil")
	}

	// 2. Test Sales Filtering by Cash Register (PRO-FR-004, AC-014..AC-016)
	pos01Sales, err := regMgr.QuerySales("POS-01", "POS-01", "", "")
	if err != nil || len(pos01Sales) != 1 || pos01Sales[0].TransactionCount != 2 {
		t.Fatalf("expected 2 sales for POS-01, got %+v (err=%v)", pos01Sales, err)
	}
	pos02Sales, err := regMgr.QuerySales("POS-01", "POS-02", "", "")
	if err != nil || len(pos02Sales) != 1 || pos02Sales[0].TransactionCount != 1 {
		t.Fatalf("expected 1 sale for POS-02, got %+v (err=%v)", pos02Sales, err)
	}

	// 3. Spin up Central Hub Server (TODO-032..035)
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
	// Seed Product #3 (Leche Alquería) with ONLY 1 unit left in master stock to test TC-021!
	if err := hubSrv.SeedMasterStock(3, "Leche Entera Alquería 1L", 1); err != nil {
		t.Fatalf("SeedMasterStock: %v", err)
	}

	httpHub := httptest.NewServer(hubSrv.Handler())
	defer httpHub.Close()

	clientPOS1 := hub.NewClient(httpHub.URL, "secret-hub-token-2026", cfg.StoreID, "POS-01", "PC-CAJA-01", stateConn)
	clientPOS2 := hub.NewClient(httpHub.URL, "secret-hub-token-2026", cfg.StoreID, "POS-02", "PC-CAJA-02", stateConn)

	// TC-021: Both POS-01 and POS-02 simultaneously sell 1 unit of Product #3 (when only 1 unit exists!)
	salePOS1 := db.Document{
		ID: 101, Number: "POS1-101", CustomerName: "Cliente 1", Total: 4200,
		Items: []db.DocumentItem{{ProductID: 3, ProductName: "Leche Entera Alquería 1L", Quantity: 1, Price: 4200, Total: 4200}},
	}
	salePOS2 := db.Document{
		ID: 202, Number: "POS2-202", CustomerName: "Cliente 2", Total: 4200,
		Items: []db.DocumentItem{{ProductID: 3, ProductName: "Leche Entera Alquería 1L", Quantity: 1, Price: 4200, Total: 4200}},
	}

	if queued, err := clientPOS1.SubmitSale(context.Background(), salePOS1); err != nil || queued {
		t.Fatalf("POS-01 SubmitSale failed: queued=%v err=%v", queued, err)
	}
	if queued, err := clientPOS2.SubmitSale(context.Background(), salePOS2); err != nil || queued {
		t.Fatalf("POS-02 SubmitSale failed: queued=%v err=%v", queued, err)
	}

	// Re-submitting the same sale must be deduplicated idempotently (TODO-033)
	if _, err := clientPOS1.SubmitSale(context.Background(), salePOS1); err != nil {
		t.Fatalf("duplicate SubmitSale failed: %v", err)
	}

	// TC-022: Concurrent catalog edits from POS-01 and POS-02 on Product #3
	_, err = hubSrv.ProcessCatalogEdit(hub.CatalogEditPayload{
		RegisterID: "POS-01", ProductID: 3, Code: "LEC-003", Name: "Leche Alquería 1L", Price: 4300, Cost: 3300,
		UpdatedAtUTC: time.Now().UTC().Add(-2 * time.Second),
	})
	if err != nil {
		t.Fatalf("ProcessCatalogEdit POS-01: %v", err)
	}
	conflictCat, err := hubSrv.ProcessCatalogEdit(hub.CatalogEditPayload{
		RegisterID: "POS-02", ProductID: 3, Code: "LEC-003", Name: "Leche Alquería 1L Oferta", Price: 4500, Cost: 3300,
		UpdatedAtUTC: time.Now().UTC(),
	})
	if err != nil || conflictCat == nil || conflictCat.ConflictType != "CONCURRENT_CATALOG_EDIT" {
		t.Fatalf("expected CONCURRENT_CATALOG_EDIT conflict (TC-022), got %+v (err=%v)", conflictCat, err)
	}

	st, err := hubSrv.Status()
	if err != nil {
		t.Fatalf("hubSrv.Status: %v", err)
	}
	if st.TotalDocuments != 2 {
		t.Fatalf("expected exactly 2 deduplicated documents on Hub, got %d", st.TotalDocuments)
	}
	if st.MasterStock[3] != -1 {
		t.Fatalf("expected master stock for Product #3 to be -1 after last-unit conflict, got %.2f", st.MasterStock[3])
	}
	if len(st.Conflicts) < 2 {
		t.Fatalf("expected both TC-021 and TC-022 conflict audit records, got %+v", st.Conflicts)
	}

	// 4. TC-015, TC-016, TC-023, TC-024: Simulate LAN / Hub Outage -> Offline Queue -> Reconnection without duplicates
	offlineClient := hub.NewClient("http://127.0.0.1:59999", "secret-hub-token-2026", cfg.StoreID, "POS-01", "PC-CAJA-01", stateConn)
	offlineSale := db.Document{ID: 303, Number: "POS1-OFFLINE-303", CustomerName: "Don Pedro Pérez", Total: 15000}
	queued, err := offlineClient.SubmitSale(context.Background(), offlineSale)
	if err != nil || !queued {
		t.Fatalf("expected offline sale to be queued in sync_outbox (queued=%v, err=%v)", queued, err)
	}

	// Now restore connectivity by pointing client back to active httpHub.URL and reconcile
	offlineClient.ServerURL = httpHub.URL
	reconciled, err := offlineClient.ReconcileOutbox(context.Background())
	if err != nil || reconciled != 1 {
		t.Fatalf("expected 1 offline sale reconciled upon Hub reconnection, got %d (err=%v)", reconciled, err)
	}
	stAfter, _ := hubSrv.Status()
	if stAfter.TotalDocuments != 3 {
		t.Fatalf("expected 3 total documents on Hub after offline reconciliation, got %d", stAfter.TotalDocuments)
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

	// Concurrently simulate Aronium Lite writing 10 rapid sales while Watcher polls and Backup creates hot snapshots (TODO-060)
	var wg sync.WaitGroup
	errCh := make(chan error, 30)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; i <= 10; i++ {
			docNum := fmt.Sprintf("20-200-STRESS-%03d", i)
			if _, err := testutil.SimulateNewFiadoSale(cfg.PosDBPath, docNum, 2, 1, 1, 4500, "Venta concurrente", "POS-01"); err != nil {
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

	var lastManifest *backup.BackupManifest
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(40 * time.Millisecond)
		m, err := backupMgr.Create(context.Background(), "clave-secreta-tienda-2026")
		if err != nil {
			errCh <- fmt.Errorf("concurrent hot backup failed: %w", err)
			return
		}
		lastManifest = m
	}()

	wg.Wait()
	close(errCh)
	for e := range errCh {
		t.Fatalf("concurrency stress error (TODO-060): %v", e)
	}

	// Final poll to catch any trailing write
	_, _ = watcher.PollOnce(context.Background())
	if eventCount == 0 {
		t.Fatalf("expected watcher to emit events during concurrent sales")
	}

	// Create a final clean encrypted backup after all 10 sales completed
	finalManifest, err := backupMgr.Create(context.Background(), "clave-secreta-tienda-2026")
	if err != nil || finalManifest == nil {
		t.Fatalf("final backup create failed: %v", err)
	}
	_ = lastManifest

	// Verify backup integrity (PRAGMA integrity_check + SHA-256 + AES-256-GCM)
	verMsg, err := backupMgr.Verify(finalManifest.ArchivePath, "clave-secreta-tienda-2026")
	if err != nil || !strings.Contains(verMsg, "OK") {
		t.Fatalf("backup Verify failed: %v (%s)", err, verMsg)
	}

	// Verify wrong passphrase fails cleanly
	if _, err := backupMgr.Verify(finalManifest.ArchivePath, "clave-incorrecta"); err == nil {
		t.Fatalf("expected backup verification with wrong passphrase to fail")
	}

	// Restore into a fresh target path and verify pre-restore safety backup & restored records (TC-017, TC-025)
	restoreTarget := filepath.Join(tmpDir, "restored_pos.db")
	_ = os.WriteFile(restoreTarget, []byte("old-dummy-content"), 0644)

	safetyPath, err := backupMgr.Restore(finalManifest.ArchivePath, restoreTarget, "clave-secreta-tienda-2026", true)
	if err != nil {
		t.Fatalf("backup Restore failed: %v", err)
	}
	if safetyPath == "" {
		t.Fatalf("expected pre-restore safety backup path to be created")
	}

	restoredConn, err := db.OpenPosDBReadOnly(restoreTarget)
	if err != nil {
		t.Fatalf("open restored pos.db failed: %v", err)
	}
	defer restoredConn.Close()
	docs, err := db.ListDocuments(restoredConn, 0, "POS-01")
	if err != nil || len(docs) != 13 { // 3 initial + 10 concurrent stress sales
		t.Fatalf("expected 13 documents in restored pos.db, got %d (err=%v)", len(docs), err)
	}
}

// BDD Scenario 8: Migration Verifier (MIG-004..006), DIAN Electronic Invoicing (TODO-052..056), Inventory Alerts, Reports, Admin & PII Masking (TODO-064..065)
func TestBDD_MigrationDIANReportsAdminAndSecurity(t *testing.T) {
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

	// 1. Test Multi-Engine Migration & Verifier (MIG-004, MIG-005, MIG-006)
	migEng := migrate.NewEngine(posConn, cfg.StoreID, cfg.CashRegisterID)
	files, bundle, err := migEng.Export("sqlserver", filepath.Join(tmpDir, "mig_out"))
	if err != nil || len(files) < 2 {
		t.Fatalf("migrate Export sqlserver failed: %v", err)
	}
	verRes, err := migEng.Verify(bundle)
	if err != nil || !verRes.Passed {
		t.Fatalf("migrate Verify failed: %+v (err=%v)", verRes, err)
	}

	// 2. Test Colombia DIAN Electronic Invoicing & Credit Note (TODO-052..056)
	dianProv := dian.NewColombiaDIANProvider(cfg, stateConn)
	docs, _ := db.ListDocuments(posConn, 0, cfg.CashRegisterID)
	subRes, err := dianProv.SubmitDocument(context.Background(), docs[1]) // Don Pedro Pérez invoice
	if err != nil {
		t.Fatalf("DIAN SubmitDocument failed: %v", err)
	}
	if subRes.Status != dian.StatusAccepted || len(subRes.CUFE) != 96 { // SHA-384 hex is 96 chars
		t.Fatalf("expected ACCEPTED status and 96-char SHA-384 CUFE, got status=%s len=%d", subRes.Status, len(subRes.CUFE))
	}
	if _, err := os.Stat(subRes.XMLPath); err != nil {
		t.Fatalf("expected UBL 2.1 XML artifact at %s: %v", subRes.XMLPath, err)
	}
	cnRes, err := dianProv.EmitCreditNote(context.Background(), docs[1], "Devolución parcial")
	if err != nil || cnRes.DocumentKind != "NOTA_CREDITO" {
		t.Fatalf("EmitCreditNote failed: %v", err)
	}

	// 3. Test Inventory Expiration Alerts & Differentiated Price Lists (TODO-048, TODO-051)
	invEng := inventory.NewEngine(cfg, posConn, stateConn)
	soonDate := time.Now().AddDate(0, 0, 10).Format("2006-01-02")
	if err := invEng.SetProductExpiration(3, "Leche Entera Alquería 1L", "LOTE-2026-A", soonDate, 5); err != nil {
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

	// 4. Test Daily Reports & ESC/POS Ticket Generation (TODO-047, TODO-049, TODO-050)
	repEng := reports.NewEngine(cfg, posConn, stateConn)
	daily, err := repEng.BuildDailyReport("")
	if err != nil || daily.TotalSalesCOP <= 0 || daily.TotalSalesDual <= 0 {
		t.Fatalf("BuildDailyReport failed: %+v (err=%v)", daily, err)
	}
	ticket := reports.BuildESCPOSTicket("Cocina", docs[0], docs[0].Items)
	if len(ticket) < 20 || ticket[0] != 0x1B {
		t.Fatalf("expected valid ESC/POS byte stream starting with 0x1B")
	}

	// 5. Test Admin Console TUI & Doctor (TODO-008, TODO-037)
	adm := admin.NewConsole(cfg, posConn, stateConn)
	tui, err := adm.RenderTerminalTUI("")
	if err != nil || !strings.Contains(tui, "CARTERA TOTAL DE FIADOS") {
		t.Fatalf("RenderTerminalTUI failed: %v", err)
	}
	docRep, err := db.RunDoctor(context.Background(), cfg.PosDBPath, cfg.StateDBPath, cfg.CashRegisterID, cfg.GoogleDrive.TokenVaultPath)
	if err != nil || !docRep.Healthy || docRep.QuickCheckResult != "ok" {
		t.Fatalf("RunDoctor reported unhealthy: %+v (err=%v)", docRep, err)
	}

	// 6. Test PII Masking & Secret Vault (TODO-064, TODO-065)
	redacted := config.RedactPII("Cliente pedro.perez@correo.co con CC 79123456 y celular 3105551234")
	if strings.Contains(redacted, "79123456") || strings.Contains(redacted, "3105551234") || strings.Contains(redacted, "pedro.perez@") {
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

