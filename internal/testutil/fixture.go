package testutil

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// CreateSyntheticPosDB creates a realistic Aronium Lite pos.db SQLite database at destPath (TODO-057).
func CreateSyntheticPosDB(destPath string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}
	_ = os.Remove(destPath)

	conn, err := OpenWritablePosDB(destPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	schemaSQL := `
	CREATE TABLE ApplicationProperty (
		Name TEXT PRIMARY KEY,
		Value TEXT
	);
	INSERT INTO ApplicationProperty (Name, Value) VALUES ('DatabaseVersion', '1.42.0');

	CREATE TABLE User (
		Id INTEGER PRIMARY KEY AUTOINCREMENT,
		FirstName TEXT,
		LastName TEXT,
		Email TEXT,
		AccessLevel INTEGER DEFAULT 10,
		IsEnabled INTEGER DEFAULT 1
	);
	INSERT INTO User (Id, FirstName, LastName, Email) VALUES (1, 'Cajero', 'Principal', 'cajero@comercio.local');

	CREATE TABLE Warehouse (
		Id INTEGER PRIMARY KEY AUTOINCREMENT,
		Name TEXT NOT NULL
	);
	INSERT INTO Warehouse (Id, Name) VALUES (1, 'Almacén Principal');

	CREATE TABLE CashRegister (
		Id INTEGER PRIMARY KEY AUTOINCREMENT,
		Name TEXT NOT NULL
	);
	INSERT INTO CashRegister (Id, Name) VALUES (1, 'POS-01'), (2, 'POS-02');

	CREATE TABLE Tax (
		Id INTEGER PRIMARY KEY AUTOINCREMENT,
		Name TEXT NOT NULL,
		Rate REAL NOT NULL DEFAULT 19.0,
		Code TEXT
	);
	INSERT INTO Tax (Id, Name, Rate, Code) VALUES (1, 'Impuesto General 19%', 19.0, '01'), (2, 'Exento 0%', 0.0, '00');

	CREATE TABLE ProductGroup (
		Id INTEGER PRIMARY KEY AUTOINCREMENT,
		Name TEXT NOT NULL,
		ParentGroupId INTEGER
	);
	INSERT INTO ProductGroup (Id, Name) VALUES
		(1, 'Alimentos'),
		(2, 'Lácteos'),
		(3, 'Bebidas'),
		(4, 'Cocina');

	CREATE TABLE Product (
		Id INTEGER PRIMARY KEY AUTOINCREMENT,
		ProductGroupId INTEGER,
		Name TEXT NOT NULL,
		Code TEXT,
		PLU INTEGER DEFAULT 0,
		MeasurementUnit TEXT DEFAULT 'UND',
		Price REAL NOT NULL DEFAULT 0,
		IsTaxInclusive INTEGER DEFAULT 1,
		CurrencyId INTEGER DEFAULT 1,
		IsPriceChangeAllowed INTEGER DEFAULT 0,
		IsService INTEGER DEFAULT 0,
		IsUsingDefaultQuantity INTEGER DEFAULT 1,
		IsEnabled INTEGER DEFAULT 1,
		Description TEXT,
		DateCreated DATETIME DEFAULT CURRENT_TIMESTAMP,
		DateUpdated DATETIME DEFAULT CURRENT_TIMESTAMP,
		Cost REAL NOT NULL DEFAULT 0,
		Markup REAL NOT NULL DEFAULT 0
	);

	CREATE TABLE Barcode (
		Id INTEGER PRIMARY KEY AUTOINCREMENT,
		ProductId INTEGER NOT NULL,
		Value TEXT NOT NULL
	);

	CREATE TABLE Stock (
		Id INTEGER PRIMARY KEY AUTOINCREMENT,
		ProductId INTEGER NOT NULL,
		WarehouseId INTEGER NOT NULL DEFAULT 1,
		Quantity REAL NOT NULL DEFAULT 0
	);

	CREATE TABLE Customer (
		Id INTEGER PRIMARY KEY AUTOINCREMENT,
		Code TEXT,
		Name TEXT NOT NULL,
		TaxNumber TEXT,
		Address TEXT,
		PostalCode TEXT,
		City TEXT,
		CountryId INTEGER,
		Email TEXT,
		PhoneNumber TEXT,
		IsEnabled INTEGER DEFAULT 1,
		IsCustomer INTEGER DEFAULT 1,
		IsSupplier INTEGER DEFAULT 0,
		DueDatePeriod INTEGER DEFAULT 15
	);

	CREATE TABLE PaymentType (
		Id INTEGER PRIMARY KEY AUTOINCREMENT,
		Name TEXT NOT NULL,
		Code TEXT,
		IsCustomerRequired INTEGER DEFAULT 0,
		IsFiscal INTEGER DEFAULT 1,
		IsSlipRequired INTEGER DEFAULT 0,
		IsChangeAllowed INTEGER DEFAULT 1,
		Ordinal INTEGER DEFAULT 1,
		IsEnabled INTEGER DEFAULT 1,
		IsQuickPayment INTEGER DEFAULT 1,
		OpenCashDrawer INTEGER DEFAULT 1,
		MarkAsPaid INTEGER DEFAULT 1
	);

	CREATE TABLE DocumentType (
		Id INTEGER PRIMARY KEY AUTOINCREMENT,
		Name TEXT NOT NULL,
		Code TEXT,
		DocumentCategoryId INTEGER DEFAULT 2,
		WarehouseId INTEGER DEFAULT 1,
		StockDirection INTEGER DEFAULT 2
	);
	INSERT INTO DocumentType (Id, Name, Code, StockDirection) VALUES
		(1, 'Compra / Entrada', '100', 1),
		(2, 'Venta POS', '200', 2),
		(3, 'Devolución', '300', 1);

	CREATE TABLE Document (
		Id INTEGER PRIMARY KEY AUTOINCREMENT,
		Number TEXT NOT NULL,
		UserId INTEGER DEFAULT 1,
		CustomerId INTEGER,
		OrderNumber TEXT,
		Date DATETIME NOT NULL,
		StockDate DATETIME NOT NULL,
		Total REAL NOT NULL DEFAULT 0,
		IsClockedOut INTEGER DEFAULT 0,
		DocumentTypeId INTEGER NOT NULL DEFAULT 2,
		WarehouseId INTEGER NOT NULL DEFAULT 1,
		ReferenceDocumentNumber TEXT,
		DateCreated DATETIME DEFAULT CURRENT_TIMESTAMP,
		DateUpdated DATETIME DEFAULT CURRENT_TIMESTAMP,
		InternalNote TEXT,
		Note TEXT,
		DueDate DATETIME,
		Discount REAL DEFAULT 0,
		DiscountType INTEGER DEFAULT 0,
		PaidStatus INTEGER DEFAULT 0
	);

	CREATE TABLE DocumentItem (
		Id INTEGER PRIMARY KEY AUTOINCREMENT,
		DocumentId INTEGER NOT NULL,
		ProductId INTEGER NOT NULL,
		Quantity REAL NOT NULL DEFAULT 1,
		ExpectedQuantity REAL DEFAULT 0,
		PriceBeforeTax REAL DEFAULT 0,
		Price REAL NOT NULL DEFAULT 0,
		Discount REAL DEFAULT 0,
		DiscountType INTEGER DEFAULT 0,
		ProductCost REAL DEFAULT 0,
		PriceBeforeDiscount REAL DEFAULT 0,
		Total REAL NOT NULL DEFAULT 0
	);

	CREATE TABLE Payment (
		Id INTEGER PRIMARY KEY AUTOINCREMENT,
		DocumentId INTEGER NOT NULL,
		PaymentTypeId INTEGER NOT NULL,
		Amount REAL NOT NULL DEFAULT 0,
		Date DATETIME DEFAULT CURRENT_TIMESTAMP,
		UserId INTEGER DEFAULT 1,
		DateCreated DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_barcode_product ON Barcode(ProductId);
	CREATE INDEX IF NOT EXISTS idx_stock_product ON Stock(ProductId);
	CREATE INDEX IF NOT EXISTS idx_docitem_doc ON DocumentItem(DocumentId);
	CREATE INDEX IF NOT EXISTS idx_payment_doc ON Payment(DocumentId);
	`
	if _, err := conn.Exec(schemaSQL); err != nil {
		return fmt.Errorf("create synthetic schema failed: %w", err)
	}

	seedDataSQL := `
	-- Payment Types
	INSERT INTO PaymentType (Id, Name, Code, IsCustomerRequired, MarkAsPaid) VALUES
		(1, 'Efectivo', 'CASH', 0, 1),
		(2, 'Transferencia / Tarjeta', 'TRANSFER', 0, 1),
		(3, 'Crédito Cliente', 'CREDIT', 1, 0);

	-- Customers
	INSERT INTO Customer (Id, Code, Name, TaxNumber, Address, City, Email, PhoneNumber, DueDatePeriod) VALUES
		(1, 'CF-001', 'Consumidor Final', '999999999999', 'Local', 'Centro', 'ventas@comercio.local', '5550000000', 0),
		(2, 'CLI-101', 'Pedro Pérez', '79123456', 'Av. Central 45-12', 'Centro', 'pedro.perez@correo.org', '5551234567', 15),
		(3, 'CLI-102', 'María Gómez', '52987654', 'Calle 15 #80-10', 'Norte', 'maria.gomez@correo.org', '5559876543', 15),
		(4, 'CLI-103', 'Comercial El Sol S.A.', '901234567-8', 'Av. Industrial 10-20', 'Sur', 'contabilidad@elsol.org', '5558887766', 30);

	-- Products
	INSERT INTO Product (Id, ProductGroupId, Name, Code, MeasurementUnit, Price, Cost, Markup) VALUES
		(1, 1, 'Arroz Grano Largo 1kg', 'ARR-001', 'UND', 4500, 3400, 32.35),
		(2, 1, 'Aceite Vegetal 1L', 'ACE-002', 'UND', 11000, 8500, 29.41),
		(3, 2, 'Leche Entera 1L', 'LEC-003', 'UND', 4200, 3300, 27.27),
		(4, 3, 'Café Molido 500g', 'CAF-004', 'UND', 16500, 12800, 28.90),
		(5, 4, 'Menú Ejecutivo Especial', 'COC-005', 'UND', 28000, 15000, 86.67);

	INSERT INTO Barcode (ProductId, Value) VALUES
		(1, '7701234500011'),
		(2, '7701234500028'),
		(3, '7701234500035'),
		(4, '7701234500042'),
		(5, '7701234500059');

	INSERT INTO Stock (ProductId, WarehouseId, Quantity) VALUES
		(1, 1, 45),
		(2, 1, 18),
		(3, 1, 3),  -- Low stock alert (<= 5)
		(4, 1, 0),  -- Out of stock alert
		(5, 1, 12);

	-- Document 1: Paid cash sale (Consumidor Final)
	INSERT INTO Document (Id, Number, CustomerId, Date, StockDate, Total, DocumentTypeId, PaidStatus, Note, InternalNote)
	VALUES (1, '20-200-000001', 1, '2026-11-10 09:15:00', '2026-11-10 09:15:00', 9000, 2, 1, 'Venta mostrador', 'POS-01');
	INSERT INTO DocumentItem (DocumentId, ProductId, Quantity, Price, ProductCost, Total)
	VALUES (1, 1, 2, 4500, 3400, 9000);
	INSERT INTO Payment (DocumentId, PaymentTypeId, Amount, Date)
	VALUES (1, 1, 9000, '2026-11-10 09:15:00');

	-- Document 2: Full Credit sale for Pedro Pérez ($20000 unpaid)
	INSERT INTO Document (Id, Number, CustomerId, Date, StockDate, Total, DocumentTypeId, PaidStatus, Note, InternalNote, DueDate)
	VALUES (2, '20-200-000002', 2, '2026-11-01 14:30:00', '2026-11-01 14:30:00', 20000, 2, 0, 'Compra a crédito para pago fin de semana', 'POS-01', '2026-11-10');
	INSERT INTO DocumentItem (DocumentId, ProductId, Quantity, Price, ProductCost, Total) VALUES
		(2, 1, 2, 4500, 3400, 9000),
		(2, 2, 1, 11000, 8500, 11000);
	INSERT INTO Payment (DocumentId, PaymentTypeId, Amount, Date)
	VALUES (2, 3, 20000, '2026-11-01 14:30:00');

	-- Document 3: Split payment sale for María Gómez (Total $50000 -> Paid $20000 Efectivo + $30000 Crédito Cliente)
	INSERT INTO Document (Id, Number, CustomerId, Date, StockDate, Total, DocumentTypeId, PaidStatus, Note, InternalNote, DueDate)
	VALUES (3, '20-200-000003', 3, '2026-11-12 17:45:00', '2026-11-12 17:45:00', 50000, 2, 0, 'Pago parcial en efectivo y saldo a cuenta de crédito', 'POS-02', '2026-11-27');
	INSERT INTO DocumentItem (DocumentId, ProductId, Quantity, Price, ProductCost, Total) VALUES
		(3, 4, 2, 16500, 12800, 33000),
		(3, 3, 2, 4200, 3300, 8400),
		(3, 1, 1, 8600, 3400, 8600);
	INSERT INTO Payment (DocumentId, PaymentTypeId, Amount, Date) VALUES
		(3, 1, 20000, '2026-11-12 17:45:00'),
		(3, 3, 30000, '2026-11-12 17:45:00');
	`
	_, err = conn.Exec(seedDataSQL)
	return err
}

