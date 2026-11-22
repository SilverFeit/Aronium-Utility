package accounts

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/SilverFeit/Aronium-Utility/internal/db"
	_ "modernc.org/sqlite"
)

// MovementType enumerates valid immutable ledger movement types in cuentas.db.
type MovementType string

const (
	MovementCharge     MovementType = "CARGO"
	MovementPayment    MovementType = "ABONO"
	MovementAdjustment MovementType = "AJUSTE"
)

// AccountStatus enumerates valid statuses for a customer account.
type AccountStatus string

const (
	StatusActive  AccountStatus = "ACTIVA"
	StatusBlocked AccountStatus = "BLOQUEADA"
	StatusClosed  AccountStatus = "CERRADA"
)

// MovementItemInput represents an item annotated on a credit account, frozen at transaction time.
type MovementItemInput struct {
	AroniumProductID       int64   `json:"aronium_product_id"`
	Barcode                string  `json:"codigo_barras"`
	NameSnapshot           string  `json:"nombre_snapshot"`
	UnitPriceSnapshot      int64   `json:"precio_unitario_snapshot"`
	Quantity               float64 `json:"cantidad"`
}

// MovementItem represents a persisted item snapshot in movimiento_items.
type MovementItem struct {
	ID                     int64   `json:"id"`
	MovementID             int64   `json:"movimiento_id"`
	AroniumProductID       int64   `json:"aronium_product_id"`
	Barcode                string  `json:"codigo_barras"`
	NameSnapshot           string  `json:"nombre_snapshot"`
	UnitPriceSnapshot      int64   `json:"precio_unitario_snapshot"`
	Quantity               float64 `json:"cantidad"`
	Subtotal               int64   `json:"subtotal"`
}

// Movement represents an immutable ledger entry in movimientos.
type Movement struct {
	ID            int64          `json:"id"`
	AccountID     string         `json:"cuenta_id"`
	Type          MovementType   `json:"tipo"`
	Amount        int64          `json:"monto"`
	PaymentMethod string         `json:"metodo_pago_usado"`
	Note          string         `json:"nota"`
	Date          string         `json:"fecha"`
	Items         []MovementItem `json:"items,omitempty"`
}

// AccountSummary represents a customer account and its derived ledger balance.
type AccountSummary struct {
	ID                 string        `json:"id"`
	Name               string        `json:"nombre"`
	Phone              string        `json:"telefono"`
	PaymentMethod      string        `json:"metodo_pago"`
	CreditLimit        int64         `json:"tope_credito"`
	Description        string        `json:"descripcion"`
	Status             AccountStatus `json:"estado"`
	CreatedAt          string        `json:"creado_en"`
	UpdatedAt          string        `json:"actualizado_en"`
	TotalCharges       int64         `json:"total_cargos"`
	TotalPayments      int64         `json:"total_abonos"`
	TotalAdjustments   int64         `json:"total_ajustes"`
	CurrentBalance     int64         `json:"saldo_actual"`
	AvailableCredit    int64         `json:"credito_disponible"`
	ExceedsCreditLimit bool          `json:"excede_tope"`
	Movements          []Movement    `json:"movimientos,omitempty"`
}

// AccountsLedgerReport summarizes all customer accounts in cuentas.db.
type AccountsLedgerReport struct {
	GeneratedAt         time.Time        `json:"generated_at"`
	AccountsDBPath      string           `json:"accounts_db_path"`
	TotalPendingBalance int64            `json:"total_saldo_pendiente"`
	ActiveAccountsCount int              `json:"cuentas_activas_con_saldo"`
	TotalAccountsCount  int              `json:"total_cuentas"`
	OverLimitCount      int              `json:"cuentas_sobre_tope"`
	Accounts            []AccountSummary `json:"cuentas"`
}

