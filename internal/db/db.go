package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// CoreTables lists the essential Aronium Lite SQLite tables required by Aronium Utility (TODO-012).
var CoreTables = []string{
	"Customer",
	"Document",
	"DocumentItem",
	"Payment",
	"PaymentType",
	"Product",
	"ProductGroup",
	"Stock",
}

// OptionalTables lists additional tables inspected when available in pos.db.
var OptionalTables = []string{
	"Barcode",
	"Warehouse",
	"Tax",
	"User",
	"DocumentType",
	"CashRegister",
	"ApplicationProperty",
}

// WithRetry executes an operation with exponential backoff when SQLITE_BUSY / locked occurs (TODO-009).
func WithRetry[T any](maxAttempts int, fn func() (T, error)) (T, error) {
	var zero T
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	backoff := 40 * time.Millisecond
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		res, err := fn()
		if err == nil {
			return res, nil
		}
		lastErr = err
		errLower := strings.ToLower(err.Error())
		if !strings.Contains(errLower, "busy") && !strings.Contains(errLower, "locked") {
			return zero, err
		}
		time.Sleep(backoff)
		backoff *= 2
	}
	return zero, fmt.Errorf("sqlite busy after %d attempts: %w", maxAttempts, lastErr)
}

func toSQLiteFileURI(path string, readOnly bool) string {
	return toSQLiteFileURIWithKey(path, readOnly, "")
}

func toSQLiteFileURIWithKey(path string, readOnly bool, key string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	slashPath := filepath.ToSlash(abs)
	if !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	u := url.URL{
		Scheme: "file",
		Path:   slashPath,
	}
	q := url.Values{}
	if strings.TrimSpace(key) != "" {
		q.Add("_pragma", fmt.Sprintf("key('%s')", strings.ReplaceAll(key, "'", "''")))
	}
	if readOnly {
		q.Set("mode", "ro")
		q.Add("_pragma", "query_only(1)")
	} else {
		q.Add("_pragma", "journal_mode(WAL)")
	}
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	return u.String()
}

