package hub

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/SilverFeit/Aronium-Utility/internal/db"
)

// SaleEventPayload is sent by each POS station (POS-01, POS-02) to the Central Hub (TODO-032, TODO-033).
type SaleEventPayload struct {
	IdempotencyKey string      `json:"idempotency_key"`
	StoreID        string      `json:"store_id"`
	RegisterID     string      `json:"register_id"`
	Hostname       string      `json:"hostname"`
	Document       db.Document `json:"document"`
	TimestampUTC   time.Time   `json:"timestamp_utc"`
}

// CatalogEditPayload represents a product catalog modification from a terminal (TC-022, TODO-035).
type CatalogEditPayload struct {
	RegisterID   string    `json:"register_id"`
	ProductID    int64     `json:"product_id"`
	Code         string    `json:"code"`
	Name         string    `json:"name"`
	Price        float64   `json:"price"`
	Cost         float64   `json:"cost"`
	UpdatedAtUTC time.Time `json:"updated_at_utc"`
}

// ConflictRecord documents a detected multi-station concurrency conflict (TC-021, TC-022, TODO-035).
type ConflictRecord struct {
	ID           int64     `json:"id"`
	ConflictType string    `json:"conflict_type"` // "LAST_UNIT_STOCK_CONFLICT" (TC-021) or "CONCURRENT_CATALOG_EDIT" (TC-022)
	ProductID    int64     `json:"product_id"`
	ProductName  string    `json:"product_name"`
	RegisterA    string    `json:"register_a"`
	RegisterB    string    `json:"register_b"`
	Resolution   string    `json:"resolution"`
	Details      string    `json:"details"`
	CreatedAt    time.Time `json:"created_at"`
}

// HubStatus summarizes consolidated multi-register metrics on the Central Hub.
type HubStatus struct {
	StoreID           string             `json:"store_id"`
	ConnectedTerminals []string          `json:"connected_terminals"`
	TotalDocuments    int                `json:"total_documents"`
	TotalSalesAmount  float64            `json:"total_sales_amount"`
	SalesByRegister   map[string]float64 `json:"sales_by_register"`
	MasterStock       map[int64]float64  `json:"master_stock"`
	Conflicts         []ConflictRecord   `json:"conflicts"`
}

// Server implements the resilient multi-station Central Hub (`aronium-util hub` - TODO-032..035, TODO-066).
type Server struct {
	mu       sync.Mutex
	HubDB    *sql.DB
	APIToken string
	StoreID  string
}