// CreateAccountRequest creates or updates a customer account and optionally records initial annotated items atomically.
type CreateAccountRequest struct {
	ID                  string              `json:"id"`
	Name                string              `json:"nombre"`
	Phone               string              `json:"telefono"`
	PaymentMethod       string              `json:"metodo_pago"`
	CreditLimit         int64               `json:"tope_credito"`
	Description         string              `json:"descripcion"`
	InitialChargeAmount int64               `json:"monto_inicial,omitempty"`
	Note                string              `json:"nota,omitempty"`
	StrictLimit         bool                `json:"strict_limit,omitempty"`
	Items               []MovementItemInput `json:"items,omitempty"`
}

// MovementRequest records a new CARGO, ABONO, or AJUSTE on an existing account.
type MovementRequest struct {
	AccountID     string              `json:"cuenta_id"`
	Type          MovementType        `json:"tipo"`
	Amount        int64               `json:"monto"`
	PaymentMethod string              `json:"metodo_pago_usado"`
	Note          string              `json:"nota"`
	StrictLimit   bool                `json:"strict_limit,omitempty"`
	Items         []MovementItemInput `json:"items,omitempty"`
}

// Manager operates on the dedicated cuentas.db SQLite WAL ledger database.
type Manager struct {
	DBPath string
	DB     *sql.DB
}

func toSQLiteWALURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	slash := filepath.ToSlash(abs)
	if !strings.HasPrefix(slash, "/") {
		slash = "/" + slash
	}
	u := url.URL{Scheme: "file", Path: slash}
	q := url.Values{}
	q.Set("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	u.RawQuery = q.Encode()
	return u.String()
}

