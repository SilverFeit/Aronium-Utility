package sync

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/SilverFeit/Aronium-Utility/internal/credit"
	"github.com/SilverFeit/Aronium-Utility/internal/inventory"
)

// SheetData represents a worksheet tab in an OpenXML .xlsx workbook.
type SheetData struct {
	Name string
	Rows [][]string
}

var invalidSheetChars = regexp.MustCompile(`[\[\]\*\?\/\\:]`)

func sanitizeSheetName(name string, used map[string]bool) string {
	clean := invalidSheetChars.ReplaceAllString(strings.TrimSpace(name), "_")
	if clean == "" {
		clean = "Hoja"
	}
	runes := []rune(clean)
	if len(runes) > 28 {
		clean = string(runes[:28])
	}
	candidate := clean
	idx := 2
	for used[strings.ToLower(candidate)] {
		suffix := fmt.Sprintf("_%d", idx)
		maxBase := 31 - len(suffix)
		baseRunes := []rune(clean)
		if len(baseRunes) > maxBase {
			baseRunes = baseRunes[:maxBase]
		}
		candidate = string(baseRunes) + suffix
		idx++
	}
	used[strings.ToLower(candidate)] = true
	return candidate
}

// BuildXLSXBytes generates a standards-compliant multi-sheet OpenXML (.xlsx) workbook in memory.
func BuildXLSXBytes(sheets []SheetData) ([]byte, error) {
	if len(sheets) == 0 {
		sheets = []SheetData{{Name: "Datos", Rows: [][]string{{"Sin datos"}}}}
	}
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	usedNames := make(map[string]bool)
	sanitized := make([]SheetData, len(sheets))
	for i, s := range sheets {
		sanitized[i] = SheetData{
			Name: sanitizeSheetName(s.Name, usedNames),
			Rows: s.Rows,
		}
	}

	// 1. [Content_Types].xml
	var ctOverrides strings.Builder
	for i := range sanitized {
		fmt.Fprintf(&ctOverrides, `<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, i+1)
	}
	contentTypes := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
		`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>` +
		ctOverrides.String() +
		`</Types>`
	if err := writeZipEntry(zw, "[Content_Types].xml", []byte(contentTypes)); err != nil {
		return nil, err
	}

	// 2. _rels/.rels
	rootRels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
		`</Relationships>`
	if err := writeZipEntry(zw, "_rels/.rels", []byte(rootRels)); err != nil {
		return nil, err
	}

	// 3. xl/workbook.xml & xl/_rels/workbook.xml.rels
	var wbSheets strings.Builder
	var wbRels strings.Builder
	for i, s := range sanitized {
		escapedName := escapeXML(s.Name)
		fmt.Fprintf(&wbSheets, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, escapedName, i+1, i+1)
		fmt.Fprintf(&wbRels, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, i+1, i+1)
	}
	fmt.Fprintf(&wbRels, `<Relationship Id="rIdStyles" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`)

	workbookXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
		`<sheets>` + wbSheets.String() + `</sheets>` +
		`</workbook>`
	if err := writeZipEntry(zw, "xl/workbook.xml", []byte(workbookXML)); err != nil {
		return nil, err
	}

	workbookRelsXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		wbRels.String() +
		`</Relationships>`
	if err := writeZipEntry(zw, "xl/_rels/workbook.xml.rels", []byte(workbookRelsXML)); err != nil {
		return nil, err
	}

	// 4. xl/styles.xml
	stylesXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
		`<fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><name val="Calibri"/></font></fonts>` +
		`<fills count="1"><fill><patternFill patternType="none"/></fill></fills>` +
		`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>` +
		`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
		`<cellXfs count="2"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/><xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/></cellXfs>` +
		`</styleSheet>`
	if err := writeZipEntry(zw, "xl/styles.xml", []byte(stylesXML)); err != nil {
		return nil, err
	}

	// 5. xl/worksheets/sheet1..N.xml
	for i, s := range sanitized {
		var sheetBuf strings.Builder
		sheetBuf.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
		sheetBuf.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
		for rIdx, row := range s.Rows {
			fmt.Fprintf(&sheetBuf, `<row r="%d">`, rIdx+1)
			styleAttr := ""
			if rIdx == 0 {
				styleAttr = ` s="1"`
			}
			for cIdx, val := range row {
				colRef := excelColumnName(cIdx)
				cellRef := fmt.Sprintf("%s%d", colRef, rIdx+1)
				fmt.Fprintf(&sheetBuf, `<c r="%s" t="inlineStr"%s><is><t>%s</t></is></c>`, cellRef, styleAttr, escapeXML(val))
			}
			sheetBuf.WriteString(`</row>`)
		}
		sheetBuf.WriteString(`</sheetData></worksheet>`)
		if err := writeZipEntry(zw, fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1), []byte(sheetBuf.String())); err != nil {
			return nil, err
		}
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WriteAtomicFile writes data to a temporary file and atomically renames it to destPath (TODO-026 Transporte A).
func WriteAtomicFile(destPath string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}
	tmpPath := destPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, destPath)
}

// BuildMasterWorkbookSheets creates the unified Inventory + Resumen_Cuentas_Credito + Detalle_Cuentas_Credito + per-customer sheets (TODO-026, TODO-027).
func BuildMasterWorkbookSheets(inv *inventory.InventoryReport, ledger *credit.LedgerReport, includeCustomerTabs bool) []SheetData {
	var sheets []SheetData

	// Sheet 1: Resumen_Cuentas_Credito
	resumenRows := [][]string{
		{
			"ID_Cliente", "Cliente", "ID_Fiscal", "Telefono", "Credito_Total_Historico",
			"Total_Pagado", "Saldo_Pendiente", "Limite_Credito", "Credito_Disponible",
			"Dias_Vencido", "Ultima_Compra", "Ultimo_Pago", "Estado_Cuenta", "Anotaciones_Cliente",
		},
	}
	if ledger != nil {
		for _, acc := range ledger.Accounts {
			resumenRows = append(resumenRows, []string{
				fmt.Sprintf("%d", acc.CustomerID),
				acc.CustomerName,
				acc.TaxNumber,
				acc.PhoneNumber,
				fmt.Sprintf("%.2f", acc.TotalCreditIssued),
				fmt.Sprintf("%.2f", acc.TotalPaymentsPaid),
				fmt.Sprintf("%.2f", acc.PendingBalance),
				fmt.Sprintf("%.2f", acc.CreditLimit),
				fmt.Sprintf("%.2f", acc.AvailableCredit),
				fmt.Sprintf("%d", acc.MaxOverdueDays),
				acc.LastPurchaseDate,
				acc.LastPaymentDate,
				acc.AccountStatus,
				strings.Join(acc.AccountNotes, " | "),
			})
		}
	}
	sheets = append(sheets, SheetData{Name: "Resumen_Cuentas_Credito", Rows: resumenRows})

	// Sheet 2: Detalle_Cuentas_Credito
	detalleRows := [][]string{
		{
			"Fecha", "Factura", "Caja", "ID_Cliente", "Cliente", "Telefono",
			"Productos", "Total_Factura", "Pagado", "Saldo_Pendiente",
			"Medios_Pago", "Anotaciones_Aronium_y_CLI", "Fecha_Vencimiento", "Dias_Vencido", "Estado",
		},
	}
	if ledger != nil {
		for _, tx := range ledger.AllTransactions {
			detalleRows = append(detalleRows, []string{
				tx.Date,
				tx.DocumentNumber,
				tx.CashRegisterID,
				fmt.Sprintf("%d", tx.CustomerID),
				tx.CustomerName,
				tx.CustomerPhone,
				tx.ItemsSummary,
				fmt.Sprintf("%.2f", tx.TotalDocument),
				fmt.Sprintf("%.2f", tx.PaidAmount),
				fmt.Sprintf("%.2f", tx.PendingBalance),
				tx.PaymentBreakdown,
				tx.CombinedNotes,
				tx.DueDate,
				fmt.Sprintf("%d", tx.OverdueDays),
				tx.Status,
			})
		}
	}
	sheets = append(sheets, SheetData{Name: "Detalle_Cuentas_Credito", Rows: detalleRows})

	// Sheet 3: Inventario
	invRows := [][]string{
		{
			"SKU_Codigo", "Codigo_Barras", "Producto", "Categoria", "Unidad",
			"Stock_Actual", "Stock_Minimo", "Costo_Unitario", "Precio_Venta",
			"Valor_Total_Costo", "Valor_Total_Venta", "Margen_Pct", "Estado_Stock", "Vencimiento",
		},
	}
	if inv != nil {
		for _, it := range inv.Items {
			invRows = append(invRows, []string{
				it.Code,
				it.Barcode,
				it.Name,
				it.Category,
				it.MeasurementUnit,
				fmt.Sprintf("%.2f", it.StockQuantity),
				fmt.Sprintf("%.2f", it.MinStock),
				fmt.Sprintf("%.2f", it.Cost),
				fmt.Sprintf("%.2f", it.SalePrice),
				fmt.Sprintf("%.2f", it.TotalCostValue),
				fmt.Sprintf("%.2f", it.TotalRetailValue),
				fmt.Sprintf("%.1f%%", it.EstimatedMargin),
				string(it.Status),
				it.ExpirationDate,
			})
		}
	}
	sheets = append(sheets, SheetData{Name: "Inventario", Rows: invRows})

	// Optional Sheet 4..N: Per-customer dedicated tabs
	if includeCustomerTabs && ledger != nil {
		for _, acc := range ledger.Accounts {
			custRows := BuildCustomerSheetRows(acc)
			sheets = append(sheets, SheetData{
				Name: "Cliente_" + acc.CustomerName,
				Rows: custRows,
			})
		}
	}

	return sheets
}

// BuildCustomerSheetRows formats an individual customer's account ledger sheet.
func BuildCustomerSheetRows(acc credit.CustomerCreditAccount) [][]string {
	rows := [][]string{
		{"ESTADO DE CUENTA DE CRÉDITO DE CLIENTE", acc.CustomerName},
		{"ID Cliente", fmt.Sprintf("%d", acc.CustomerID), "Teléfono", acc.PhoneNumber, "ID Fiscal", acc.TaxNumber},
		{"Total Crédito Histórico", fmt.Sprintf("%.2f", acc.TotalCreditIssued), "Total Pagado", fmt.Sprintf("%.2f", acc.TotalPaymentsPaid), "SALDO PENDIENTE", fmt.Sprintf("%.2f", acc.PendingBalance)},
		{"Límite de Crédito", fmt.Sprintf("%.2f", acc.CreditLimit), "Disponible", fmt.Sprintf("%.2f", acc.AvailableCredit), "Estado", acc.AccountStatus},
		{"Anotaciones Generales", strings.Join(acc.AccountNotes, " | ")},
		{},
		{"Fecha", "Factura #", "Caja", "Productos Comprados", "Total Factura", "Pagado", "Saldo Pendiente", "Anotaciones / Compromisos", "Vencimiento", "Estado"},
	}
	for _, tx := range acc.Transactions {
		rows = append(rows, []string{
			tx.Date,
			tx.DocumentNumber,
			tx.CashRegisterID,
			tx.ItemsSummary,
			fmt.Sprintf("%.2f", tx.TotalDocument),
			fmt.Sprintf("%.2f", tx.PaidAmount),
			fmt.Sprintf("%.2f", tx.PendingBalance),
			tx.CombinedNotes,
			tx.DueDate,
			tx.Status,
		})
	}
	return rows
}

func writeZipEntry(zw *zip.Writer, name string, content []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(content)
	return err
}

func escapeXML(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func excelColumnName(index int) string {
	name := ""
	for index >= 0 {
		name = string(rune('A'+(index%26))) + name
		index = index/26 - 1
	}
	return name
}
