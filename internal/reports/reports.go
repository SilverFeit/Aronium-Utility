package reports

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/SilverFeit/Aronium-Utility/internal/config"
	"github.com/SilverFeit/Aronium-Utility/internal/credit"
	"github.com/SilverFeit/Aronium-Utility/internal/db"
)

// ProductSalesRank tracks top-selling products for daily closing reports (TODO-047).
type ProductSalesRank struct {
	ProductID   int64   `json:"product_id"`
	ProductName string  `json:"product_name"`
	Quantity    float64 `json:"quantity"`
	Revenue     float64 `json:"revenue"`
}

// DailyClosingReport consolidates daily sales, payment methods, cash registers, new customer credit issued, payments received, and dual currency (TODO-047, TODO-049).
type DailyClosingReport struct {
	Date                 string             `json:"date"`
	StoreID              string             `json:"store_id"`
	StoreName            string             `json:"store_name"`
	TotalTransactions    int                `json:"total_transactions"`
	TotalSales           float64            `json:"total_sales"`
	DualCurrencyCode     string             `json:"dual_currency_code"`
	DualCurrencyRate     float64            `json:"dual_currency_rate"`
	TotalSalesDual       float64            `json:"total_sales_dual"`
	SalesByPaymentMethod map[string]float64 `json:"sales_by_payment_method"`
	SalesByRegister      map[string]float64 `json:"sales_by_register"`
	NewCreditIssued      float64            `json:"new_credit_issued"`
	PaymentsReceived     float64            `json:"payments_received"`
	TotalPortfolio       float64            `json:"total_portfolio"`
	TopProducts          []ProductSalesRank `json:"top_products"`
}

// Engine generates daily closing reports, delivers them via SMTP/Telegram/Google Drive, and routes ESC/POS kitchen/bar tickets.
type Engine struct {
	Cfg     *config.Config
	PosDB   *sql.DB
	StateDB *sql.DB
}

func NewEngine(cfg *config.Config, posDB, stateDB *sql.DB) *Engine {
	return &Engine{
		Cfg:     cfg,
		PosDB:   posDB,
		StateDB: stateDB,
	}
}

// BuildDailyReport compiles the daily closing report for a given date (YYYY-MM-DD, or empty for all/today) (TODO-047, TODO-049).
func (e *Engine) BuildDailyReport(dateFilter string) (*DailyClosingReport, error) {
	docs, err := db.ListDocuments(e.PosDB, 0, e.Cfg.CashRegisterID)
	if err != nil {
		return nil, err
	}
	creditEng := credit.NewEngine(e.Cfg, e.PosDB, e.StateDB)
	ledger, err := creditEng.BuildLedger("", 0)
	if err != nil {
		return nil, err
	}

	dualCode := e.Cfg.Inventory.DualCurrencyCode
	if dualCode == "" {
		dualCode = "EUR"
	}
	dualRate := e.Cfg.Inventory.DualCurrencyRate
	if dualRate <= 0 {
		dualRate = 1.08
	}

	rep := &DailyClosingReport{
		Date:                 dateFilter,
		StoreID:              e.Cfg.StoreID,
		StoreName:            e.Cfg.StoreName,
		DualCurrencyCode:     dualCode,
		DualCurrencyRate:     dualRate,
		SalesByPaymentMethod: make(map[string]float64),
		SalesByRegister:      make(map[string]float64),
		TotalPortfolio:       ledger.TotalPendingPortfolio,
	}
	if rep.Date == "" {
		rep.Date = time.Now().Format("2006-01-02")
	}

	prodMap := make(map[int64]*ProductSalesRank)

	for _, doc := range docs {
		if dateFilter != "" && len(doc.Date) >= len(dateFilter) && !strings.HasPrefix(doc.Date, dateFilter) {
			continue
		}
		rep.TotalTransactions++
		rep.TotalSales += doc.Total
		rep.SalesByRegister[doc.CashRegisterID] += doc.Total

		if len(doc.Payments) == 0 && doc.PaidStatus == 0 {
			rep.SalesByPaymentMethod["Crédito Cliente (Sin Pago)"] += doc.Total
			rep.NewCreditIssued += doc.Total
		}
		for _, p := range doc.Payments {
			rep.SalesByPaymentMethod[p.PaymentTypeName] += p.Amount
			lower := strings.ToLower(p.PaymentTypeName)
			if strings.Contains(lower, "crédito") || strings.Contains(lower, "credito") || strings.Contains(lower, "credit") || !p.MarkAsPaid {
				rep.NewCreditIssued += p.Amount
			} else {
				rep.PaymentsReceived += p.Amount
			}
		}

		for _, it := range doc.Items {
			rk, ok := prodMap[it.ProductID]
			if !ok {
				rk = &ProductSalesRank{
					ProductID:   it.ProductID,
					ProductName: it.ProductName,
				}
				prodMap[it.ProductID] = rk
			}
			rk.Quantity += it.Quantity
			rk.Revenue += it.Total
		}
	}

	rep.TotalSalesDual = rep.TotalSales / dualRate

	for _, rk := range prodMap {
		rep.TopProducts = append(rep.TopProducts, *rk)
	}
	sort.Slice(rep.TopProducts, func(i, j int) bool {
		return rep.TopProducts[i].Revenue > rep.TopProducts[j].Revenue
	})
	if len(rep.TopProducts) > 10 {
		rep.TopProducts = rep.TopProducts[:10]
	}

	return rep, nil
}