func NewServer(hubDB *sql.DB, storeID, apiToken string) (*Server, error) {
	s := &Server{
		HubDB:    hubDB,
		APIToken: apiToken,
		StoreID:  storeID,
	}
	if err := s.initTables(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Server) initTables() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS hub_documents (
			idempotency_key TEXT PRIMARY KEY,
			store_id TEXT NOT NULL,
			register_id TEXT NOT NULL,
			document_id INTEGER NOT NULL,
			document_number TEXT NOT NULL,
			customer_name TEXT NOT NULL,
			total REAL NOT NULL,
			paid_status INTEGER NOT NULL,
			payload_json TEXT NOT NULL,
			received_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS hub_stock (
			product_id INTEGER PRIMARY KEY,
			product_name TEXT NOT NULL,
			quantity REAL NOT NULL,
			last_register_id TEXT NOT NULL DEFAULT '',
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS hub_catalog (
			product_id INTEGER PRIMARY KEY,
			code TEXT NOT NULL,
			name TEXT NOT NULL,
			price REAL NOT NULL,
			cost REAL NOT NULL,
			last_register_id TEXT NOT NULL,
			updated_at_utc TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS hub_conflicts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conflict_type TEXT NOT NULL,
			product_id INTEGER NOT NULL,
			product_name TEXT NOT NULL,
			register_a TEXT NOT NULL,
			register_b TEXT NOT NULL,
			resolution TEXT NOT NULL,
			details TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS hub_terminals (
			register_id TEXT PRIMARY KEY,
			hostname TEXT NOT NULL,
			last_seen DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
	}
	for _, q := range stmts {
		if _, err := s.HubDB.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

// SeedMasterStock sets the initial stock quantity of a product on the Central Hub.
func (s *Server) SeedMasterStock(productID int64, productName string, initialQty float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.HubDB.Exec(`
		INSERT INTO hub_stock (product_id, product_name, quantity, last_register_id, updated_at)
		VALUES (?, ?, ?, 'HUB-INIT', CURRENT_TIMESTAMP)
		ON CONFLICT(product_id) DO UPDATE SET
			product_name = excluded.product_name,
			quantity = excluded.quantity,
			updated_at = CURRENT_TIMESTAMP;
	`, productID, productName, initialQty)
	return err
}

// ProcessSale ingests a sale event idempotently, updates master stock deltas, and detects TC-021 last-unit conflicts.
func (s *Server) ProcessSale(ev SaleEventPayload) (duplicate bool, conflicts []ConflictRecord, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if ev.IdempotencyKey == "" {
		ev.IdempotencyKey = fmt.Sprintf("%s:%s:doc-%d", ev.StoreID, ev.RegisterID, ev.Document.ID)
	}

	_, _ = s.HubDB.Exec(`
		INSERT INTO hub_terminals (register_id, hostname, last_seen)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(register_id) DO UPDATE SET hostname = excluded.hostname, last_seen = CURRENT_TIMESTAMP;
	`, ev.RegisterID, ev.Hostname)

	rawDoc, _ := json.Marshal(ev.Document)
	res, err := s.HubDB.Exec(`
		INSERT OR IGNORE INTO hub_documents
			(idempotency_key, store_id, register_id, document_id, document_number, customer_name, total, paid_status, payload_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);
	`, ev.IdempotencyKey, ev.StoreID, ev.RegisterID, ev.Document.ID, ev.Document.Number, ev.Document.CustomerName, ev.Document.Total, ev.Document.PaidStatus, string(rawDoc))
	if err != nil {
		return false, nil, err
	}
	aff, _ := res.RowsAffected()
	if aff == 0 {
		// Already processed (Zero duplication guarantee - TODO-033, TC-016, TC-024)
		return true, nil, nil
	}

	// Reconcile stock deltas and detect simultaneous last-unit sales (TC-021, TODO-035)
	for _, it := range ev.Document.Items {
		var currentQty float64
		var prevRegister string
		err := s.HubDB.QueryRow(`SELECT quantity, last_register_id FROM hub_stock WHERE product_id = ?;`, it.ProductID).Scan(&currentQty, &prevRegister)
		if err == sql.ErrNoRows {
			currentQty = 0
			prevRegister = ""
		}

		newQty := currentQty - it.Quantity
		_, _ = s.HubDB.Exec(`
			INSERT INTO hub_stock (product_id, product_name, quantity, last_register_id, updated_at)
			VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(product_id) DO UPDATE SET
				product_name = excluded.product_name,
				quantity = excluded.quantity,
				last_register_id = excluded.last_register_id,
				updated_at = CURRENT_TIMESTAMP;
		`, it.ProductID, it.ProductName, newQty, ev.RegisterID)

		if newQty < 0 {
			otherReg := prevRegister
			if otherReg == "" {
				otherReg = "UNKNOWN"
			}
			details := fmt.Sprintf("Venta concurrente de última unidad (TC-021): stock previo=%.2f, vendido=%.2f por %s (venta previa en %s), stock resultante=%.2f",
				currentQty, it.Quantity, ev.RegisterID, otherReg, newQty)
			cRec := ConflictRecord{
				ConflictType: "LAST_UNIT_STOCK_CONFLICT",
				ProductID:    it.ProductID,
				ProductName:  it.ProductName,
				RegisterA:    otherReg,
				RegisterB:    ev.RegisterID,
				Resolution:   "DELTA_SUM_NEGATIVE_ALERT",
				Details:      details,
				CreatedAt:    time.Now().UTC(),
			}
			_, _ = s.HubDB.Exec(`
				INSERT INTO hub_conflicts (conflict_type, product_id, product_name, register_a, register_b, resolution, details)
				VALUES (?, ?, ?, ?, ?, ?, ?);
			`, cRec.ConflictType, cRec.ProductID, cRec.ProductName, cRec.RegisterA, cRec.RegisterB, cRec.Resolution, cRec.Details)
			conflicts = append(conflicts, cRec)
		}
	}

	return false, conflicts, nil
}

// ProcessCatalogEdit applies Last-Write-Wins catalog updates and logs concurrent edit conflicts (TC-022, TODO-035).
func (s *Server) ProcessCatalogEdit(edit CatalogEditPayload) (*ConflictRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if edit.UpdatedAtUTC.IsZero() {
		edit.UpdatedAtUTC = time.Now().UTC()
	}
	tsStr := edit.UpdatedAtUTC.Format(time.RFC3339Nano)

	var prevName, prevReg, prevTS string
	var prevPrice float64
	err := s.HubDB.QueryRow(`SELECT name, price, last_register_id, updated_at_utc FROM hub_catalog WHERE product_id = ?;`, edit.ProductID).
		Scan(&prevName, &prevPrice, &prevReg, &prevTS)

	var conflict *ConflictRecord
	if err == nil && prevReg != "" && !strings.EqualFold(prevReg, edit.RegisterID) {
		winner := edit.RegisterID
		resolution := "LAST_WRITE_WINS"
		if prevTS > tsStr {
			winner = prevReg
		}
		details := fmt.Sprintf("Edición concurrente de producto #%d (TC-022): %s ('%s' $%.2f @ %s) vs %s ('%s' $%.2f @ %s) -> Ganador: %s",
			edit.ProductID, prevReg, prevName, prevPrice, prevTS, edit.RegisterID, edit.Name, edit.Price, tsStr, winner)
		conflict = &ConflictRecord{
			ConflictType: "CONCURRENT_CATALOG_EDIT",
			ProductID:    edit.ProductID,
			ProductName:  edit.Name,
			RegisterA:    prevReg,
			RegisterB:    edit.RegisterID,
			Resolution:   resolution + " (" + winner + ")",
			Details:      details,
			CreatedAt:    time.Now().UTC(),
		}
		_, _ = s.HubDB.Exec(`
			INSERT INTO hub_conflicts (conflict_type, product_id, product_name, register_a, register_b, resolution, details)
			VALUES (?, ?, ?, ?, ?, ?, ?);
		`, conflict.ConflictType, conflict.ProductID, conflict.ProductName, conflict.RegisterA, conflict.RegisterB, conflict.Resolution, conflict.Details)

		if prevTS > tsStr {
			// Earlier timestamp loses under Last-Write-Wins
			return conflict, nil
		}
	}

	_, err = s.HubDB.Exec(`
		INSERT INTO hub_catalog (product_id, code, name, price, cost, last_register_id, updated_at_utc)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(product_id) DO UPDATE SET
			code = excluded.code,
			name = excluded.name,
			price = excluded.price,
			cost = excluded.cost,
			last_register_id = excluded.last_register_id,
			updated_at_utc = excluded.updated_at_utc;
	`, edit.ProductID, edit.Code, edit.Name, edit.Price, edit.Cost, edit.RegisterID, tsStr)
	return conflict, err
}

// Status returns the consolidated state of the Hub across all registers.
func (s *Server) Status() (*HubStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := &HubStatus{
		StoreID:         s.StoreID,
		SalesByRegister: make(map[string]float64),
		MasterStock:     make(map[int64]float64),
	}

	tRows, err := s.HubDB.Query(`SELECT register_id FROM hub_terminals ORDER BY register_id;`)
	if err == nil {
		for tRows.Next() {
			var r string
			if err := tRows.Scan(&r); err == nil {
				st.ConnectedTerminals = append(st.ConnectedTerminals, r)
			}
		}
		tRows.Close()
	}

	dRows, err := s.HubDB.Query(`SELECT register_id, total FROM hub_documents;`)
	if err == nil {
		for dRows.Next() {
			var reg string
			var tot float64
			if err := dRows.Scan(&reg, &tot); err == nil {
				st.TotalDocuments++
				st.TotalSalesAmount += tot
				st.SalesByRegister[reg] += tot
			}
		}
		dRows.Close()
	}

	sRows, err := s.HubDB.Query(`SELECT product_id, quantity FROM hub_stock;`)
	if err == nil {
		for sRows.Next() {
			var pid int64
			var qty float64
			if err := sRows.Scan(&pid, &qty); err == nil {
				st.MasterStock[pid] = qty
			}
		}
		sRows.Close()
	}

	cRows, err := s.HubDB.Query(`SELECT id, conflict_type, product_id, product_name, register_a, register_b, resolution, details FROM hub_conflicts ORDER BY id ASC;`)
	if err == nil {
		for cRows.Next() {
			var c ConflictRecord
			if err := cRows.Scan(&c.ID, &c.ConflictType, &c.ProductID, &c.ProductName, &c.RegisterA, &c.RegisterB, &c.Resolution, &c.Details); err == nil {
				st.Conflicts = append(st.Conflicts, c)
			}
		}
		cRows.Close()
	}

	return st, nil
}

// Handler returns the authenticated HTTP mux for the Central Hub server (TODO-032, TODO-066).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	authMiddleware := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.APIToken != "" {
				hdr := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
				if hdr == "" {
					hdr = r.Header.Get("X-Aronium-Hub-Token")
				}
				if hdr != s.APIToken {
					http.Error(w, "Unauthorized Hub Token", http.StatusUnauthorized)
					return
				}
			}
			next(w, r)
		}
	}

	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "store_id": s.StoreID})
	})

	mux.HandleFunc("/api/v1/sales", authMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var ev SaleEventPayload
		if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		dup, conflicts, err := s.ProcessSale(ev)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted":  true,
			"duplicate": dup,
			"conflicts": conflicts,
		})
	}))

	mux.HandleFunc("/api/v1/catalog", authMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var edit CatalogEditPayload
		if err := json.NewDecoder(r.Body).Decode(&edit); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		conflict, err := s.ProcessCatalogEdit(edit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted": true,
			"conflict": conflict,
		})
	}))

	mux.HandleFunc("/api/v1/status", authMiddleware(func(w http.ResponseWriter, r *http.Request) {
		st, err := s.Status()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(st)
	}))

	return mux
}

