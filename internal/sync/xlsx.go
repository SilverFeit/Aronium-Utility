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

	"github.com/SilverFeit/Aronium-Utility/internal/accounts"
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
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.relationships+xml"/>` +
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

// BuildMasterWorkbookSheets creates the 4-tab unified workbook:
// Cuentas, Movimientos, Detalle_Articulos, Inventario_Neto, plus optional per-customer tabs (TODO-106).
func BuildMasterWorkbookSheets(
	inv *inventory.InventoryReport,
	accLedger *accounts.AccountsLedgerReport,
	posCreditLedger *credit.LedgerReport,
	includeCustomerTabs bool,
) []SheetData {
	var sheets []SheetData

	// Tab 1: Cuentas
	cuentasRows := [][]string{
		{
			"ID_Cuenta", "Nombre", "Telefono", "Metodo_Pago", "Tope_Credito",
			"Total_Cargos", "Total_Abonos", "Total_Ajustes", "Saldo_Actual",
			"Credito_Disponible", "Estado", "Descripcion", "Actualizado_En",
		},
	}
	if accLedger != nil {
		for _, acc := range accLedger.Accounts {
			cuentasRows = append(cuentasRows, []string{
				acc.ID,
				acc.Name,
				acc.Phone,
				acc.PaymentMethod,
				fmt.Sprintf("%d", acc.CreditLimit),
				fmt.Sprintf("%d", acc.TotalCharges),
				fmt.Sprintf("%d", acc.TotalPayments),
				fmt.Sprintf("%d", acc.TotalAdjustments),
				fmt.Sprintf("%d", acc.CurrentBalance),
				fmt.Sprintf("%d", acc.AvailableCredit),
				string(acc.Status),
				acc.Description,
				acc.UpdatedAt,
			})
		}
	}
	sheets = append(sheets, SheetData{Name: "Cuentas", Rows: cuentasRows})

	// Tab 2: Movimientos
	movRows := [][]string{
		{
			"ID_Movimiento", "Fecha", "ID_Cuenta", "Cliente",
			"Tipo", "Monto", "Metodo_Pago_Usado", "Nota",
		},
	}
	if accLedger != nil {
		for _, acc := range accLedger.Accounts {
			for _, mv := range acc.Movements {
				movRows = append(movRows, []string{
					fmt.Sprintf("%d", mv.ID),
					mv.Date,
					acc.ID,
					acc.Name,
					string(mv.Type),
					fmt.Sprintf("%d", mv.Amount),
					mv.PaymentMethod,
					mv.Note,
				})
			}
		}
	}
	sheets = append(sheets, SheetData{Name: "Movimientos", Rows: movRows})

	// Tab 3: Detalle_Articulos (Frozen snapshots)
	detalleArtRows := [][]string{
		{
			"ID_Movimiento", "Fecha", "ID_Cuenta", "Cliente",
			"ID_Producto_Aronium", "Codigo_Barras", "Articulo_Snapshot",
			"Precio_Unitario_Snapshot", "Cantidad", "Subtotal",
		},
	}
	if accLedger != nil {
		for _, acc := range accLedger.Accounts {
			for _, mv := range acc.Movements {
				for _, it := range mv.Items {
					detalleArtRows = append(detalleArtRows, []string{
						fmt.Sprintf("%d", mv.ID),
						mv.Date,
						acc.ID,
						acc.Name,
						fmt.Sprintf("%d", it.AroniumProductID),
						it.Barcode,
						it.NameSnapshot,
						fmt.Sprintf("%d", it.UnitPriceSnapshot),
						fmt.Sprintf("%.2f", it.Quantity),
						fmt.Sprintf("%d", it.Subtotal),
					})
				}
			}
		}
	}
	sheets = append(sheets, SheetData{Name: "Detalle_Articulos", Rows: detalleArtRows})

	// Tab 4: Inventario_Neto (Stock_Aronium - Unidades_En_Cuentas)
	invRows := [][]string{
		{
			"SKU_Codigo", "Codigo_Barras", "Producto", "Categoria",
			"Precio_Venta_Actual", "Stock_Aronium", "Unidades_En_Cuentas",
			"Stock_Neto_Real", "Stock_Minimo", "Costo_Unitario", "Estado_Stock",
		},
	}
	if inv != nil {
		for _, it := range inv.Items {
			invRows = append(invRows, []string{
				it.Code,
				it.Barcode,
				it.Name,
				it.Category,
				fmt.Sprintf("%.2f", it.SalePrice),
				fmt.Sprintf("%.2f", it.AroniumStock),
				fmt.Sprintf("%.2f", it.CommittedInAccountsQty),
				fmt.Sprintf("%.2f", it.NetStockQuantity),
				fmt.Sprintf("%.2f", it.MinStock),
				fmt.Sprintf("%.2f", it.Cost),
				string(it.Status),
			})
		}
	}
	sheets = append(sheets, SheetData{Name: "Inventario_Neto", Rows: invRows})

	// Optional per-customer dedicated tabs from cuentas.db
	if includeCustomerTabs && accLedger != nil {
		for _, acc := range accLedger.Accounts {
			sheets = append(sheets, SheetData{
				Name: "Cliente_" + acc.Name,
				Rows: BuildAccountSheetRows(acc),
			})
		}
	}

	// Optional legacy POS credit ledger tabs if present
	if posCreditLedger != nil && len(posCreditLedger.Accounts) > 0 {
		posRows := [][]string{
			{"ID_Cliente", "Cliente", "Saldo_Pendiente_POS", "Estado"},
		}
		for _, cacc := range posCreditLedger.Accounts {
			posRows = append(posRows, []string{
				fmt.Sprintf("%d", cacc.CustomerID),
				cacc.CustomerName,
				fmt.Sprintf("%.2f", cacc.PendingBalance),
				cacc.AccountStatus,
			})
		}
		sheets = append(sheets, SheetData{Name: "Creditos_POS_Legacy", Rows: posRows})
	}

	return sheets
}