// OpenWritablePosDB opens a synthetic pos.db in read/write mode to simulate Aronium Lite writing transactions in tests.
func OpenWritablePosDB(path string) (*sql.DB, error) {
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
	u.RawQuery = q.Encode()
	return sql.Open("sqlite", u.String())
}

// SimulateNewCreditSale inserts a new customer credit sale into a synthetic pos.db.
func SimulateNewCreditSale(posPath, docNumber string, customerID int64, productID int64, qty, unitPrice float64, note, registerID string) (int64, error) {
	conn, err := OpenWritablePosDB(posPath)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	total := qty * unitPrice
	res, err := conn.Exec(`
		INSERT INTO Document (Number, CustomerId, Date, StockDate, Total, DocumentTypeId, PaidStatus, Note, InternalNote)
		VALUES (?, ?, '2026-11-18 18:00:00', '2026-11-18 18:00:00', ?, 2, 0, ?, ?);
	`, docNumber, customerID, total, note, registerID)
	if err != nil {
		return 0, err
	}
	docID, _ := res.LastInsertId()

	_, err = conn.Exec(`
		INSERT INTO DocumentItem (DocumentId, ProductId, Quantity, Price, Total)
		VALUES (?, ?, ?, ?, ?);
	`, docID, productID, qty, unitPrice, total)
	if err != nil {
		return docID, err
	}

	_, err = conn.Exec(`
		INSERT INTO Payment (DocumentId, PaymentTypeId, Amount, Date)
		VALUES (?, 3, ?, '2026-11-18 18:00:00');
	`, docID, total)
	if err != nil {
		return docID, err
	}

	_, _ = conn.Exec(`UPDATE Stock SET Quantity = Quantity - ? WHERE ProductId = ?;`, qty, productID)
	return docID, nil
}

// SimulateCustomerPayment inserts a partial or full payment against an existing document in a synthetic pos.db.
func SimulateCustomerPayment(posPath string, docID int64, paymentAmount float64, markDocumentFullyPaid bool) error {
	conn, err := OpenWritablePosDB(posPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = conn.Exec(`
		INSERT INTO Payment (DocumentId, PaymentTypeId, Amount, Date)
		VALUES (?, 1, ?, '2026-11-18 19:30:00');
	`, docID, paymentAmount)
	if err != nil {
		return err
	}
	if markDocumentFullyPaid {
		_, err = conn.Exec(`UPDATE Document SET PaidStatus = 1 WHERE Id = ?;`, docID)
	}
	return err
}