// FormatDailyReportText renders the daily closing report in human-readable text with Dual Currency (TODO-047, TODO-049).
func (e *Engine) FormatDailyReportText(rep *DailyClosingReport) string {
	var sb strings.Builder
	sb.WriteString("=========================================================\n")
	sb.WriteString(fmt.Sprintf("  REPORTE DE CIERRE DIARIO — %s (%s)\n", rep.StoreName, rep.StoreID))
	sb.WriteString(fmt.Sprintf("  Fecha de Corte: %s\n", rep.Date))
	sb.WriteString("=========================================================\n")
	sb.WriteString(fmt.Sprintf("Transacciones Cerradas:  %d\n", rep.TotalTransactions))
	sb.WriteString(fmt.Sprintf("Ventas Totales:          $%.2f\n", rep.TotalSales))
	sb.WriteString(fmt.Sprintf("Ventas Totales (%s):    %.2f %s (Tasa: %.2f)\n", rep.DualCurrencyCode, rep.TotalSalesDual, rep.DualCurrencyCode, rep.DualCurrencyRate))
	sb.WriteString(fmt.Sprintf("Nuevos Créditos:         $%.2f\n", rep.NewCreditIssued))
	sb.WriteString(fmt.Sprintf("Ingresos / Pagos:        $%.2f\n", rep.PaymentsReceived))
	sb.WriteString(fmt.Sprintf("Cartera Total Activa:    $%.2f\n", rep.TotalPortfolio))
	sb.WriteString("---------------------------------------------------------\n")
	sb.WriteString("VENTAS POR MEDIO DE PAGO:\n")
	for k, v := range rep.SalesByPaymentMethod {
		sb.WriteString(fmt.Sprintf("  - %-22s: $%.2f\n", k, v))
	}
	sb.WriteString("---------------------------------------------------------\n")
	sb.WriteString("VENTAS POR CAJA REGISTRADORA:\n")
	for k, v := range rep.SalesByRegister {
		sb.WriteString(fmt.Sprintf("  - %-22s: $%.2f\n", k, v))
	}
	sb.WriteString("---------------------------------------------------------\n")
	sb.WriteString("TOP PRODUCTOS VENDIDOS:\n")
	for idx, p := range rep.TopProducts {
		sb.WriteString(fmt.Sprintf("  %d. %-24s | Cant: %.0f | Total: $%.2f\n", idx+1, p.ProductName, p.Quantity, p.Revenue))
	}
	sb.WriteString("=========================================================\n")
	return sb.String()
}