// Client is the offline-first POS workstation client that reports to the Central Hub (TODO-033, TC-015..016, TC-023..024).
type Client struct {
	ServerURL  string
	APIToken   string
	StoreID    string
	RegisterID string
	Hostname   string
	StateDB    *sql.DB
	HTTPClient *http.Client
}

func NewClient(serverURL, apiToken, storeID, registerID, hostname string, stateDB *sql.DB) *Client {
	return &Client{
		ServerURL:  strings.TrimRight(serverURL, "/"),
		APIToken:   apiToken,
		StoreID:    storeID,
		RegisterID: registerID,
		Hostname:   hostname,
		StateDB:    stateDB,
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// SubmitSale sends a sale to the Hub immediately, or enqueues it in sync_outbox if the Hub/LAN is offline (TODO-033).
func (c *Client) SubmitSale(ctx context.Context, doc db.Document) (queuedOffline bool, err error) {
	idempKey := fmt.Sprintf("hub:%s:%s:doc-%d", c.StoreID, c.RegisterID, doc.ID)
	payload := SaleEventPayload{
		IdempotencyKey: idempKey,
		StoreID:        c.StoreID,
		RegisterID:     c.RegisterID,
		Hostname:       c.Hostname,
		Document:       doc,
		TimestampUTC:   time.Now().UTC(),
	}
	raw, _ := json.Marshal(payload)

	if err := c.postJSON(ctx, "/api/v1/sales", raw); err != nil {
		// Offline-first fallback: persist in sync_outbox without blocking the POS terminal!
		if c.StateDB != nil {
			_, qErr := db.EnqueueOutbox(c.StateDB, idempKey, "hub", "SaleCreated", string(raw))
			return true, qErr
		}
		return false, err
	}
	return false, nil
}

// ReconcileOutbox flushes all queued offline sales to the Central Hub with zero duplication upon reconnection (TODO-033, TC-016, TC-024).
func (c *Client) ReconcileOutbox(ctx context.Context) (int, error) {
	if c.StateDB == nil {
		return 0, nil
	}
	items, err := db.ListPendingOutbox(c.StateDB, "hub", 20)
	if err != nil {
		return 0, err
	}
	reconciled := 0
	for _, it := range items {
		if err := c.postJSON(ctx, "/api/v1/sales", []byte(it.PayloadJSON)); err != nil {
			_ = db.MarkOutboxStatus(c.StateDB, it.ID, "FAILED", err.Error())
			continue
		}
		_ = db.MarkOutboxStatus(c.StateDB, it.ID, "SENT", "")
		reconciled++
	}
	return reconciled, nil
}

func (c *Client) postJSON(ctx context.Context, path string, raw []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ServerURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIToken)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("hub HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// DiscoverLAN broadcasts a UDP discovery probe on the local network or falls back to manual IP:port (TODO-034, AC-020, AC-021).
func DiscoverLAN(discoveryPort int, manualFallbackURL string, timeout time.Duration) string {
	if timeout <= 0 {
		timeout = 500 * time.Millisecond
	}
	conn, err := net.ListenPacket("udp4", ":0")
	if err == nil {
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(timeout))
		dst, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("255.255.255.255:%d", discoveryPort))
		if err == nil {
			_, _ = conn.WriteTo([]byte("ARONIUM_HUB_DISCOVER"), dst)
			buf := make([]byte, 256)
			if n, _, err := conn.ReadFrom(buf); err == nil && n > 0 {
				discovered := strings.TrimSpace(string(buf[:n]))
				if strings.HasPrefix(discovered, "http") {
					return discovered
				}
			}
		}
	}
	return manualFallbackURL
}

// CatalogCacheWarning returns the explicit architectural warning regarding Aronium Lite's in-memory product cache (TODO-036, TC-030, Section 6.1).
func CatalogCacheWarning() string {
	return "ADVERTENCIA (Sección 6.1 / TC-030): Aronium Lite carga el catálogo de productos en memoria al iniciar. Cualquier actualización de productos o precios sincronizada desde el Hub requiere cerrar y reiniciar Aronium Lite en esta estación para visualizarse en la pantalla de ventas."
}
