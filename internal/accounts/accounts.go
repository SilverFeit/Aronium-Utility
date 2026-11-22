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
	AccountKey         string        `json:"account_key"`
	ID                 string        `json:"id"`
	IDPrefix           string        `json:"id_prefix,omitempty"`
	IDNumber           string        `json:"id_number,omitempty"`
	SharedIDCount      int           `json:"shared_id_count"`
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

// SchemaMigrationReport summarizes automatic schema upgrades detected when opening cuentas.db.
type SchemaMigrationReport struct {
	PreviousVersion    int    `json:"previous_version"`
	CurrentVersion     int    `json:"current_version"`
	Migrated           bool   `json:"migrated"`
	BackupPath         string `json:"backup_path,omitempty"`
	AccountsPreserved  int    `json:"accounts_preserved"`
	MovementsPreserved int    `json:"movements_preserved"`
	ItemsPreserved     int    `json:"items_preserved"`
}

const CurrentSchemaVersion = 2

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
	AccountKey          string              `json:"account_key,omitempty"`
	ID                  string              `json:"id"`
	IDPrefix            string              `json:"id_prefix,omitempty"`
	IDNumber            string              `json:"id_number,omitempty"`
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
	CustomerName  string              `json:"nombre,omitempty"`
	Type          MovementType        `json:"tipo"`
	Amount        int64               `json:"monto"`
	PaymentMethod string              `json:"metodo_pago_usado"`
	Note          string              `json:"nota"`
	StrictLimit   bool                `json:"strict_limit,omitempty"`
	Items         []MovementItemInput `json:"items,omitempty"`
}

// SplitCompoundID parses or joins the two-part ID (Text prefix + Numeric part, e.g. Parques-18502).
func SplitCompoundID(rawID, prefix, number string) (fullID, outPrefix, outNumber string) {
	p := strings.TrimSpace(prefix)
	n := strings.TrimSpace(number)
	raw := strings.TrimSpace(rawID)
	if p != "" || n != "" {
		outPrefix = strings.Trim(p, "-")
		outNumber = strings.Trim(n, "-")
		if outPrefix != "" && outNumber != "" {
			return outPrefix + "-" + outNumber, outPrefix, outNumber
		}
		if outPrefix != "" {
			return outPrefix, outPrefix, ""
		}
		return outNumber, "", outNumber
	}
	if idx := strings.LastIndex(raw, "-"); idx > 0 && idx < len(raw)-1 {
		outPrefix = strings.TrimSpace(raw[:idx])
		outNumber = strings.TrimSpace(raw[idx+1:])
		return outPrefix + "-" + outNumber, outPrefix, outNumber
	}
	return raw, "", raw
}