// OpenAccountsDB opens or creates cuentas.db in WAL mode and runs idempotent schema migrations.
func OpenAccountsDB(accountsDBPath string) (*sql.DB, error) {
	if strings.TrimSpace(accountsDBPath) == "" {
		return nil, fmt.Errorf("accounts_db_path cannot be empty")
	}
	if err := os.MkdirAll(filepath.Dir(accountsDBPath), 0755); err != nil {
		return nil, err
	}
	conn, err := sql.Open("sqlite", toSQLiteWALURI(accountsDBPath))
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)
	if err := migrateAccountsDB(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func migrateAccountsDB(conn *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS cuentas (
			id TEXT PRIMARY KEY,
			nombre TEXT NOT NULL,
			telefono TEXT NOT NULL DEFAULT '',
			metodo_pago TEXT NOT NULL DEFAULT 'Efectivo',
			tope_credito INTEGER NOT NULL DEFAULT 0,
			descripcion TEXT NOT NULL DEFAULT '',
			estado TEXT NOT NULL DEFAULT 'ACTIVA' CHECK (estado IN ('ACTIVA', 'BLOQUEADA', 'CERRADA')),
			creado_en DATETIME DEFAULT CURRENT_TIMESTAMP,
			actualizado_en DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS movimientos (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			cuenta_id TEXT NOT NULL REFERENCES cuentas(id) ON DELETE CASCADE,
			tipo TEXT NOT NULL CHECK (tipo IN ('CARGO', 'ABONO', 'AJUSTE')),
			monto INTEGER NOT NULL,
			metodo_pago_usado TEXT NOT NULL DEFAULT '',
			nota TEXT NOT NULL DEFAULT '',
			fecha DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS movimiento_items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			movimiento_id INTEGER NOT NULL REFERENCES movimientos(id) ON DELETE CASCADE,
			aronium_product_id INTEGER NOT NULL DEFAULT 0,
			codigo_barras TEXT NOT NULL DEFAULT '',
			nombre_snapshot TEXT NOT NULL,
			precio_unitario_snapshot INTEGER NOT NULL,
			cantidad REAL NOT NULL DEFAULT 1,
			subtotal INTEGER NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_movimientos_cuenta ON movimientos(cuenta_id);`,
		`CREATE INDEX IF NOT EXISTS idx_movitems_mov ON movimiento_items(movimiento_id);`,
		`CREATE INDEX IF NOT EXISTS idx_movitems_prod ON movimiento_items(aronium_product_id);`,
	}
	for idx, q := range stmts {
		if _, err := conn.Exec(q); err != nil {
			return fmt.Errorf("accounts migration %d failed: %w", idx+1, err)
		}
	}
	return nil
}

// NewManager opens cuentas.db and returns an initialized Manager.
func NewManager(accountsDBPath string) (*Manager, error) {
	conn, err := OpenAccountsDB(accountsDBPath)
	if err != nil {
		return nil, err
	}
	return &Manager{
		DBPath: accountsDBPath,
		DB:     conn,
	}, nil
}

func (m *Manager) Close() error {
	if m.DB != nil {
		return m.DB.Close()
	}
	return nil
}

func computeItemsTotal(items []MovementItemInput) (int64, []MovementItem) {
	var total int64
	built := make([]MovementItem, 0, len(items))
	for _, it := range items {
		qty := it.Quantity
		if qty <= 0 {
			qty = 1
		}
		sub := int64(math.Round(qty * float64(it.UnitPriceSnapshot)))
		total += sub
		built = append(built, MovementItem{
			AroniumProductID:  it.AroniumProductID,
			Barcode:           strings.TrimSpace(it.Barcode),
			NameSnapshot:      strings.TrimSpace(it.NameSnapshot),
			UnitPriceSnapshot: it.UnitPriceSnapshot,
			Quantity:          qty,
			Subtotal:          sub,
		})
	}
	return total, built
}

// CreateOrUpdateAccountWithItems atomically creates/updates a customer account and optionally records an initial itemized CARGO.
func (m *Manager) CreateOrUpdateAccountWithItems(req CreateAccountRequest) (*AccountSummary, error) {
	id := strings.TrimSpace(req.ID)
	name := strings.TrimSpace(req.Name)
	phone := strings.TrimSpace(req.Phone)
	payMethod := strings.TrimSpace(req.PaymentMethod)
	desc := strings.TrimSpace(req.Description)

	if id == "" {
		return nil, fmt.Errorf("el campo ID / documento de la cuenta es obligatorio")
	}
	if name == "" {
		return nil, fmt.Errorf("el campo Nombre del cliente es obligatorio")
	}
	if req.CreditLimit < 0 {
		return nil, fmt.Errorf("el tope de crédito no puede ser negativo")
	}
	if payMethod == "" {
		payMethod = "Efectivo"
	}

	itemsTotal, preparedItems := computeItemsTotal(req.Items)
	chargeAmount := req.InitialChargeAmount
	if len(preparedItems) > 0 {
		chargeAmount = itemsTotal
	}

	tx, err := m.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	_, err = tx.Exec(`
		INSERT INTO cuentas (id, nombre, telefono, metodo_pago, tope_credito, descripcion, estado, creado_en, actualizado_en)
		VALUES (?, ?, ?, ?, ?, ?, 'ACTIVA', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			nombre = excluded.nombre,
			telefono = excluded.telefono,
			metodo_pago = excluded.metodo_pago,
			tope_credito = excluded.tope_credito,
			descripcion = excluded.descripcion,
			actualizado_en = CURRENT_TIMESTAMP;
	`, id, name, phone, payMethod, req.CreditLimit, desc)
	if err != nil {
		return nil, err
	}

	if chargeAmount > 0 || len(preparedItems) > 0 {
		var currentBalance int64
		_ = tx.QueryRow(`
			SELECT COALESCE(SUM(
				CASE
					WHEN tipo = 'CARGO' THEN monto
					WHEN tipo = 'ABONO' THEN -monto
					WHEN tipo = 'AJUSTE' THEN monto
					ELSE 0
				END
			), 0)
			FROM movimientos
			WHERE cuenta_id = ?;
		`, id).Scan(&currentBalance)

		if req.StrictLimit && req.CreditLimit > 0 && (currentBalance+chargeAmount) > req.CreditLimit {
			return nil, fmt.Errorf("tope de crédito excedido para %s: saldo actual $%d + nuevo cargo $%d = $%d supera el tope de $%d",
				name, currentBalance, chargeAmount, currentBalance+chargeAmount, req.CreditLimit)
		}

		note := strings.TrimSpace(req.Note)
		if note == "" {
			if len(preparedItems) > 0 {
				note = fmt.Sprintf("Anotación de %d artículo(s)", len(preparedItems))
			} else {
				note = "Cargo inicial en cuenta"
			}
		}

		movRes, err := tx.Exec(`
			INSERT INTO movimientos (cuenta_id, tipo, monto, metodo_pago_usado, nota, fecha)
			VALUES (?, 'CARGO', ?, ?, ?, CURRENT_TIMESTAMP);
		`, id, chargeAmount, payMethod, note)
		if err != nil {
			return nil, err
		}
		movID, _ := movRes.LastInsertId()

		for _, it := range preparedItems {
			if _, err := tx.Exec(`
				INSERT INTO movimiento_items (movimiento_id, aronium_product_id, codigo_barras, nombre_snapshot, precio_unitario_snapshot, cantidad, subtotal)
				VALUES (?, ?, ?, ?, ?, ?, ?);
			`, movID, it.AroniumProductID, it.Barcode, it.NameSnapshot, it.UnitPriceSnapshot, it.Quantity, it.Subtotal); err != nil {
				return nil, err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil

	return m.GetAccount(id)
}

// RecordMovement atomically adds a CARGO, ABONO, or AJUSTE movement to an existing customer account.
func (m *Manager) RecordMovement(req MovementRequest) (*AccountSummary, error) {
	accountID := strings.TrimSpace(req.AccountID)
	if accountID == "" {
		return nil, fmt.Errorf("cuenta_id es obligatorio")
	}
	if req.Type != MovementCharge && req.Type != MovementPayment && req.Type != MovementAdjustment {
		return nil, fmt.Errorf("tipo de movimiento inválido '%s' (permitidos: CARGO, ABONO, AJUSTE)", req.Type)
	}

	itemsTotal, preparedItems := computeItemsTotal(req.Items)
	amount := req.Amount
	if len(preparedItems) > 0 {
		amount = itemsTotal
	}
	if amount <= 0 && req.Type != MovementAdjustment {
		return nil, fmt.Errorf("el monto del movimiento debe ser mayor que cero")
	}

	tx, err := m.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	var creditLimit int64
	var status string
	var name string
	var defaultMethod string
	err = tx.QueryRow(`SELECT nombre, metodo_pago, tope_credito, estado FROM cuentas WHERE id = ?;`, accountID).
		Scan(&name, &defaultMethod, &creditLimit, &status)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("la cuenta '%s' no existe en cuentas.db", accountID)
	}
	if err != nil {
		return nil, err
	}
	if req.Type == MovementCharge && status != string(StatusActive) {
		return nil, fmt.Errorf("no se pueden anotar cargos en la cuenta '%s' porque su estado es %s", name, status)
	}

	if req.Type == MovementCharge && req.StrictLimit && creditLimit > 0 {
		var currentBalance int64
		_ = tx.QueryRow(`
			SELECT COALESCE(SUM(
				CASE
					WHEN tipo = 'CARGO' THEN monto
					WHEN tipo = 'ABONO' THEN -monto
					WHEN tipo = 'AJUSTE' THEN monto
					ELSE 0
				END
			), 0)
			FROM movimientos
			WHERE cuenta_id = ?;
		`, accountID).Scan(&currentBalance)
		if (currentBalance + amount) > creditLimit {
			return nil, fmt.Errorf("tope de crédito excedido para %s: saldo actual $%d + cargo $%d = $%d supera el tope de $%d",
				name, currentBalance, amount, currentBalance+amount, creditLimit)
		}
	}

	payMethod := strings.TrimSpace(req.PaymentMethod)
	if payMethod == "" {
		payMethod = defaultMethod
	}
	note := strings.TrimSpace(req.Note)
	if note == "" {
		switch req.Type {
		case MovementPayment:
			note = "Pago / Abono recibido en cuenta"
		case MovementCharge:
			note = fmt.Sprintf("Cargo por %d artículo(s)", len(preparedItems))
		default:
			note = "Ajuste de cuenta"
		}
	}

	movRes, err := tx.Exec(`
		INSERT INTO movimientos (cuenta_id, tipo, monto, metodo_pago_usado, nota, fecha)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP);
	`, accountID, string(req.Type), amount, payMethod, note)
	if err != nil {
		return nil, err
	}
	movID, _ := movRes.LastInsertId()

	for _, it := range preparedItems {
		if _, err := tx.Exec(`
			INSERT INTO movimiento_items (movimiento_id, aronium_product_id, codigo_barras, nombre_snapshot, precio_unitario_snapshot, cantidad, subtotal)
			VALUES (?, ?, ?, ?, ?, ?, ?);
		`, movID, it.AroniumProductID, it.Barcode, it.NameSnapshot, it.UnitPriceSnapshot, it.Quantity, it.Subtotal); err != nil {
			return nil, err
		}
	}

	_, _ = tx.Exec(`UPDATE cuentas SET actualizado_en = CURRENT_TIMESTAMP WHERE id = ?;`, accountID)

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil

	return m.GetAccount(accountID)
}

// UpdateStatus updates an account's operational state (ACTIVA, BLOQUEADA, CERRADA).
func (m *Manager) UpdateStatus(accountID string, status AccountStatus) error {
	res, err := m.DB.Exec(`UPDATE cuentas SET estado = ?, actualizado_en = CURRENT_TIMESTAMP WHERE id = ?;`, string(status), strings.TrimSpace(accountID))
	if err != nil {
		return err
	}
	aff, _ := res.RowsAffected()
	if aff == 0 {
		return fmt.Errorf("cuenta '%s' no encontrada", accountID)
	}
	return nil
}

// GetAccount retrieves a single customer account with all movements and item snapshots.
func (m *Manager) GetAccount(accountID string) (*AccountSummary, error) {
	rep, err := m.ListAccounts("")
	if err != nil {
		return nil, err
	}
	for i := range rep.Accounts {
		if strings.EqualFold(rep.Accounts[i].ID, strings.TrimSpace(accountID)) {
			acc := rep.Accounts[i]
			return &acc, nil
		}
	}
	return nil, fmt.Errorf("cuenta '%s' no encontrada", accountID)
}

// ListAccounts returns the full immutable ledger report across all accounts in cuentas.db.
func (m *Manager) ListAccounts(search string) (*AccountsLedgerReport, error) {
	rows, err := db.WithRetry(5, func() (*sql.Rows, error) {
		return m.DB.Query(`
			SELECT id, nombre, telefono, metodo_pago, tope_credito, descripcion, estado,
			       COALESCE(CAST(creado_en AS TEXT), ''), COALESCE(CAST(actualizado_en AS TEXT), '')
			FROM cuentas
			ORDER BY actualizado_en DESC, nombre ASC;
		`)
	})
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []AccountSummary
	accountIdx := make(map[string]int)

	for rows.Next() {
		var acc AccountSummary
		var statusStr string
		if err := rows.Scan(
			&acc.ID, &acc.Name, &acc.Phone, &acc.PaymentMethod,
			&acc.CreditLimit, &acc.Description, &statusStr,
			&acc.CreatedAt, &acc.UpdatedAt,
		); err != nil {
			return nil, err
		}
		acc.Status = AccountStatus(statusStr)
		accountIdx[acc.ID] = len(accounts)
		accounts = append(accounts, acc)
	}
	rows.Close()

	// Load all items grouped by movimiento_id
	itemsByMov := make(map[int64][]MovementItem)
	iRows, err := m.DB.Query(`
		SELECT id, movimiento_id, aronium_product_id, codigo_barras, nombre_snapshot, precio_unitario_snapshot, cantidad, subtotal
		FROM movimiento_items
		ORDER BY id ASC;
	`)
	if err == nil {
		for iRows.Next() {
			var it MovementItem
			if err := iRows.Scan(&it.ID, &it.MovementID, &it.AroniumProductID, &it.Barcode, &it.NameSnapshot, &it.UnitPriceSnapshot, &it.Quantity, &it.Subtotal); err == nil {
				itemsByMov[it.MovementID] = append(itemsByMov[it.MovementID], it)
			}
		}
		iRows.Close()
	}

	// Load all movements and compute derived ledger balances
	mRows, err := m.DB.Query(`
		SELECT id, cuenta_id, tipo, monto, metodo_pago_usado, nota, COALESCE(CAST(fecha AS TEXT), '')
		FROM movimientos
		ORDER BY id DESC;
	`)
	if err == nil {
		for mRows.Next() {
			var mv Movement
			var tipoStr string
			if err := mRows.Scan(&mv.ID, &mv.AccountID, &tipoStr, &mv.Amount, &mv.PaymentMethod, &mv.Note, &mv.Date); err == nil {
				mv.Type = MovementType(tipoStr)
				mv.Items = itemsByMov[mv.ID]
				if idx, ok := accountIdx[mv.AccountID]; ok {
					acc := &accounts[idx]
					acc.Movements = append(acc.Movements, mv)
					switch mv.Type {
					case MovementCharge:
						acc.TotalCharges += mv.Amount
					case MovementPayment:
						acc.TotalPayments += mv.Amount
					case MovementAdjustment:
						acc.TotalAdjustments += mv.Amount
					}
				}
			}
		}
		mRows.Close()
	}

	report := &AccountsLedgerReport{
		GeneratedAt:    time.Now().UTC(),
		AccountsDBPath: m.DBPath,
	}

	q := strings.ToLower(strings.TrimSpace(search))
	for i := range accounts {
		acc := &accounts[i]
		acc.CurrentBalance = acc.TotalCharges - acc.TotalPayments + acc.TotalAdjustments
		if acc.CreditLimit > 0 {
			avail := acc.CreditLimit - acc.CurrentBalance
			if avail < 0 {
				avail = 0
			}
			acc.AvailableCredit = avail
			acc.ExceedsCreditLimit = acc.CurrentBalance > acc.CreditLimit
		}

		if q != "" {
			if !strings.Contains(strings.ToLower(acc.ID), q) &&
				!strings.Contains(strings.ToLower(acc.Name), q) &&
				!strings.Contains(strings.ToLower(acc.Phone), q) {
				continue
			}
		}

		report.Accounts = append(report.Accounts, *acc)
		report.TotalAccountsCount++
		if acc.CurrentBalance > 0 {
			report.TotalPendingBalance += acc.CurrentBalance
			report.ActiveAccountsCount++
		}
		if acc.ExceedsCreditLimit {
			report.OverLimitCount++
		}
	}

	sort.Slice(report.Accounts, func(i, j int) bool {
		if report.Accounts[i].CurrentBalance != report.Accounts[j].CurrentBalance {
			return report.Accounts[i].CurrentBalance > report.Accounts[j].CurrentBalance
		}
		return report.Accounts[i].Name < report.Accounts[j].Name
	})

	return report, nil
}

// ComputePendingUnitsByProduct calculates committed product units in active accounts with pending balance (Ruta A Net Stock).
func (m *Manager) ComputePendingUnitsByProduct() (map[int64]float64, error) {
	rep, err := m.ListAccounts("")
	if err != nil {
		return nil, err
	}
	committed := make(map[int64]float64)
	for _, acc := range rep.Accounts {
		if acc.Status == StatusClosed || acc.CurrentBalance <= 0 || acc.TotalCharges <= 0 {
			continue
		}
		unpaidRatio := math.Min(1.0, float64(acc.CurrentBalance)/float64(acc.TotalCharges))
		for _, mv := range acc.Movements {
			if mv.Type != MovementCharge {
				continue
			}
			for _, it := range mv.Items {
				if it.AroniumProductID > 0 {
					committed[it.AroniumProductID] += math.Round(it.Quantity*unpaidRatio*10000) / 10000
				}
			}
		}
	}
	return committed, nil
}

// OnlineBackupAccountsDB creates a consistent hot binary copy of cuentas.db using SQLite VACUUM INTO.
func (m *Manager) OnlineBackupAccountsDB(ctx context.Context, destPath string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}
	tmpDest := destPath + ".tmp"
	_ = os.Remove(tmpDest)
	escaped := strings.ReplaceAll(filepath.ToSlash(tmpDest), "'", "''")
	_, err := db.WithRetry(6, func() (int, error) {
		_, e := m.DB.ExecContext(ctx, fmt.Sprintf(`VACUUM INTO '%s';`, escaped))
		return 0, e
	})
	if err != nil {
		_ = os.Remove(tmpDest)
		return fmt.Errorf("online backup of cuentas.db failed: %w", err)
	}
	return os.Rename(tmpDest, destPath)
}

// GenerateStatement formats an account statement ready to print or share with the customer.
func GenerateStatement(acc AccountSummary, storeName string) string {
	var sb strings.Builder
	sb.WriteString("====================================================\n")
	sb.WriteString("   ESTADO DE CUENTA DE CLIENTE\n")
	sb.WriteString(fmt.Sprintf("   Comercio: %s\n", storeName))
	sb.WriteString(fmt.Sprintf("   Fecha:    %s\n", time.Now().Format("2006-01-02 15:04")))
	sb.WriteString("====================================================\n")
	sb.WriteString(fmt.Sprintf("ID / Documento:  %s\n", acc.ID))
	sb.WriteString(fmt.Sprintf("Cliente:         %s\n", acc.Name))
	if acc.Phone != "" {
		sb.WriteString(fmt.Sprintf("Teléfono:        %s\n", acc.Phone))
	}
	sb.WriteString(fmt.Sprintf("Método de Pago:  %s\n", acc.PaymentMethod))
	if acc.Description != "" {
		sb.WriteString(fmt.Sprintf("Descripción:     %s\n", acc.Description))
	}
	sb.WriteString(fmt.Sprintf("Estado:          %s\n", acc.Status))
	sb.WriteString(fmt.Sprintf("Total Cargos:    $%d\n", acc.TotalCharges))
	sb.WriteString(fmt.Sprintf("Total Abonos:    $%d\n", acc.TotalPayments))
	if acc.TotalAdjustments != 0 {
		sb.WriteString(fmt.Sprintf("Total Ajustes:   $%d\n", acc.TotalAdjustments))
	}
	sb.WriteString(fmt.Sprintf("SALDO ACTUAL:    $%d\n", acc.CurrentBalance))
	if acc.CreditLimit > 0 {
		sb.WriteString(fmt.Sprintf("Tope Crédito:    $%d (Disponible: $%d)\n", acc.CreditLimit, acc.AvailableCredit))
	}
	sb.WriteString("----------------------------------------------------\n")
	sb.WriteString("HISTORIAL DE MOVIMIENTOS Y ARTÍCULOS:\n")
	for _, mv := range acc.Movements {
		sb.WriteString(fmt.Sprintf("- [%s] %-6s | $%d | %s | %s\n",
			mv.Date, mv.Type, mv.Amount, mv.PaymentMethod, mv.Note))
		for _, it := range mv.Items {
			sb.WriteString(fmt.Sprintf("    • %.2g x %s [%s] @ $%d = $%d\n",
				it.Quantity, it.NameSnapshot, it.Barcode, it.UnitPriceSnapshot, it.Subtotal))
		}
	}
	sb.WriteString("====================================================\n")
	return sb.String()
}