// SaveAndDeliver writes the daily closing report to the Google Drive Sync folder and optionally sends via Telegram/SMTP (TODO-047).
func (e *Engine) SaveAndDeliver(ctx context.Context, rep *DailyClosingReport) (savedPath string, err error) {
	text := e.FormatDailyReportText(rep)
	reportsDir := filepath.Join(e.Cfg.GoogleDrive.LocalDriveSyncDir, e.Cfg.GoogleDrive.RootFolderName, "Reportes")
	if err := os.MkdirAll(reportsDir, 0755); err != nil {
		return "", err
	}
	savedPath = filepath.Join(reportsDir, fmt.Sprintf("Cierre_Diario_%s.txt", rep.Date))
	if err := os.WriteFile(savedPath, []byte(text), 0644); err != nil {
		return "", err
	}

	botToken := config.LoadSecret("telegram_bot_token", "ARONIUM_TELEGRAM_BOT_TOKEN")
	if botToken != "" && e.Cfg.Reports.TelegramChatID != "" {
		_ = sendTelegramMessage(ctx, botToken, e.Cfg.Reports.TelegramChatID, text)
	}

	smtpPass := config.LoadSecret("smtp_password", "ARONIUM_SMTP_PASSWORD")
	if e.Cfg.Reports.SMTPHost != "" && smtpPass != "" && len(e.Cfg.Reports.Recipients) > 0 {
		addr := fmt.Sprintf("%s:%d", e.Cfg.Reports.SMTPHost, e.Cfg.Reports.SMTPPort)
		auth := smtp.PlainAuth("", e.Cfg.Reports.SMTPUser, smtpPass, e.Cfg.Reports.SMTPHost)
		msg := fmt.Sprintf("To: %s\r\nSubject: Reporte Cierre Diario %s (%s)\r\n\r\n%s",
			strings.Join(e.Cfg.Reports.Recipients, ","), e.Cfg.StoreName, rep.Date, text)
		_ = smtp.SendMail(addr, auth, e.Cfg.Reports.SMTPUser, e.Cfg.Reports.Recipients, []byte(msg))
	}

	return savedPath, nil
}

func sendTelegramMessage(ctx context.Context, botToken, chatID, text string) error {
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)
	payload, _ := json.Marshal(map[string]string{
		"chat_id": chatID,
		"text":    text,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// BuildESCPOSTicket formats an ESC/POS thermal printer byte stream for a kitchen/bar station (TODO-050).
func BuildESCPOSTicket(stationName string, doc db.Document, items []db.DocumentItem) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0x1B, 0x40})
	buf.Write([]byte{0x1B, 0x45, 0x01})
	buf.WriteString(fmt.Sprintf("*** COMANDA %s ***\n", strings.ToUpper(stationName)))
	buf.Write([]byte{0x1B, 0x45, 0x00})
	buf.WriteString(fmt.Sprintf("Orden/Factura: #%s | Caja: %s\n", doc.Number, doc.CashRegisterID))
	buf.WriteString(fmt.Sprintf("Cliente:       %s\n", doc.CustomerName))
	buf.WriteString(fmt.Sprintf("Fecha:         %s\n", doc.Date))
	buf.WriteString("--------------------------------\n")
	for _, it := range items {
		buf.WriteString(fmt.Sprintf("%.0f x %s\n", it.Quantity, it.ProductName))
	}
	if doc.Note != "" {
		buf.WriteString("--------------------------------\n")
		buf.WriteString(fmt.Sprintf("NOTA: %s\n", doc.Note))
	}
	buf.WriteString("\n\n\n")
	buf.Write([]byte{0x1D, 0x56, 0x42, 0x00})
	return buf.Bytes()
}

// RouteOrderToESCPOS transmits category-filtered ESC/POS comandas to network thermal printers (TODO-050).
func (e *Engine) RouteOrderToESCPOS(doc db.Document, categoryByProductID map[int64]string) (int, error) {
	if len(e.Cfg.Reports.ESCPOSPrinters) == 0 {
		return 0, nil
	}
	grouped := make(map[string][]db.DocumentItem)
	for _, it := range doc.Items {
		cat := categoryByProductID[it.ProductID]
		for stationKey := range e.Cfg.Reports.ESCPOSPrinters {
			if strings.EqualFold(cat, stationKey) || strings.EqualFold(stationKey, "ALL") {
				grouped[stationKey] = append(grouped[stationKey], it)
			}
		}
	}

	sent := 0
	for station, items := range grouped {
		printerAddr := e.Cfg.Reports.ESCPOSPrinters[station]
		ticket := BuildESCPOSTicket(station, doc, items)
		conn, err := net.DialTimeout("tcp", printerAddr, 3*time.Second)
		if err != nil {
			return sent, err
		}
		_, err = conn.Write(ticket)
		_ = conn.Close()
		if err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
}