// MakeAccountKey builds a deterministic primary key distinguishing multiple customers sharing the same apartment ID.
func MakeAccountKey(id, name string) string {
	return strings.ToLower(strings.TrimSpace(id)) + "::" + strings.ToLower(strings.TrimSpace(name))
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

// EnsureOrRecoverAccountsDBFile snapshots cuentas.db if present, or restores it automatically from
// local/Drive backups if cuentas.db was deleted during an uninstall/reinstall cycle.
func EnsureOrRecoverAccountsDBFile(accountsDBPath string) string {
	baseDir := filepath.Dir(accountsDBPath)
	snapPath := filepath.Join(baseDir, "backups", "cuentas_pre_install_snapshot.db")

	if info, err := os.Stat(accountsDBPath); err == nil && info.Size() > 64 {
		if raw, rErr := os.ReadFile(accountsDBPath); rErr == nil && len(raw) > 64 {
			_ = os.MkdirAll(filepath.Dir(snapPath), 0755)
			_ = os.WriteFile(snapPath, raw, 0644)
		}
		return ""
	}

	candidates := []string{
		snapPath,
		filepath.Join(baseDir, "GoogleDriveSync", "AroniumUtility", "Cuentas_Credito", "cuentas_backup.db"),
		accountsDBPath + ".v1.bak",
	}
	for _, cand := range candidates {
		if raw, err := os.ReadFile(cand); err == nil && len(raw) > 64 {
			_ = os.MkdirAll(baseDir, 0755)
			if wErr := os.WriteFile(accountsDBPath, raw, 0644); wErr == nil {
				return cand
			}
		}
	}
	return ""
}

// OpenAccountsDB opens or creates cuentas.db in WAL mode and runs automatic lossless schema migrations.
func OpenAccountsDB(accountsDBPath string) (*sql.DB, error) {
	conn, _, err := OpenAccountsDBWithReport(accountsDBPath)
	return conn, err
}

// OpenAccountsDBWithReport opens or creates cuentas.db and reports any v1->v2 schema migration performed.
func OpenAccountsDBWithReport(accountsDBPath string) (*sql.DB, *SchemaMigrationReport, error) {
	if strings.TrimSpace(accountsDBPath) == "" {
		return nil, nil, fmt.Errorf("accounts_db_path cannot be empty")
	}
	if err := os.MkdirAll(filepath.Dir(accountsDBPath), 0755); err != nil {
		return nil, nil, err
	}
	EnsureOrRecoverAccountsDBFile(accountsDBPath)
	conn, err := sql.Open("sqlite", toSQLiteWALURI(accountsDBPath))
	if err != nil {
		return nil, nil, err
	}
	conn.SetMaxOpenConns(1)
	rep, err := migrateAccountsConn(conn, accountsDBPath)
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return conn, rep, nil
}

// MigrateAccountsDB opens cuentas.db, runs any needed lossless schema migration, and returns the migration report.
func MigrateAccountsDB(accountsDBPath string) (*SchemaMigrationReport, error) {
	conn, rep, err := OpenAccountsDBWithReport(accountsDBPath)
	if conn != nil {
		_ = conn.Close()
	}
	return rep, err
}

func migrateAccountsConn(conn *sql.DB, accountsDBPath string) (*SchemaMigrationReport, error) {
	var userVer int
	_ = conn.QueryRow(`PRAGMA user_version;`).Scan(&userVer)

	var cuentasExists int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='cuentas';`).Scan(&cuentasExists)

	if cuentasExists == 0 {
		if err := createFreshV2Schema(conn); err != nil {
			return nil, err
		}
		return &SchemaMigrationReport{
			PreviousVersion: userVer,
			CurrentVersion:  CurrentSchemaVersion,
			Migrated:        false,
		}, nil
	}

	hasAccountKey := false
	cols, err := conn.Query(`PRAGMA table_info(cuentas);`)
	if err != nil {
		return nil, fmt.Errorf("inspecting cuentas schema failed: %w", err)
	}
	for cols.Next() {
		var cid int
		var colName, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := cols.Scan(&cid, &colName, &colType, &notNull, &dflt, &pk); err == nil {
			if strings.EqualFold(colName, "account_key") {
				hasAccountKey = true
			}
		}
	}
	_ = cols.Close()

	if hasAccountKey {
		for _, idxStmt := range []string{
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_cuentas_id_nombre ON cuentas(LOWER(id), LOWER(nombre));`,
			`CREATE INDEX IF NOT EXISTS idx_cuentas_id ON cuentas(LOWER(id));`,
			`CREATE INDEX IF NOT EXISTS idx_movimientos_cuenta ON movimientos(cuenta_id);`,
			`CREATE INDEX IF NOT EXISTS idx_movitems_mov ON movimiento_items(movimiento_id);`,
			`CREATE INDEX IF NOT EXISTS idx_movitems_prod ON movimiento_items(aronium_product_id);`,
			`PRAGMA user_version = 2;`,
		} {
			if _, err := conn.Exec(idxStmt); err != nil {
				return nil, fmt.Errorf("v2 index migration failed: %w", err)
			}
		}
		var accCnt, movCnt, itemCnt int
		_ = conn.QueryRow(`SELECT COUNT(*) FROM cuentas;`).Scan(&accCnt)
		_ = conn.QueryRow(`SELECT COUNT(*) FROM movimientos;`).Scan(&movCnt)
		_ = conn.QueryRow(`SELECT COUNT(*) FROM movimiento_items;`).Scan(&itemCnt)
		prev := userVer
		if prev < CurrentSchemaVersion {
			prev = CurrentSchemaVersion
		}
		return &SchemaMigrationReport{
			PreviousVersion:    prev,
			CurrentVersion:     CurrentSchemaVersion,
			Migrated:           false,
			AccountsPreserved:  accCnt,
			MovementsPreserved: movCnt,
			ItemsPreserved:     itemCnt,
		}, nil
	}

	// Lossless v1 -> v2 upgrade: preserve every account, movement, and item while removing PK uniqueness on id
	_, _ = conn.Exec(`PRAGMA wal_checkpoint(TRUNCATE);`)
	backupPath := ""
	if accountsDBPath != "" {
		if rawDB, err := os.ReadFile(accountsDBPath); err == nil && len(rawDB) > 0 {
			candidateBak := accountsDBPath + ".v1.bak"
			if wErr := os.WriteFile(candidateBak, rawDB, 0644); wErr == nil {
				backupPath = candidateBak
			}
		}
	}

	type legacyAccountRow struct {
		ID            string
		Name          string
		Phone         string
		PaymentMethod string
		CreditLimit   int64
		Description   string
		Status        string
		CreatedAt     string
		UpdatedAt     string
	}
	var legacyRows []legacyAccountRow
	rRows, err := conn.Query(`
		SELECT id, nombre, telefono, metodo_pago, tope_credito, descripcion, estado,
		       COALESCE(CAST(creado_en AS TEXT), ''), COALESCE(CAST(actualizado_en AS TEXT), '')
		FROM cuentas;
	`)
	if err != nil {
		return nil, fmt.Errorf("reading legacy v1 cuentas failed: %w", err)
	}
	for rRows.Next() {
		var r legacyAccountRow
		if err := rRows.Scan(&r.ID, &r.Name, &r.Phone, &r.PaymentMethod, &r.CreditLimit, &r.Description, &r.Status, &r.CreatedAt, &r.UpdatedAt); err != nil {
			_ = rRows.Close()
			return nil, err
		}
		legacyRows = append(legacyRows, r)
	}
	_ = rRows.Close()

	// Disable foreign keys BEFORE beginning transaction (SQLite ignores PRAGMA foreign_keys inside tx)
	if _, err := conn.Exec(`PRAGMA foreign_keys = OFF;`); err != nil {
		return nil, err
	}
	defer func() {
		_, _ = conn.Exec(`PRAGMA foreign_keys = ON;`)
	}()

	tx, err := conn.Begin()
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	stageStmts := []string{
		`CREATE TABLE tmp_v1_movimientos AS SELECT id, cuenta_id, tipo, monto, metodo_pago_usado, nota, fecha FROM movimientos;`,
		`CREATE TABLE tmp_v1_movimiento_items AS SELECT id, movimiento_id, aronium_product_id, codigo_barras, nombre_snapshot, precio_unitario_snapshot, cantidad, subtotal FROM movimiento_items;`,
		`DROP TABLE IF EXISTS movimiento_items;`,
		`DROP TABLE IF EXISTS movimientos;`,
		`DROP TABLE IF EXISTS cuentas;`,
	}
	for _, s := range stageStmts {
		if _, err := tx.Exec(s); err != nil {
			return nil, fmt.Errorf("v1->v2 staging failed (%s): %w", s, err)
		}
	}

	for _, s := range v2DDLStatements() {
		if _, err := tx.Exec(s); err != nil {
			return nil, fmt.Errorf("v2 schema creation failed: %w", err)
		}
	}

	migratedKeys := make(map[string]string, len(legacyRows))
	for _, r := range legacyRows {
		fullID, idPrefix, idNumber := SplitCompoundID(r.ID, "", "")
		accountKey := MakeAccountKey(fullID, r.Name)
		migratedKeys[r.ID] = accountKey
		if _, err := tx.Exec(`
			INSERT INTO cuentas (account_key, id, id_prefix, id_number, nombre, telefono, metodo_pago, tope_credito, descripcion, estado, creado_en, actualizado_en)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, COALESCE(NULLIF(?, ''), CURRENT_TIMESTAMP), COALESCE(NULLIF(?, ''), CURRENT_TIMESTAMP));
		`, accountKey, fullID, idPrefix, idNumber, r.Name, r.Phone, r.PaymentMethod, r.CreditLimit, r.Description, r.Status, r.CreatedAt, r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("restoring legacy account '%s' failed: %w", r.ID, err)
		}
	}

	for oldID, newKey := range migratedKeys {
		if _, err := tx.Exec(`UPDATE tmp_v1_movimientos SET cuenta_id = ? WHERE cuenta_id = ?;`, newKey, oldID); err != nil {
			return nil, fmt.Errorf("updating legacy movement key '%s' failed: %w", oldID, err)
		}
	}

	restoreStmts := []string{
		`INSERT INTO movimientos (id, cuenta_id, tipo, monto, metodo_pago_usado, nota, fecha)
		 SELECT id, cuenta_id, tipo, monto, metodo_pago_usado, nota, fecha FROM tmp_v1_movimientos;`,
		`INSERT INTO movimiento_items (id, movimiento_id, aronium_product_id, codigo_barras, nombre_snapshot, precio_unitario_snapshot, cantidad, subtotal)
		 SELECT id, movimiento_id, aronium_product_id, codigo_barras, nombre_snapshot, precio_unitario_snapshot, cantidad, subtotal FROM tmp_v1_movimiento_items;`,
		`DROP TABLE tmp_v1_movimiento_items;`,
		`DROP TABLE tmp_v1_movimientos;`,
		`PRAGMA user_version = 2;`,
	}
	for _, s := range restoreStmts {
		if _, err := tx.Exec(s); err != nil {
			return nil, fmt.Errorf("v1->v2 restore failed: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil

	_, _ = conn.Exec(`PRAGMA foreign_keys = ON;`)

	var movCnt, itemCnt int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM movimientos;`).Scan(&movCnt)
	_ = conn.QueryRow(`SELECT COUNT(*) FROM movimiento_items;`).Scan(&itemCnt)

	prevVer := userVer
	if prevVer == 0 {
		prevVer = 1
	}
	return &SchemaMigrationReport{
		PreviousVersion:    prevVer,
		CurrentVersion:     CurrentSchemaVersion,
		Migrated:           true,
		BackupPath:         backupPath,
		AccountsPreserved:  len(legacyRows),
		MovementsPreserved: movCnt,
		ItemsPreserved:     itemCnt,
	}, nil
}

func v2DDLStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS cuentas (
			account_key TEXT PRIMARY KEY,
			id TEXT NOT NULL,
			id_prefix TEXT NOT NULL DEFAULT '',
			id_number TEXT NOT NULL DEFAULT '',
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
			cuenta_id TEXT NOT NULL REFERENCES cuentas(account_key) ON DELETE CASCADE,
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
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_cuentas_id_nombre ON cuentas(LOWER(id), LOWER(nombre));`,
		`CREATE INDEX IF NOT EXISTS idx_cuentas_id ON cuentas(LOWER(id));`,
		`CREATE INDEX IF NOT EXISTS idx_movimientos_cuenta ON movimientos(cuenta_id);`,
		`CREATE INDEX IF NOT EXISTS idx_movitems_mov ON movimiento_items(movimiento_id);`,
		`CREATE INDEX IF NOT EXISTS idx_movitems_prod ON movimiento_items(aronium_product_id);`,
	}
}

func createFreshV2Schema(conn *sql.DB) error {
	stmts := append(v2DDLStatements(), `PRAGMA user_version = 2;`)
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

type queryRower interface {
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}

func resolveAccountRow(qr queryRower, idOrKey, customerName string) (accountKey, displayID, name, defaultMethod, status string, creditLimit int64, err error) {
	key := strings.TrimSpace(idOrKey)
	cust := strings.TrimSpace(customerName)

	// 1. Direct match on immutable account_key
	err = qr.QueryRow(`SELECT account_key, id, nombre, metodo_pago, tope_credito, estado FROM cuentas WHERE LOWER(account_key) = LOWER(?);`, key).
		Scan(&accountKey, &displayID, &name, &defaultMethod, &creditLimit, &status)
	if err == nil {
		return
	}

	// 2. Match by compound ID + customer name
	if cust != "" {
		err = qr.QueryRow(`SELECT account_key, id, nombre, metodo_pago, tope_credito, estado FROM cuentas WHERE LOWER(id) = LOWER(?) AND LOWER(nombre) = LOWER(?);`, key, cust).
			Scan(&accountKey, &displayID, &name, &defaultMethod, &creditLimit, &status)
		if err == nil {
			return
		}
	}

	// 3. Match by compound ID (succeeds only when unambiguous, or explains all names sharing the ID)
	rows, qErr := qr.Query(`SELECT account_key, id, nombre, metodo_pago, tope_credito, estado FROM cuentas WHERE LOWER(id) = LOWER(?) ORDER BY nombre ASC;`, key)
	if qErr != nil {
		return "", "", "", "", "", 0, qErr
	}
	defer rows.Close()

	type candidate struct {
		accountKey, displayID, name, defaultMethod, status string
		creditLimit                                        int64
	}
	var cands []candidate
	for rows.Next() {
		var c candidate
		if sErr := rows.Scan(&c.accountKey, &c.displayID, &c.name, &c.defaultMethod, &c.creditLimit, &c.status); sErr == nil {
			cands = append(cands, c)
		}
	}
	_ = rows.Close()

	if len(cands) == 1 {
		c := cands[0]
		return c.accountKey, c.displayID, c.name, c.defaultMethod, c.status, c.creditLimit, nil
	}
	if len(cands) > 1 {
		var names []string
		for _, c := range cands {
			names = append(names, c.name)
		}
		return "", "", "", "", "", 0, fmt.Errorf("existen %d cuentas compartiendo el ID '%s' (%s); selecciona el cliente específico",
			len(cands), key, strings.Join(names, ", "))
	}
	return "", "", "", "", "", 0, fmt.Errorf("la cuenta '%s' no existe en cuentas.db", key)
}

// CreateOrUpdateAccountWithItems atomically creates/updates a customer account and optionally records an initial itemized CARGO.
func (m *Manager) CreateOrUpdateAccountWithItems(req CreateAccountRequest) (*AccountSummary, error) {
	fullID, idPrefix, idNumber := SplitCompoundID(req.ID, req.IDPrefix, req.IDNumber)
	name := strings.TrimSpace(req.Name)
	phone := strings.TrimSpace(req.Phone)
	payMethod := strings.TrimSpace(req.PaymentMethod)
	desc := strings.TrimSpace(req.Description)

	if fullID == "" {
		return nil, fmt.Errorf("el campo ID (Ej. Parques-18502) es obligatorio")
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

	var existingKey string
	if strings.TrimSpace(req.AccountKey) != "" {
		_ = tx.QueryRow(`SELECT account_key FROM cuentas WHERE LOWER(account_key) = LOWER(?);`, strings.TrimSpace(req.AccountKey)).Scan(&existingKey)
	}
	if existingKey == "" {
		_ = tx.QueryRow(`SELECT account_key FROM cuentas WHERE LOWER(id) = LOWER(?) AND LOWER(nombre) = LOWER(?);`, fullID, name).Scan(&existingKey)
	}

	accountKey := existingKey
	if accountKey != "" {
		_, err = tx.Exec(`
			UPDATE cuentas
			SET id = ?, id_prefix = ?, id_number = ?, nombre = ?, telefono = ?, metodo_pago = ?, tope_credito = ?, descripcion = ?, actualizado_en = CURRENT_TIMESTAMP
			WHERE account_key = ?;
		`, fullID, idPrefix, idNumber, name, phone, payMethod, req.CreditLimit, desc, accountKey)
	} else {
		accountKey = MakeAccountKey(fullID, name)
		_, err = tx.Exec(`
			INSERT INTO cuentas (account_key, id, id_prefix, id_number, nombre, telefono, metodo_pago, tope_credito, descripcion, estado, creado_en, actualizado_en)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'ACTIVA', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
		`, accountKey, fullID, idPrefix, idNumber, name, phone, payMethod, req.CreditLimit, desc)
	}
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
		`, accountKey).Scan(&currentBalance)

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
		`, accountKey, chargeAmount, payMethod, note)
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

	snapPath := filepath.Join(filepath.Dir(m.DBPath), "backups", "cuentas_pre_install_snapshot.db")
	_ = m.OnlineBackupAccountsDB(context.Background(), snapPath)

	return m.GetAccount(accountKey)
}

// RecordMovement atomically adds a CARGO, ABONO, or AJUSTE movement to an existing customer account.
func (m *Manager) RecordMovement(req MovementRequest) (*AccountSummary, error) {
	if strings.TrimSpace(req.AccountID) == "" {
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

	accountKey, _, name, defaultMethod, status, creditLimit, err := resolveAccountRow(tx, req.AccountID, req.CustomerName)
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
		`, accountKey).Scan(&currentBalance)
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
	`, accountKey, string(req.Type), amount, payMethod, note)
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

	_, _ = tx.Exec(`UPDATE cuentas SET actualizado_en = CURRENT_TIMESTAMP WHERE account_key = ?;`, accountKey)

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil

	snapPath := filepath.Join(filepath.Dir(m.DBPath), "backups", "cuentas_pre_install_snapshot.db")
	_ = m.OnlineBackupAccountsDB(context.Background(), snapPath)

	return m.GetAccount(accountKey)
}

// UpdateStatus updates an account's operational state (ACTIVA, BLOQUEADA, CERRADA).
func (m *Manager) UpdateStatus(accountID string, status AccountStatus) error {
	return m.UpdateStatusByName(accountID, "", status)
}

// UpdateStatusByName updates an account's operational state disambiguating by customer name when sharing an ID.
func (m *Manager) UpdateStatusByName(accountID, customerName string, status AccountStatus) error {
	accountKey, _, _, _, _, _, err := resolveAccountRow(m.DB, accountID, customerName)
	if err != nil {
		return err
	}
	_, err = m.DB.Exec(`UPDATE cuentas SET estado = ?, actualizado_en = CURRENT_TIMESTAMP WHERE account_key = ?;`, string(status), accountKey)
	return err
}

// GetAccount retrieves a single customer account by account_key or unique ID.
func (m *Manager) GetAccount(accountID string) (*AccountSummary, error) {
	return m.GetAccountByIDAndName(accountID, "")
}

// GetAccountByIDAndName retrieves a customer account using account_key or (id, nombre).
func (m *Manager) GetAccountByIDAndName(accountID, customerName string) (*AccountSummary, error) {
	accountKey, _, _, _, _, _, err := resolveAccountRow(m.DB, accountID, customerName)
	if err != nil {
		return nil, err
	}
	rep, err := m.ListAccounts("")
	if err != nil {
		return nil, err
	}
	for i := range rep.Accounts {
		if strings.EqualFold(rep.Accounts[i].AccountKey, accountKey) {
			acc := rep.Accounts[i]
			return &acc, nil
		}
	}
	return nil, fmt.Errorf("cuenta '%s' no encontrada", accountID)
}

// ListAccountsByID returns all customer accounts sharing a compound apartment/location ID (e.g. Parques-18502).
func (m *Manager) ListAccountsByID(sharedID string) ([]AccountSummary, error) {
	rep, err := m.ListAccounts("")
	if err != nil {
		return nil, err
	}
	target := strings.ToLower(strings.TrimSpace(sharedID))
	var out []AccountSummary
	for _, acc := range rep.Accounts {
		if strings.ToLower(acc.ID) == target {
			out = append(out, acc)
		}
	}
	return out, nil
}

// ListAccounts returns the full immutable ledger report across all accounts in cuentas.db.
func (m *Manager) ListAccounts(search string) (*AccountsLedgerReport, error) {
	rows, err := db.WithRetry(5, func() (*sql.Rows, error) {
		return m.DB.Query(`
			SELECT account_key, id, id_prefix, id_number, nombre, telefono, metodo_pago, tope_credito, descripcion, estado,
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
	idCounts := make(map[string]int)

	for rows.Next() {
		var acc AccountSummary
		var statusStr string
		if err := rows.Scan(
			&acc.AccountKey, &acc.ID, &acc.IDPrefix, &acc.IDNumber,
			&acc.Name, &acc.Phone, &acc.PaymentMethod,
			&acc.CreditLimit, &acc.Description, &statusStr,
			&acc.CreatedAt, &acc.UpdatedAt,
		); err != nil {
			return nil, err
		}
		acc.Status = AccountStatus(statusStr)
		accountIdx[acc.AccountKey] = len(accounts)
		idCounts[strings.ToLower(strings.TrimSpace(acc.ID))]++
		accounts = append(accounts, acc)
	}
	rows.Close()

	for i := range accounts {
		accounts[i].SharedIDCount = idCounts[strings.ToLower(strings.TrimSpace(accounts[i].ID))]
	}

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
				!strings.Contains(strings.ToLower(acc.AccountKey), q) &&
				!strings.Contains(strings.ToLower(acc.Name), q) &&
				!strings.Contains(strings.ToLower(acc.Phone), q) &&
				!strings.Contains(strings.ToLower(acc.Description), q) {
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
	sb.WriteString(fmt.Sprintf("ID Cuenta:       %s\n", acc.ID))
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
