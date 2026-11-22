package tests

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	cfg.StoreID = "STORE-TEST-01"
	cfg.StoreName = "Minimercado Central"
	cfg.CashRegisterID = "POS-01"
	cfg.PosDBPath = posDBPath
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

	doneCh := make(chan error, 1)
	go func() {
		_, _, err := driveMgr.AuthenticateViaWeb(ctx, "test-client-id.apps.googleusercontent.com", "GOCSPX-test-secret", false)
		doneCh <- err
	}()

	time.Sleep(120 * time.Millisecond)
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
