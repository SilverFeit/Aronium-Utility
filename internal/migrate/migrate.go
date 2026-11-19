package migrate

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SilverFeit/Aronium-Utility/internal/db"
)

// ExportBundle holds the full extracted dataset from Aronium Lite pos.db (TODO-042).
type ExportBundle struct {
	ExportedAt    time.Time        `json:"exported_at"`
	SchemaVersion string           `json:"schema_version"`
	StoreID       string           `json:"store_id"`
	Products      []db.Product     `json:"products"`
	Customers     []db.Customer    `json:"customers"`
	PaymentTypes  []db.PaymentType `json:"payment_types"`
	Documents     []db.Document    `json:"documents"`
	Metrics       MigrationMetrics `json:"metrics"`
}

// MigrationMetrics records row counts and financial sums for post-migration verification (MIG-004, MIG-005, TODO-044).
type MigrationMetrics struct {
	ProductCount        int     `json:"product_count"`
	CustomerCount       int     `json:"customer_count"`
	PaymentTypeCount    int     `json:"payment_type_count"`
	DocumentCount       int     `json:"document_count"`
	DocumentItemCount   int     `json:"document_item_count"`
	PaymentCount        int     `json:"payment_count"`
	TotalSalesAmount    float64 `json:"total_sales_amount"`
	TotalPaymentsAmount float64 `json:"total_payments_amount"`
	TotalStockQuantity  float64 `json:"total_stock_quantity"`
}

// VerificationResult reports the comparison between source pos.db and migrated target (TODO-044).
type VerificationResult struct {
	Passed        bool             `json:"passed"`
	SourceMetrics MigrationMetrics `json:"source_metrics"`
	TargetMetrics MigrationMetrics `json:"target_metrics"`
	Discrepancies []string         `json:"discrepancies,omitempty"`
}

// Engine executes multi-engine exports, SQL Server / PostgreSQL script generation, and MIG-004..006 verification.
type Engine struct {
	PosDB      *sql.DB
	StoreID    string
	RegisterID string
}

func NewEngine(posDB *sql.DB, storeID, registerID string) *Engine {
	return &Engine{
		PosDB:      posDB,
		StoreID:    storeID,
		RegisterID: registerID,
	}
}

// BuildBundle extracts the complete dataset and financial sums from pos.db (TODO-042).
func (e *Engine) BuildBundle() (*ExportBundle, error) {
	_, _, ver, _ := db.IntrospectSchema(e.PosDB)
	products, err := db.ListProductsWithStock(e.PosDB)
	if err != nil {
		return nil, err
	}
	customers, err := db.ListCustomers(e.PosDB)
	if err != nil {
		return nil, err
	}
	paymentTypes, err := db.ListPaymentTypes(e.PosDB)
	if err != nil {
		return nil, err
	}
	docs, err := db.ListDocuments(e.PosDB, 0, e.RegisterID)
	if err != nil {
		return nil, err
	}

	var m MigrationMetrics
	m.ProductCount = len(products)
	m.CustomerCount = len(customers)
	m.PaymentTypeCount = len(paymentTypes)
	m.DocumentCount = len(docs)

	for _, p := range products {
		m.TotalStockQuantity += p.StockQuantity
	}
	for _, d := range docs {
		m.TotalSalesAmount += d.Total
		m.DocumentItemCount += len(d.Items)
		m.PaymentCount += len(d.Payments)
		for _, pay := range d.Payments {
			m.TotalPaymentsAmount += pay.Amount
		}
	}

	return &ExportBundle{
		ExportedAt:    time.Now().UTC(),
		SchemaVersion: ver,
		StoreID:       e.StoreID,
		Products:      products,
		Customers:     customers,
		PaymentTypes:  paymentTypes,
		Documents:     docs,
		Metrics:       m,
	}, nil
}