// OpenPosDBForOnlineBackup opens pos.db with busy_timeout=5000 for SQLite's non-mutating VACUUM INTO online backup API (TODO-038).
func OpenPosDBForOnlineBackup(posDBPath string) (*sql.DB, error) {
	if _, err := os.Stat(posDBPath); err != nil {
		return nil, fmt.Errorf("pos.db not found at %s: %w", posDBPath, err)
	}
	abs, err := filepath.Abs(posDBPath)
	if err != nil {
		abs = posDBPath
	}
	slashPath := filepath.ToSlash(abs)
	if !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	u := url.URL{Scheme: "file", Path: slashPath}
	q := url.Values{}
	q.Set("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	return sql.Open("sqlite", u.String())
}

// OpenPosDBReadOnly opens Aronium Lite's pos.db in strict read-only mode (TODO-009).
func OpenPosDBReadOnly(posDBPath string) (*sql.DB, error) {
	return OpenPosDBReadOnlyWithKey(posDBPath, os.Getenv("ARONIUM_POS_DB_PASSWORD"))
}

// OpenPosDBReadOnlyWithKey opens pos.db in strict read-only mode applying an optional SQLite key PRAGMA.
func OpenPosDBReadOnlyWithKey(posDBPath, key string) (*sql.DB, error) {
	if _, err := os.Stat(posDBPath); err != nil {
		return nil, fmt.Errorf("pos.db not found at %s: %w", posDBPath, err)
	}
	dsn := toSQLiteFileURIWithKey(posDBPath, true, key)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(4)
	conn.SetMaxIdleConns(2)

	// Verify read-only query_only pragma and readability
	_, err = WithRetry(5, func() (int, error) {
		if _, e := conn.Exec("PRAGMA query_only = ON; PRAGMA busy_timeout = 5000;"); e != nil {
			return 0, e
		}
		var cnt int
		if e := conn.QueryRow("SELECT COUNT(*) FROM sqlite_master;").Scan(&cnt); e != nil {
			return 0, e
		}
		return cnt, nil
	})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

// OpenStateDB opens or creates aronium_utility_state.db and runs versioned migrations (TODO-010).
func OpenStateDB(stateDBPath string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(stateDBPath), 0755); err != nil {
		return nil, err
	}
	dsn := toSQLiteFileURI(stateDBPath, false)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)
	if err := migrateStateDB(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func migrateStateDB(conn *sql.DB) error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS sync_checkpoints (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			last_document_id INTEGER NOT NULL DEFAULT 0,
			last_payment_id INTEGER NOT NULL DEFAULT 0,
			stock_hash TEXT NOT NULL DEFAULT '',
			catalog_hash TEXT NOT NULL DEFAULT '',
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		INSERT OR IGNORE INTO sync_checkpoints (id, last_document_id, last_payment_id, stock_hash, catalog_hash)
		VALUES (1, 0, 0, '', '');`,
		`CREATE TABLE IF NOT EXISTS credit_notes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			customer_id INTEGER NOT NULL DEFAULT 0,
			customer_name TEXT NOT NULL DEFAULT '',
			document_id INTEGER NOT NULL DEFAULT 0,
			document_num TEXT NOT NULL DEFAULT '',
			note TEXT NOT NULL,
			due_promise TEXT NOT NULL DEFAULT '',
			created_by TEXT NOT NULL DEFAULT 'CLI',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS customer_credit_limits (
			customer_id INTEGER PRIMARY KEY,
			customer_name TEXT NOT NULL,
			credit_limit REAL NOT NULL DEFAULT 500000,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS sync_outbox (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			idempotency_key TEXT NOT NULL UNIQUE,
			target TEXT NOT NULL,
			event_type TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'PENDING',
			attempts INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS register_metadata (
			register_id TEXT PRIMARY KEY,
			register_name TEXT NOT NULL,
			store_id TEXT NOT NULL,
			hostname TEXT NOT NULL,
			ip_address TEXT NOT NULL DEFAULT '',
			printers_json TEXT NOT NULL DEFAULT '{}',
			last_heartbeat DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS kardex_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			document_id INTEGER NOT NULL,
			document_number TEXT NOT NULL,
			product_id INTEGER NOT NULL,
			product_code TEXT NOT NULL,
			product_name TEXT NOT NULL,
			delta_qty REAL NOT NULL,
			resulting_qty REAL NOT NULL,
			register_id TEXT NOT NULL,
			recorded_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(document_id, product_id)
		);`,
		`CREATE TABLE IF NOT EXISTS product_expirations (
			product_id INTEGER PRIMARY KEY,
			product_name TEXT NOT NULL,
			batch_lot TEXT NOT NULL DEFAULT '',
			expiration_date TEXT NOT NULL,
			min_stock_override REAL NOT NULL DEFAULT -1,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS price_lists (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			list_name TEXT NOT NULL,
			product_id INTEGER NOT NULL,
			product_name TEXT NOT NULL,
			custom_price REAL NOT NULL,
			currency TEXT NOT NULL DEFAULT 'USD',
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(list_name, product_id)
		);`,
		`CREATE TABLE IF NOT EXISTS fiscal_config (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			issuer_tax_id TEXT NOT NULL,
			issuer_name TEXT NOT NULL,
			prefix TEXT NOT NULL,
			authorization_number TEXT NOT NULL,
			range_from INTEGER NOT NULL,
			range_to INTEGER NOT NULL,
			current_consecutive INTEGER NOT NULL DEFAULT 1,
			environment TEXT NOT NULL DEFAULT 'test',
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS fiscal_documents (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			aronium_document_id INTEGER NOT NULL UNIQUE,
			aronium_document_number TEXT NOT NULL,
			fiscal_number TEXT NOT NULL,
			customer_tax_id TEXT NOT NULL,
			customer_name TEXT NOT NULL,
			customer_email TEXT NOT NULL,
			total REAL NOT NULL,
			status TEXT NOT NULL DEFAULT 'PENDING',
			document_uuid TEXT NOT NULL DEFAULT '',
			qr_data TEXT NOT NULL DEFAULT '',
			xml_path TEXT NOT NULL DEFAULT '',
			pdf_path TEXT NOT NULL DEFAULT '',
			api_response TEXT NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
	}

	for idx, stmt := range migrations {
		if _, err := conn.Exec(stmt); err != nil {
			return fmt.Errorf("state migration %d failed: %w", idx+1, err)
		}
		_, _ = conn.Exec(`INSERT OR IGNORE INTO schema_migrations (version) VALUES (?)`, idx+1)
	}
	return nil
}

// AttachPosDBReadOnly attaches pos.db as schema `aronium_pos` in read-only mode onto stateDB (TODO-011).
func AttachPosDBReadOnly(stateDB *sql.DB, posDBPath string) error {
	uri := toSQLiteFileURIWithKey(posDBPath, true, os.Getenv("ARONIUM_POS_DB_PASSWORD"))
	_, err := WithRetry(5, func() (int, error) {
		_, e := stateDB.Exec(fmt.Sprintf(`ATTACH DATABASE '%s' AS aronium_pos;`, strings.ReplaceAll(uri, "'", "''")))
		return 0, e
	})
	return err
}

func tableColumns(conn *sql.DB, tableName string) map[string]bool {
	cols := make(map[string]bool)
	rows, err := conn.Query(fmt.Sprintf("PRAGMA table_info(%s);", tableName))
	if err != nil {
		return cols
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var cname, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &cname, &ctype, &notnull, &dflt, &pk); err == nil {
			cols[strings.ToLower(strings.TrimSpace(cname))] = true
		}
	}
	return cols
}

func colExpr(cols map[string]bool, alias, col, fallback string) string {
	if cols[strings.ToLower(col)] {
		if alias != "" {
			return fmt.Sprintf("COALESCE(%s.%s, %s)", alias, col, fallback)
		}
		return fmt.Sprintf("COALESCE(%s, %s)", col, fallback)
	}
	return fallback
}

func colTextExpr(cols map[string]bool, alias, col, fallback string) string {
	if cols[strings.ToLower(col)] {
		if alias != "" {
			return fmt.Sprintf("COALESCE(CAST(%s.%s AS TEXT), %s)", alias, col, fallback)
		}
		return fmt.Sprintf("COALESCE(CAST(%s AS TEXT), %s)", col, fallback)
	}
	return fallback
}

// DetachPosDB detaches `aronium_pos` from stateDB.
func DetachPosDB(stateDB *sql.DB) error {
	_, err := stateDB.Exec(`DETACH DATABASE aronium_pos;`)
	return err
}

// IntrospectSchema returns detected tables, missing core tables, and schema version from pos.db (TODO-012, TODO-013).
func IntrospectSchema(posDB *sql.DB) (detected []string, missing []string, version string, err error) {
	rows, err := WithRetry(5, func() (*sql.Rows, error) {
		return posDB.Query(`SELECT name FROM sqlite_master WHERE type='table' ORDER BY name;`)
	})
	if err != nil {
		return nil, nil, "", err
	}
	defer rows.Close()

	tableSet := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			detected = append(detected, name)
			tableSet[strings.ToLower(name)] = true
		}
	}

	for _, req := range CoreTables {
		if !tableSet[strings.ToLower(req)] {
			missing = append(missing, req)
		}
	}

	version = "Aronium-Lite-SQLite-1.x"
	if tableSet["applicationproperty"] {
		var val string
		row := posDB.QueryRow(`SELECT Value FROM ApplicationProperty WHERE Name IN ('DatabaseVersion', 'Version', 'ApplicationVersion') LIMIT 1;`)
		if err := row.Scan(&val); err == nil && val != "" {
			version = val
		}
	}
	return detected, missing, version, nil
}

// ValidateSchemaCompatibility returns an error if pos.db is missing any core table or required column (TODO-013).
func ValidateSchemaCompatibility(posDB *sql.DB) (string, error) {
	_, missing, version, err := IntrospectSchema(posDB)
	if err != nil {
		return "", err
	}
	if len(missing) > 0 {
		return version, fmt.Errorf("incompatible Aronium pos.db schema (version=%s): missing core tables: %s", version, strings.Join(missing, ", "))
	}
	// Verify essential columns on Document and Customer
	requiredCols := map[string][]string{
		"Customer": {"Id", "Name"},
		"Document": {"Id", "CustomerId", "Total", "PaidStatus"},
		"Product":  {"Id", "Name", "Price"},
	}
	for table, cols := range requiredCols {
		foundCols := make(map[string]bool)
		rows, err := posDB.Query(fmt.Sprintf("PRAGMA table_info(%s);", table))
		if err != nil {
			return version, err
		}
		for rows.Next() {
			var cid int
			var cname, ctype string
			var notnull int
			var dflt sql.NullString
			var pk int
			if err := rows.Scan(&cid, &cname, &ctype, &notnull, &dflt, &pk); err == nil {
				foundCols[strings.ToLower(cname)] = true
			}
		}
		rows.Close()
		for _, c := range cols {
			if !foundCols[strings.ToLower(c)] {
				return version, fmt.Errorf("schema drift detected in table %s: missing required column %s", table, c)
			}
		}
	}
	return version, nil
}

// ListCustomers retrieves all customers from pos.db tolerating schema differences across Aronium versions.
func ListCustomers(posDB *sql.DB) ([]Customer, error) {
	return WithRetry(5, func() ([]Customer, error) {
		if !tableExists(posDB, "Customer") {
			return nil, nil
		}
		cols := tableColumns(posDB, "Customer")
		query := fmt.Sprintf(`
			SELECT
				Id,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s
			FROM Customer
			ORDER BY %s ASC;
		`,
			colExpr(cols, "", "Code", "''"),
			colExpr(cols, "", "Name", "''"),
			colExpr(cols, "", "TaxNumber", "''"),
			colExpr(cols, "", "Address", "''"),
			colExpr(cols, "", "PostalCode", "''"),
			colExpr(cols, "", "City", "''"),
			colExpr(cols, "", "Email", "''"),
			colExpr(cols, "", "PhoneNumber", "''"),
			colExpr(cols, "", "IsEnabled", "1"),
			colExpr(cols, "", "IsCustomer", "1"),
			colExpr(cols, "", "IsSupplier", "0"),
			colExpr(cols, "", "DueDatePeriod", "0"),
			func() string {
				if cols["name"] {
					return "Name"
				}
				return "Id"
			}(),
		)
		rows, err := posDB.Query(query)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []Customer
		for rows.Next() {
			var c Customer
			var enabled, isCust, isSupp int
			if err := rows.Scan(
				&c.ID, &c.Code, &c.Name, &c.TaxNumber, &c.Address,
				&c.PostalCode, &c.City, &c.Email, &c.PhoneNumber,
				&enabled, &isCust, &isSupp, &c.DueDatePeriod,
			); err != nil {
				return nil, err
			}
			c.IsEnabled = enabled != 0
			c.IsCustomer = isCust != 0
			c.IsSupplier = isSupp != 0
			out = append(out, c)
		}
		return out, nil
	})
}

// ListPaymentTypes retrieves all payment types from pos.db tolerating schema differences across Aronium versions.
func ListPaymentTypes(posDB *sql.DB) ([]PaymentType, error) {
	return WithRetry(5, func() ([]PaymentType, error) {
		if !tableExists(posDB, "PaymentType") {
			return nil, nil
		}
		cols := tableColumns(posDB, "PaymentType")
		orderCol := "Id ASC"
		if cols["ordinal"] {
			orderCol = "Ordinal ASC, Id ASC"
		}
		query := fmt.Sprintf(`
			SELECT
				Id,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s
			FROM PaymentType
			ORDER BY %s;
		`,
			colExpr(cols, "", "Name", "'Pago'"),
			colExpr(cols, "", "Code", "''"),
			colExpr(cols, "", "IsCustomerRequired", "0"),
			colExpr(cols, "", "IsFiscal", "1"),
			colExpr(cols, "", "IsChangeAllowed", "1"),
			colExpr(cols, "", "Ordinal", "0"),
			colExpr(cols, "", "IsEnabled", "1"),
			colExpr(cols, "", "IsQuickPayment", "1"),
			colExpr(cols, "", "OpenCashDrawer", "1"),
			colExpr(cols, "", "MarkAsPaid", "1"),
			orderCol,
		)
		rows, err := posDB.Query(query)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []PaymentType
		for rows.Next() {
			var pt PaymentType
			var custReq, fiscal, change, enabled, quick, drawer, markPaid int
			if err := rows.Scan(
				&pt.ID, &pt.Name, &pt.Code, &custReq, &fiscal,
				&change, &pt.Ordinal, &enabled, &quick, &drawer, &markPaid,
			); err != nil {
				return nil, err
			}
			pt.IsCustomerRequired = custReq != 0
			pt.IsFiscal = fiscal != 0
			pt.IsChangeAllowed = change != 0
			pt.IsEnabled = enabled != 0
			pt.IsQuickPayment = quick != 0
			pt.OpenCashDrawer = drawer != 0
			pt.MarkAsPaid = markPaid != 0
			out = append(out, pt)
		}
		return out, nil
	})
}

// ListDocuments retrieves documents from pos.db with customer details, items, and payments.
func ListDocuments(posDB *sql.DB, sinceDocID int64, defaultRegisterID string) ([]Document, error) {
	return WithRetry(5, func() ([]Document, error) {
		if !tableExists(posDB, "Document") {
			return nil, nil
		}
		dCols := tableColumns(posDB, "Document")
		hasCust := tableExists(posDB, "Customer")
		cCols := map[string]bool{}
		custJoin := ""
		if hasCust && dCols["customerid"] {
			cCols = tableColumns(posDB, "Customer")
			custJoin = "LEFT JOIN Customer c ON c.Id = d.CustomerId"
		}

		query := fmt.Sprintf(`
			SELECT
				d.Id,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s
			FROM Document d
			%s
			WHERE d.Id > ?
			ORDER BY d.Id ASC;
		`,
			colTextExpr(dCols, "d", "Number", "CAST(d.Id AS TEXT)"),
			colExpr(dCols, "d", "UserId", "0"),
			colExpr(cCols, "c", "Id", "0"),
			colExpr(cCols, "c", "Name", "'Consumidor Final'"),
			colExpr(cCols, "c", "TaxNumber", "'999999999999'"),
			colExpr(cCols, "c", "PhoneNumber", "''"),
			colExpr(cCols, "c", "Email", "''"),
			colExpr(dCols, "d", "OrderNumber", "''"),
			colTextExpr(dCols, "d", "Date", "''"),
			colTextExpr(dCols, "d", "StockDate", "''"),
			colExpr(dCols, "d", "Total", "0"),
			colExpr(dCols, "d", "DocumentTypeId", "0"),
			colExpr(dCols, "d", "WarehouseId", "0"),
			colExpr(dCols, "d", "ReferenceDocumentNumber", "''"),
			colExpr(dCols, "d", "InternalNote", "''"),
			colExpr(dCols, "d", "Note", "''"),
			colTextExpr(dCols, "d", "DueDate", "''"),
			colExpr(dCols, "d", "Discount", "0"),
			colExpr(dCols, "d", "DiscountType", "0"),
			colExpr(dCols, "d", "PaidStatus", "0"),
			custJoin,
		)
		rows, err := posDB.Query(query, sinceDocID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var docs []Document
		for rows.Next() {
			var doc Document
			if err := rows.Scan(
				&doc.ID, &doc.Number, &doc.UserID, &doc.CustomerID,
				&doc.CustomerName, &doc.CustomerTaxID, &doc.CustomerPhone, &doc.CustomerEmail,
				&doc.OrderNumber, &doc.Date, &doc.StockDate, &doc.Total,
				&doc.DocumentTypeID, &doc.WarehouseID, &doc.ReferenceDocNum,
				&doc.InternalNote, &doc.Note, &doc.DueDate,
				&doc.Discount, &doc.DiscountType, &doc.PaidStatus,
			); err != nil {
				return nil, err
			}
			doc.CashRegisterID = defaultRegisterID
			docs = append(docs, doc)
		}
		rows.Close()

		if len(docs) == 0 {
			return docs, nil
		}

		itemsByDoc, err := loadDocumentItemsSince(posDB, sinceDocID)
		if err == nil {
			for i := range docs {
				if its, ok := itemsByDoc[docs[i].ID]; ok {
					docs[i].Items = its
				}
			}
		}
		paymentsByDoc, err := loadDocumentPaymentsSince(posDB, sinceDocID)
		if err == nil {
			for i := range docs {
				if pms, ok := paymentsByDoc[docs[i].ID]; ok {
					docs[i].Payments = pms
				}
			}
		}
		return docs, nil
	})
}

func loadDocumentItemsSince(posDB *sql.DB, sinceDocID int64) (map[int64][]DocumentItem, error) {
	result := make(map[int64][]DocumentItem)
	if !tableExists(posDB, "DocumentItem") {
		return result, nil
	}
	diCols := tableColumns(posDB, "DocumentItem")
	if !diCols["documentid"] {
		return result, nil
	}
	hasProd := tableExists(posDB, "Product")
	pCols := map[string]bool{}
	prodJoin := ""
	if hasProd && diCols["productid"] {
		pCols = tableColumns(posDB, "Product")
		prodJoin = "LEFT JOIN Product p ON p.Id = di.ProductId"
	}

	costExpr := "0"
	if diCols["productcost"] && pCols["cost"] {
		costExpr = "COALESCE(di.ProductCost, COALESCE(p.Cost, 0))"
	} else if diCols["productcost"] {
		costExpr = "COALESCE(di.ProductCost, 0)"
	} else if pCols["cost"] {
		costExpr = "COALESCE(p.Cost, 0)"
	}

	totalExpr := "COALESCE(di.Quantity, 0) * COALESCE(di.Price, 0)"
	if diCols["total"] {
		totalExpr = "COALESCE(di.Total, COALESCE(di.Quantity, 0) * COALESCE(di.Price, 0))"
	}

	query := fmt.Sprintf(`
		SELECT
			di.Id,
			%s,
			%s,
			%s,
			%s,
			%s,
			%s,
			%s,
			%s,
			%s,
			%s
		FROM DocumentItem di
		%s
		WHERE di.DocumentId > ?
		ORDER BY di.Id ASC;
	`,
		colExpr(diCols, "di", "DocumentId", "0"),
		colExpr(diCols, "di", "ProductId", "0"),
		colExpr(pCols, "p", "Code", "''"),
		colExpr(pCols, "p", "Name", "'Producto #' || CAST(COALESCE(di.ProductId, di.Id) AS TEXT)"),
		colExpr(pCols, "p", "MeasurementUnit", "'UND'"),
		colExpr(diCols, "di", "Quantity", "0"),
		colExpr(diCols, "di", "Price", "0"),
		colExpr(diCols, "di", "Discount", "0"),
		costExpr,
		totalExpr,
		prodJoin,
	)

	rows, err := posDB.Query(query, sinceDocID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var it DocumentItem
		if err := rows.Scan(
			&it.ID, &it.DocumentID, &it.ProductID, &it.ProductCode,
			&it.ProductName, &it.UnitOfMeasurement, &it.Quantity,
			&it.Price, &it.Discount, &it.ProductCost, &it.Total,
		); err != nil {
			return nil, err
		}
		result[it.DocumentID] = append(result[it.DocumentID], it)
	}
	return result, nil
}

func loadDocumentPaymentsSince(posDB *sql.DB, sinceDocID int64) (map[int64][]Payment, error) {
	result := make(map[int64][]Payment)
	if !tableExists(posDB, "Payment") {
		return result, nil
	}
	pmCols := tableColumns(posDB, "Payment")
	if !pmCols["documentid"] {
		return result, nil
	}
	hasPT := tableExists(posDB, "PaymentType")
	ptCols := map[string]bool{}
	ptJoin := ""
	if hasPT && pmCols["paymenttypeid"] {
		ptCols = tableColumns(posDB, "PaymentType")
		ptJoin = "LEFT JOIN PaymentType pt ON pt.Id = pm.PaymentTypeId"
	}

	query := fmt.Sprintf(`
		SELECT
			pm.Id,
			%s,
			%s,
			%s,
			%s,
			%s,
			%s,
			%s
		FROM Payment pm
		%s
		WHERE pm.DocumentId > ?
		ORDER BY pm.Id ASC;
	`,
		colExpr(pmCols, "pm", "DocumentId", "0"),
		colExpr(pmCols, "pm", "PaymentTypeId", "0"),
		colExpr(ptCols, "pt", "Name", "'Pago'"),
		colExpr(ptCols, "pt", "MarkAsPaid", "1"),
		colExpr(pmCols, "pm", "Amount", "0"),
		colTextExpr(pmCols, "pm", "Date", "''"),
		colExpr(pmCols, "pm", "UserId", "0"),
		ptJoin,
	)

	rows, err := posDB.Query(query, sinceDocID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Payment
		var markPaid int
		if err := rows.Scan(
			&p.ID, &p.DocumentID, &p.PaymentTypeID, &p.PaymentTypeName,
			&markPaid, &p.Amount, &p.Date, &p.UserID,
		); err != nil {
			return nil, err
		}
		p.MarkAsPaid = markPaid != 0
		result[p.DocumentID] = append(result[p.DocumentID], p)
	}
	return result, nil
}

// ListProductsWithStock returns the complete product catalog joined dynamically across all Aronium Lite schema variants (TODO-022, TODO-103).
func ListProductsWithStock(posDB *sql.DB) ([]Product, error) {
	return WithRetry(5, func() ([]Product, error) {
		if !tableExists(posDB, "Product") {
			return nil, fmt.Errorf("tabla Product no encontrada en pos.db")
		}
		pCols := tableColumns(posDB, "Product")

		hasProductGroup := tableExists(posDB, "ProductGroup") && pCols["productgroupid"]
		pgNameExpr := "'General'"
		pgJoin := ""
		if hasProductGroup {
			pgCols := tableColumns(posDB, "ProductGroup")
			if pgCols["name"] {
				pgNameExpr = "COALESCE(pg.Name, 'General')"
				pgJoin = "LEFT JOIN ProductGroup pg ON pg.Id = p.ProductGroupId"
			}
		}

		hasStock := tableExists(posDB, "Stock")
		stockSelect := "0"
		stockJoin := ""
		if hasStock {
			stCols := tableColumns(posDB, "Stock")
			if stCols["productid"] && stCols["quantity"] {
				stockSelect = "COALESCE(st.TotalQty, 0)"
				stockJoin = "LEFT JOIN (SELECT ProductId, SUM(Quantity) AS TotalQty FROM Stock GROUP BY ProductId) st ON st.ProductId = p.Id"
			}
		}

		hasBarcode := tableExists(posDB, "Barcode")
		barcodeSelect := colExpr(pCols, "p", "Barcode", "''")
		allBarcodesSelect := barcodeSelect
		barcodeJoin := ""
		if hasBarcode {
			bcCols := tableColumns(posDB, "Barcode")
			if bcCols["productid"] && bcCols["value"] {
				if pCols["barcode"] {
					barcodeSelect = "COALESCE(NULLIF(bc.FirstBarcode, ''), COALESCE(p.Barcode, ''))"
					allBarcodesSelect = "COALESCE(bc.AllBarcodes, COALESCE(p.Barcode, ''))"
				} else {
					barcodeSelect = "COALESCE(bc.FirstBarcode, '')"
					allBarcodesSelect = "COALESCE(bc.AllBarcodes, '')"
				}
				barcodeJoin = "LEFT JOIN (SELECT ProductId, MIN(Value) AS FirstBarcode, GROUP_CONCAT(Value, '|') AS AllBarcodes FROM Barcode GROUP BY ProductId) bc ON bc.ProductId = p.Id"
			}
		}

		orderExpr := "p.Id ASC"
		if pCols["name"] {
			orderExpr = "p.Name ASC"
		}

		query := fmt.Sprintf(`
			SELECT
				p.Id,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s,
				%s
			FROM Product p
			%s
			%s
			%s
			ORDER BY %s;
		`,
			colExpr(pCols, "p", "ProductGroupId", "0"),
			pgNameExpr,
			colExpr(pCols, "p", "Name", "''"),
			colExpr(pCols, "p", "Code", "''"),
			barcodeSelect,
			allBarcodesSelect,
			colExpr(pCols, "p", "PLU", "0"),
			colExpr(pCols, "p", "MeasurementUnit", "'UND'"),
			colExpr(pCols, "p", "Price", "0"),
			colExpr(pCols, "p", "Cost", "0"),
			colExpr(pCols, "p", "Markup", "0"),
			colExpr(pCols, "p", "IsService", "0"),
			colExpr(pCols, "p", "IsEnabled", "1"),
			stockSelect,
			pgJoin,
			stockJoin,
			barcodeJoin,
			orderExpr,
		)

		rows, err := posDB.Query(query)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var products []Product
		for rows.Next() {
			var p Product
			var isService, isEnabled int
			var allBarcodes string
			if err := rows.Scan(
				&p.ID, &p.ProductGroupID, &p.ProductGroupName,
				&p.Name, &p.Code, &p.Barcode, &allBarcodes, &p.PLU,
				&p.MeasurementUnit, &p.Price, &p.Cost, &p.Markup,
				&isService, &isEnabled, &p.StockQuantity,
			); err != nil {
				return nil, err
			}
			p.IsService = isService != 0
			p.IsEnabled = isEnabled != 0
			seenBC := make(map[string]bool)
			if p.Barcode != "" {
				seenBC[strings.ToLower(p.Barcode)] = true
				p.Barcodes = append(p.Barcodes, p.Barcode)
			}
			if allBarcodes != "" {
				for _, b := range strings.Split(allBarcodes, "|") {
					trimmed := strings.TrimSpace(b)
					if trimmed != "" && !seenBC[strings.ToLower(trimmed)] {
						seenBC[strings.ToLower(trimmed)] = true
						p.Barcodes = append(p.Barcodes, trimmed)
					}
				}
			}
			products = append(products, p)
		}
		return products, nil
	})
}

// CatalogMatch bundles barcode scanner exact matches and partial text matches from pos.db (TODO-103).
type CatalogMatch struct {
	ExactBarcodeMatch *Product  `json:"exact_match,omitempty"`
	Matches           []Product `json:"matches"`
	PosDBMTime        string    `json:"pos_db_mtime"`
}

// CatalogCache caches the live product index in memory and invalidates automatically when pos.db or pos.db-wal changes (TODO-103).
type CatalogCache struct {
	mu           sync.RWMutex
	posDBPath    string
	lastModTime  time.Time
	products     []Product
	byBarcode    map[string]int
	byCode       map[string]int
}

func NewCatalogCache(posDBPath string) *CatalogCache {
	return &CatalogCache{
		posDBPath: posDBPath,
		byBarcode: make(map[string]int),
		byCode:    make(map[string]int),
	}
}

func currentDBMTime(posDBPath string) time.Time {
	var maxT time.Time
	for _, p := range []string{posDBPath, posDBPath + "-wal"} {
		if st, err := os.Stat(p); err == nil {
			if st.ModTime().After(maxT) {
				maxT = st.ModTime()
			}
		}
	}
	return maxT
}

// GetProducts returns the live product catalog, reloading from pos.db only if mtime changed.
func (c *CatalogCache) GetProducts(posDB *sql.DB) ([]Product, time.Time, error) {
	mtime := currentDBMTime(c.posDBPath)

	c.mu.RLock()
	if len(c.products) > 0 && !mtime.After(c.lastModTime) {
		prods := c.products
		lastM := c.lastModTime
		c.mu.RUnlock()
		return prods, lastM, nil
	}
	c.mu.RUnlock()

	products, err := ListProductsWithStock(posDB)
	if err != nil {
		return nil, mtime, err
	}

	byBarcode := make(map[string]int, len(products))
	byCode := make(map[string]int, len(products))
	for i, p := range products {
		if p.Barcode != "" {
			byBarcode[strings.ToLower(strings.TrimSpace(p.Barcode))] = i
		}
		for _, bc := range p.Barcodes {
			byBarcode[strings.ToLower(strings.TrimSpace(bc))] = i
		}
		if p.Code != "" {
			byCode[strings.ToLower(strings.TrimSpace(p.Code))] = i
		}
	}

	c.mu.Lock()
	c.products = products
	c.byBarcode = byBarcode
	c.byCode = byCode
	c.lastModTime = mtime
	c.mu.Unlock()

	return products, mtime, nil
}

// Search performs <10ms barcode exact lookup and partial name/SKU matching over the mtime-cached catalog (TODO-103).
func (c *CatalogCache) Search(posDB *sql.DB, query string, exactBarcode string, limit int) (*CatalogMatch, error) {
	products, mtime, err := c.GetProducts(posDB)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}

	res := &CatalogMatch{
		PosDBMTime: mtime.UTC().Format(time.RFC3339),
	}

	// Check exact barcode or SKU code (for USB/Bluetooth HID scanner Enter events)
	lookupCode := strings.ToLower(strings.TrimSpace(exactBarcode))
	if lookupCode == "" {
		lookupCode = strings.ToLower(strings.TrimSpace(query))
	}
	if lookupCode != "" {
		c.mu.RLock()
		if idx, ok := c.byBarcode[lookupCode]; ok {
			p := products[idx]
			res.ExactBarcodeMatch = &p
		} else if idx, ok := c.byCode[lookupCode]; ok {
			p := products[idx]
			res.ExactBarcodeMatch = &p
		}
		c.mu.RUnlock()
	}

	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" && exactBarcode != "" {
		q = strings.ToLower(strings.TrimSpace(exactBarcode))
	}

	for _, p := range products {
		if !p.IsEnabled {
			continue
		}
		if q == "" {
			res.Matches = append(res.Matches, p)
		} else {
			matched := strings.Contains(strings.ToLower(p.Name), q) ||
				strings.Contains(strings.ToLower(p.Code), q) ||
				strings.Contains(strings.ToLower(p.Barcode), q) ||
				strings.Contains(strings.ToLower(p.ProductGroupName), q)
			if !matched {
				for _, bc := range p.Barcodes {
					if strings.Contains(strings.ToLower(bc), q) {
						matched = true
						break
					}
				}
			}
			if matched {
				res.Matches = append(res.Matches, p)
			}
		}
		if len(res.Matches) >= limit {
			break
		}
	}

	return res, nil
}

// SearchCatalogForScanner searches live products in pos.db supporting both exact barcode/SKU match and partial text search (TODO-103).
func SearchCatalogForScanner(posDB *sql.DB, posDBPath, query, exactBarcode string, limit int) (*CatalogMatch, error) {
	cache := NewCatalogCache(posDBPath)
	return cache.Search(posDB, query, exactBarcode, limit)
}

func tableExists(conn *sql.DB, tableName string) bool {
	var count int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND LOWER(name)=LOWER(?);`, tableName).Scan(&count)
	return count > 0
}

// ComputeDatabaseSnapshot computes max Document.Id, max Payment.Id, Stock hash, and Catalog hash.
func ComputeDatabaseSnapshot(posDB *sql.DB) (*SyncCheckpoint, error) {
	return WithRetry(5, func() (*SyncCheckpoint, error) {
		cp := &SyncCheckpoint{UpdatedAt: time.Now().UTC()}
		_ = posDB.QueryRow(`SELECT COALESCE(MAX(Id), 0) FROM Document;`).Scan(&cp.LastDocumentID)
		_ = posDB.QueryRow(`SELECT COALESCE(MAX(Id), 0) FROM Payment;`).Scan(&cp.LastPaymentID)

		products, err := ListProductsWithStock(posDB)
		if err != nil {
			return cp, err
		}
		stockHasher := sha256.New()
		catHasher := sha256.New()
		for _, p := range products {
			fmt.Fprintf(stockHasher, "%d:%.4f;", p.ID, p.StockQuantity)
			fmt.Fprintf(catHasher, "%d:%s:%s:%.2f:%.2f:%v;", p.ID, p.Code, p.Name, p.Price, p.Cost, p.IsEnabled)
		}
		cp.StockHash = hex.EncodeToString(stockHasher.Sum(nil))[:16]
		cp.CatalogHash = hex.EncodeToString(catHasher.Sum(nil))[:16]
		return cp, nil
	})
}

// GetCheckpoint reads the saved checkpoint from aronium_utility_state.db.
func GetCheckpoint(stateDB *sql.DB) (*SyncCheckpoint, error) {
	cp := &SyncCheckpoint{}
	var updatedStr string
	err := stateDB.QueryRow(`
		SELECT last_document_id, last_payment_id, stock_hash, catalog_hash, COALESCE(CAST(updated_at AS TEXT), '')
		FROM sync_checkpoints WHERE id = 1;
	`).Scan(&cp.LastDocumentID, &cp.LastPaymentID, &cp.StockHash, &cp.CatalogHash, &updatedStr)
	if err != nil {
		return nil, err
	}
	cp.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
	return cp, nil
}

// SaveCheckpoint updates the checkpoint in aronium_utility_state.db.
func SaveCheckpoint(stateDB *sql.DB, cp *SyncCheckpoint) error {
	_, err := stateDB.Exec(`
		UPDATE sync_checkpoints
		SET last_document_id = ?, last_payment_id = ?, stock_hash = ?, catalog_hash = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = 1;
	`, cp.LastDocumentID, cp.LastPaymentID, cp.StockHash, cp.CatalogHash)
	return err
}

// AddCreditNote saves an extended customer/document note in aronium_utility_state.db without touching pos.db (TODO-020).
func AddCreditNote(stateDB *sql.DB, note CreditNote) (int64, error) {
	if note.CreatedBy == "" {
		note.CreatedBy = "CLI"
	}
	res, err := stateDB.Exec(`
		INSERT INTO credit_notes (customer_id, customer_name, document_id, document_num, note, due_promise, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?);
	`, note.CustomerID, note.CustomerName, note.DocumentID, note.DocumentNum, note.Note, note.DuePromise, note.CreatedBy)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListCreditNotes retrieves external customer credit notes from aronium_utility_state.db.
func ListCreditNotes(stateDB *sql.DB, customerFilter string) ([]CreditNote, error) {
	query := `
		SELECT id, customer_id, customer_name, document_id, document_num, note, due_promise, created_by, COALESCE(CAST(created_at AS TEXT), '')
		FROM credit_notes
	`
	var args []any
	if strings.TrimSpace(customerFilter) != "" {
		query += ` WHERE LOWER(customer_name) LIKE LOWER(?) OR CAST(customer_id AS TEXT) = ? OR LOWER(document_num) = LOWER(?) `
		args = append(args, "%"+customerFilter+"%", customerFilter, customerFilter)
	}
	query += ` ORDER BY id ASC;`

	rows, err := stateDB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var notes []CreditNote
	for rows.Next() {
		var n CreditNote
		var createdStr string
		if err := rows.Scan(&n.ID, &n.CustomerID, &n.CustomerName, &n.DocumentID, &n.DocumentNum, &n.Note, &n.DuePromise, &n.CreatedBy, &createdStr); err != nil {
			return nil, err
		}
		n.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdStr)
		notes = append(notes, n)
	}
	return notes, nil
}

// SetCustomerCreditLimit configures a customer's maximum credit limit in aronium_utility_state.db (TODO-021).
func SetCustomerCreditLimit(stateDB *sql.DB, customerID int64, customerName string, limit float64) error {
	_, err := stateDB.Exec(`
		INSERT INTO customer_credit_limits (customer_id, customer_name, credit_limit, updated_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(customer_id) DO UPDATE SET
			customer_name = excluded.customer_name,
			credit_limit = excluded.credit_limit,
			updated_at = CURRENT_TIMESTAMP;
	`, customerID, customerName, limit)
	return err
}

// GetCustomerCreditLimits returns a map of customerID -> custom credit limit.
func GetCustomerCreditLimits(stateDB *sql.DB) (map[int64]float64, error) {
	rows, err := stateDB.Query(`SELECT customer_id, credit_limit FROM customer_credit_limits;`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]float64)
	for rows.Next() {
		var cid int64
		var lim float64
		if err := rows.Scan(&cid, &lim); err == nil {
			out[cid] = lim
		}
	}
	return out, nil
}

// EnqueueOutbox inserts an idempotent item into the offline-first sync_outbox queue (TODO-010, TODO-028).
func EnqueueOutbox(stateDB *sql.DB, idempotencyKey, target, eventType, payloadJSON string) (bool, error) {
	res, err := stateDB.Exec(`
		INSERT OR IGNORE INTO sync_outbox (idempotency_key, target, event_type, payload_json, status)
		VALUES (?, ?, ?, ?, 'PENDING');
	`, idempotencyKey, target, eventType, payloadJSON)
	if err != nil {
		return false, err
	}
	affected, _ := res.RowsAffected()
	return affected > 0, nil
}

// ListPendingOutbox returns all PENDING or retryable FAILED outbox items for a given target.
func ListPendingOutbox(stateDB *sql.DB, target string, maxAttempts int) ([]OutboxItem, error) {
	if maxAttempts <= 0 {
		maxAttempts = 10
	}
	query := `
		SELECT id, idempotency_key, target, event_type, payload_json, status, attempts, last_error
		FROM sync_outbox
		WHERE status IN ('PENDING', 'FAILED') AND attempts < ?
	`
	args := []any{maxAttempts}
	if target != "" {
		query += ` AND target = ?`
		args = append(args, target)
	}
	query += ` ORDER BY id ASC;`

	rows, err := stateDB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []OutboxItem
	for rows.Next() {
		var item OutboxItem
		if err := rows.Scan(&item.ID, &item.IdempotencyKey, &item.Target, &item.EventType, &item.PayloadJSON, &item.Status, &item.Attempts, &item.LastError); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// MarkOutboxStatus updates the status of an outbox item after a sync attempt.
func MarkOutboxStatus(stateDB *sql.DB, id int64, status, lastErr string) error {
	_, err := stateDB.Exec(`
		UPDATE sync_outbox
		SET status = ?, attempts = attempts + 1, last_error = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?;
	`, status, lastErr, id)
	return err
}

// RunDoctor performs a complete health diagnosis of pos.db, stateDB, register identity, and modules (TODO-008).
func RunDoctor(ctx context.Context, posDBPath, stateDBPath, registerID, tokenVaultPath string) (*DoctorReport, error) {
	rep := &DoctorReport{
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		Healthy:        true,
		PosDBPath:      posDBPath,
		StateDBPath:    stateDBPath,
		CashRegisterID: registerID,
		Details:        make(map[string]string),
	}

	posConn, err := OpenPosDBReadOnly(posDBPath)
	if err != nil {
		rep.Healthy = false
		rep.PosDBReadable = false
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("Cannot open pos.db in read-only mode: %v", err))
	} else {
		defer posConn.Close()
		rep.PosDBReadable = true

		// PRAGMA quick_check in read-only mode
		var qc string
		if err := posConn.QueryRowContext(ctx, `PRAGMA quick_check;`).Scan(&qc); err != nil {
			rep.QuickCheckResult = "error: " + err.Error()
			rep.Healthy = false
		} else {
			rep.QuickCheckResult = qc
			if strings.ToLower(qc) != "ok" {
				rep.Healthy = false
				rep.Warnings = append(rep.Warnings, "PRAGMA quick_check reported: "+qc)
			}
		}

		detected, missing, ver, err := IntrospectSchema(posConn)
		rep.DetectedTables = detected
		rep.MissingCoreTables = missing
		rep.AroniumSchemaVersion = ver
		if err != nil || len(missing) > 0 {
			rep.Healthy = false
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("Missing core Aronium tables: %v", missing))
		}
		if prods, pErr := ListProductsWithStock(posConn); pErr != nil {
			rep.Healthy = false
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("Error leyendo catálogo de productos desde pos.db: %v", pErr))
		} else {
			rep.PosDBProductCount = len(prods)
		}
	}

	stateConn, err := OpenStateDB(stateDBPath)
	if err != nil {
		rep.Healthy = false
		rep.StateDBReady = false
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("Cannot initialize state DB: %v", err))
	} else {
		defer stateConn.Close()
		rep.StateDBReady = true
		_ = stateConn.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_outbox WHERE status IN ('PENDING', 'FAILED');`).Scan(&rep.PendingOutboxCount)

		// Check register identity conflict (TC-029)
		hostname, _ := os.Hostname()
		var existingHost string
		err := stateConn.QueryRowContext(ctx, `SELECT hostname FROM register_metadata WHERE register_id = ?;`, registerID).Scan(&existingHost)
		if err == nil && existingHost != "" && !strings.EqualFold(existingHost, hostname) {
			rep.RegisterIDConflict = true
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("Cash register ID %s is already claimed by hostname %s (current host: %s)", registerID, existingHost, hostname))
		}
	}

	if tokenVaultPath != "" {
		if info, err := os.Stat(tokenVaultPath); err == nil && info.Size() > 0 {
			rep.GDriveConfigured = true
			rep.GDriveTokenValid = true
		}
	}

	return rep, nil
}