// BuildAccountSheetRows formats an individual customer's account ledger sheet from cuentas.db.
func BuildAccountSheetRows(acc accounts.AccountSummary) [][]string {
	rows := [][]string{
		{"ESTADO DE CUENTA DE CLIENTE", acc.Name},
		{"ID Cuenta", acc.ID, "Teléfono", acc.Phone, "Método de Pago", acc.PaymentMethod},
		{"Total Cargos", fmt.Sprintf("%d", acc.TotalCharges), "Total Abonos", fmt.Sprintf("%d", acc.TotalPayments), "SALDO ACTUAL", fmt.Sprintf("%d", acc.CurrentBalance)},
		{"Tope de Crédito", fmt.Sprintf("%d", acc.CreditLimit), "Disponible", fmt.Sprintf("%d", acc.AvailableCredit), "Estado", string(acc.Status)},
		{"Descripción", acc.Description},
		{},
		{"ID Mov", "Fecha", "Tipo", "Monto", "Método", "Nota / Artículos", "Total Ítems"},
	}
	for _, mv := range acc.Movements {
		var itemNames []string
		for _, it := range mv.Items {
			itemNames = append(itemNames, fmt.Sprintf("%.2gx %s ($%d)", it.Quantity, it.NameSnapshot, it.Subtotal))
		}
		noteText := mv.Note
		if len(itemNames) > 0 {
			noteText = fmt.Sprintf("%s | %s", mv.Note, strings.Join(itemNames, ", "))
		}
		rows = append(rows, []string{
			fmt.Sprintf("%d", mv.ID),
			mv.Date,
			string(mv.Type),
			fmt.Sprintf("%d", mv.Amount),
			mv.PaymentMethod,
			noteText,
			fmt.Sprintf("%d", len(mv.Items)),
		})
	}
	return rows
}

// BuildCustomerSheetRows formats an individual legacy POS customer credit sheet.
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