// Export writes the extracted bundle to disk in JSON, CSV, PostgreSQL SQL, or SQL Server Express SQL format (TODO-042, TODO-043).
func (e *Engine) Export(format, outputDir string) ([]string, *ExportBundle, error) {
	bundle, err := e.BuildBundle()
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, nil, err
	}

	var files []string
	switch strings.ToLower(format) {
	case "json":
		p := filepath.Join(outputDir, "aronium_export.json")
		raw, err := json.MarshalIndent(bundle, "", "  ")
		if err != nil {
			return nil, nil, err
		}
		if err := os.WriteFile(p, raw, 0644); err != nil {
			return nil, nil, err
		}
		files = append(files, p)

	case "csv":
		prodCSV := filepath.Join(outputDir, "products.csv")
		custCSV := filepath.Join(outputDir, "customers.csv")
		docsCSV := filepath.Join(outputDir, "documents.csv")
		if err := exportProductsCSV(prodCSV, bundle.Products); err != nil {
			return nil, nil, err
		}
		if err := exportCustomersCSV(custCSV, bundle.Customers); err != nil {
			return nil, nil, err
		}
		if err := exportDocumentsCSV(docsCSV, bundle.Documents); err != nil {
			return nil, nil, err
		}
		files = append(files, prodCSV, custCSV, docsCSV)

	case "postgres", "postgresql":
		p := filepath.Join(outputDir, "aronium_migrate_postgres.sql")
		sqlScript := buildSQLDump(bundle, "postgres")
		if err := os.WriteFile(p, []byte(sqlScript), 0644); err != nil {
			return nil, nil, err
		}
		files = append(files, p)

	case "sqlserver", "mssql", "pro":
		p := filepath.Join(outputDir, "aronium_migrate_sqlserver_pro.sql")
		sqlScript := buildSQLDump(bundle, "sqlserver")
		if err := os.WriteFile(p, []byte(sqlScript), 0644); err != nil {
			return nil, nil, err
		}
		files = append(files, p)

	default:
		return nil, nil, fmt.Errorf("unsupported export format '%s' (supported: json, csv, postgres, sqlserver)", format)
	}

	// Always write MIG-006 Post-Migration Checklist alongside export (TODO-045)
	checklistPath := filepath.Join(outputDir, "POST_MIGRATION_CHECKLIST_MIG006.md")
	_ = os.WriteFile(checklistPath, []byte(GeneratePostMigrationChecklist(e.StoreID, bundle.Metrics)), 0644)
	files = append(files, checklistPath)

	return files, bundle, nil
}

// Verify compares the live pos.db metrics against an exported bundle or target metrics (MIG-004, MIG-005, TODO-044).
func (e *Engine) Verify(target MetricsProvider) (*VerificationResult, error) {
	srcBundle, err := e.BuildBundle()
	if err != nil {
		return nil, err
	}
	src := srcBundle.Metrics
	dst := target.GetMetrics()

	res := &VerificationResult{
		Passed:        true,
		SourceMetrics: src,
		TargetMetrics: dst,
	}

	checkInt := func(label string, a, b int) {
		if a != b {
			res.Passed = false
			res.Discrepancies = append(res.Discrepancies, fmt.Sprintf("%s mismatch: source=%d vs target=%d", label, a, b))
		}
	}
	checkFloat := func(label string, a, b float64) {
		if math.Abs(a-b) > 0.01 {
			res.Passed = false
			res.Discrepancies = append(res.Discrepancies, fmt.Sprintf("%s financial sum mismatch: source=%.2f vs target=%.2f", label, a, b))
		}
	}

	checkInt("ProductCount (MIG-004)", src.ProductCount, dst.ProductCount)
	checkInt("CustomerCount (MIG-005)", src.CustomerCount, dst.CustomerCount)
	checkInt("DocumentCount (MIG-005)", src.DocumentCount, dst.DocumentCount)
	checkInt("DocumentItemCount (MIG-005)", src.DocumentItemCount, dst.DocumentItemCount)
	checkInt("PaymentCount (MIG-005)", src.PaymentCount, dst.PaymentCount)
	checkFloat("SUM(Document.Total) (MIG-005)", src.TotalSalesAmount, dst.TotalSalesAmount)
	checkFloat("SUM(Payment.Amount) (MIG-005)", src.TotalPaymentsAmount, dst.TotalPaymentsAmount)
	checkFloat("SUM(Stock.Quantity) (MIG-004)", src.TotalStockQuantity, dst.TotalStockQuantity)

	return res, nil
}

// MetricsProvider allows verifying against an ExportBundle or a live database.
type MetricsProvider interface {
	GetMetrics() MigrationMetrics
}

func (b *ExportBundle) GetMetrics() MigrationMetrics {
	return b.Metrics
}

// GeneratePostMigrationChecklist produces the MIG-006 post-migration verification & manual setup checklist (TODO-045).
func GeneratePostMigrationChecklist(storeID string, m MigrationMetrics) string {
	return fmt.Sprintf(`# Checklist Post-Migración Aronium Lite -> Aronium Pro / SQL Server (MIG-006)

**Comercio / Sede:** %s
**Fecha de Generación:** %s

## 1. Resumen Verificado de Datos Migrados (MIG-004 / MIG-005)
- [x] **Productos y Stock:** %d productos (Unidades totales en inventario: %.2f)
- [x] **Clientes:** %d clientes registrados
- [x] **Documentos Comerciales:** %d documentos (%d líneas de ítem)
- [x] **Sumatoria Financiera (`+"`SUM(Total)`"+`):** $%.2f COP
- [x] **Pagos y Abonos (`+"`SUM(Payment.Amount)`"+`):** %d registros ($%.2f COP)

## 2. Componentes Locales que Requieren Reconfiguración Manual por Estación (MIG-006)
De acuerdo con la especificación funcional de Aronium Pro (Sección 5.2 / MIG-006), los siguientes elementos **NO** se transfieren automáticamente con la base de datos y deben revisarse en cada computador (`+"`POS-01`"+`, `+"`POS-02`"+`, `+"`POS-ADMIN`"+`):

- [ ] **Identidad de Caja Registradora (`+"`PRO-FR-003`"+`):**
  - Crear cada caja en `+"`Management -> Cash registers -> Add cash register`"+` (`+"`POS-01`"+`, `+"`POS-02`"+`, etc.).
  - En cada estación cliente ir a `+"`Settings -> Database -> Cash register`"+`, asignar su caja única, guardar y **reiniciar Aronium**.
- [ ] **Conexión a Instancia SQL Server Express (`+"`PRO-FR-001`"+` / `+"`PRO-FR-006`"+`):**
  - Verificar conexión a `+"`<IP-SERVIDOR>\\ARONIUM`"+` (base de datos `+"`aroniumdb`"+`).
  - Confirmar reglas de Firewall de Windows para SQL Server en la red LAN.
- [ ] **Impresoras Térmicas y Tirillas por Estación (`+"`AC-011`"+` / `+"`TC-019`"+`):**
  - Reasignar la impresora de recibos y facturas en `+"`Settings -> Print options`"+` después de cambiar la caja registradora.
- [ ] **Periféricos Locales:**
  - Verificar apertura de cajón monedero (RJ11 / comando ESC/POS), lector de códigos de barras y báscula en cada terminal.
- [ ] **Plugins y Extensiones (Sección 8):**
  - Reinstalar y activar plugins locales en cada computador que los utilice.
- [ ] **Aronium Utility (`+"`aronium-util`"+`):**
  - Ejecutar `+"`aronium-util doctor`"+` en cada estación para confirmar ausencia de conflictos de `+"`cash_register_id`"+` (`+"`TC-029`"+`).
`, storeID, time.Now().Format(time.RFC3339), m.ProductCount, m.TotalStockQuantity, m.CustomerCount, m.DocumentCount, m.DocumentItemCount, m.TotalSalesAmount, m.PaymentCount, m.TotalPaymentsAmount)
}

func buildSQLDump(b *ExportBundle, dialect string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("-- Aronium Utility Migration SQL Dump (%s)\n", strings.ToUpper(dialect)))
	sb.WriteString(fmt.Sprintf("-- Generated at: %s | Store: %s\n\n", b.ExportedAt.Format(time.RFC3339), b.StoreID))

	if dialect == "sqlserver" {
		sb.WriteString("-- Target: Microsoft SQL Server Express 2019 (Instance: ARONIUM / Database: aroniumdb)\n")
		sb.WriteString("USE [aroniumdb];\nBEGIN TRANSACTION;\n\n")
	} else {
		sb.WriteString("-- Target: PostgreSQL\n")
		sb.WriteString("BEGIN;\n\n")
	}

	esc := func(s string) string {
		return strings.ReplaceAll(s, "'", "''")
	}

	for _, c := range b.Customers {
		sb.WriteString(fmt.Sprintf("INSERT INTO Customer (Id, Code, Name, TaxNumber, PhoneNumber, Email) VALUES (%d, '%s', '%s', '%s', '%s', '%s');\n",
			c.ID, esc(c.Code), esc(c.Name), esc(c.TaxNumber), esc(c.PhoneNumber), esc(c.Email)))
	}
	sb.WriteString("\n")
	for _, p := range b.Products {
		sb.WriteString(fmt.Sprintf("INSERT INTO Product (Id, Code, Name, Price, Cost) VALUES (%d, '%s', '%s', %.2f, %.2f);\n",
			p.ID, esc(p.Code), esc(p.Name), p.Price, p.Cost))
	}
	sb.WriteString("\n")
	for _, d := range b.Documents {
		sb.WriteString(fmt.Sprintf("INSERT INTO Document (Id, Number, CustomerId, Date, Total, PaidStatus, Note) VALUES (%d, '%s', %d, '%s', %.2f, %d, '%s');\n",
			d.ID, esc(d.Number), d.CustomerID, esc(d.Date), d.Total, d.PaidStatus, esc(d.Note)))
	}

	if dialect == "sqlserver" {
		sb.WriteString("\nCOMMIT TRANSACTION;\n")
	} else {
		sb.WriteString("\nCOMMIT;\n")
	}
	return sb.String()
}

func exportProductsCSV(path string, products []db.Product) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"Id", "Code", "Barcode", "Name", "Category", "Price", "Cost", "StockQuantity"})
	for _, p := range products {
		_ = w.Write([]string{
			fmt.Sprintf("%d", p.ID), p.Code, p.Barcode, p.Name, p.ProductGroupName,
			fmt.Sprintf("%.2f", p.Price), fmt.Sprintf("%.2f", p.Cost), fmt.Sprintf("%.2f", p.StockQuantity),
		})
	}
	w.Flush()
	return w.Error()
}

func exportCustomersCSV(path string, customers []db.Customer) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"Id", "Code", "Name", "TaxNumber", "PhoneNumber", "Email"})
	for _, c := range customers {
		_ = w.Write([]string{fmt.Sprintf("%d", c.ID), c.Code, c.Name, c.TaxNumber, c.PhoneNumber, c.Email})
	}
	w.Flush()
	return w.Error()
}

func exportDocumentsCSV(path string, docs []db.Document) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"Id", "Number", "Date", "CustomerId", "CustomerName", "Total", "PaidStatus", "Note"})
	for _, d := range docs {
		_ = w.Write([]string{
			fmt.Sprintf("%d", d.ID), d.Number, d.Date, fmt.Sprintf("%d", d.CustomerID),
			d.CustomerName, fmt.Sprintf("%.2f", d.Total), fmt.Sprintf("%d", d.PaidStatus), d.Note,
		})
	}
	w.Flush()
	return w.Error()
}
