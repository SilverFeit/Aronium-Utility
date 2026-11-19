package credit

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/SilverFeit/Aronium-Utility/internal/config"
	"github.com/SilverFeit/Aronium-Utility/internal/db"
)

// CreditTransaction represents a single credit bill or partial payment routed to a customer's credit account.
type CreditTransaction struct {
	DocumentID       int64    `json:"document_id"`
	DocumentNumber   string   `json:"document_number"`
	Date             string   `json:"date"`
	DueDate          string   `json:"due_date"`
	CashRegisterID   string   `json:"cash_register_id"`
	CustomerID       int64    `json:"customer_id"`
	CustomerName     string   `json:"customer_name"`
	CustomerPhone    string   `json:"customer_phone"`
	ItemsSummary     string   `json:"items_summary"`
	TotalDocument    float64  `json:"total_document"`
	PaidAmount       float64  `json:"paid_amount"`
	PendingBalance   float64  `json:"pending_balance"`
	PaymentBreakdown string   `json:"payment_breakdown"`
	AroniumNote      string   `json:"aronium_note"`
	CLINotes         []string `json:"cli_notes,omitempty"`
	CombinedNotes    string   `json:"combined_notes"`
	OverdueDays      int      `json:"overdue_days"`
	Status           string   `json:"status"` // "PENDIENTE", "ABONADO_PARCIAL", "PAGADO", "VENCIDO"
}

// CustomerCreditAccount aggregates all credit transactions, payments, and notes for a specific customer (TODO-018, TODO-019).
type CustomerCreditAccount struct {
	CustomerID         int64               `json:"customer_id"`
	CustomerCode       string              `json:"customer_code"`
	CustomerName       string              `json:"customer_name"`
	TaxNumber          string              `json:"tax_number"`
	PhoneNumber        string              `json:"phone_number"`
	Email              string              `json:"email"`
	TotalCreditIssued  float64             `json:"total_credit_issued"`
	TotalPaymentsPaid  float64             `json:"total_payments_paid"`
	PendingBalance     float64             `json:"pending_balance"`
	CreditLimit        float64             `json:"credit_limit"`
	AvailableCredit    float64             `json:"available_credit"`
	ExceedsCreditLimit bool                `json:"exceeds_credit_limit"`
	MaxOverdueDays     int                 `json:"max_overdue_days"`
	LastPurchaseDate   string              `json:"last_purchase_date"`
	LastPaymentDate    string              `json:"last_payment_date"`
	AccountStatus      string              `json:"account_status"` // "AL_DIA", "PENDIENTE", "LIMITE_EXCEDIDO", "VENCIDO"
	AccountNotes       []string            `json:"account_notes,omitempty"`
	Transactions       []CreditTransaction `json:"transactions"`
}

// LedgerReport contains the complete store-wide Accounts Receivable summary and per-customer credit accounts.
type LedgerReport struct {
	GeneratedAt           time.Time               `json:"generated_at"`
	StoreID               string                  `json:"store_id"`
	CashRegisterID        string                  `json:"cash_register_id"`
	TotalPendingPortfolio float64                 `json:"total_pending_portfolio"`
	ActiveDebtorCount     int                     `json:"active_debtor_count"`
	OverLimitCount        int                     `json:"over_limit_count"`
	OverdueCount          int                     `json:"overdue_count"`
	Accounts              []CustomerCreditAccount `json:"accounts"`
	AllTransactions       []CreditTransaction     `json:"all_transactions"`
}

// Engine computes customer credit accounts, split payments, partial payments, and notes (TODO-017..021).
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

func (e *Engine) isCreditPaymentType(ptName string, markAsPaid bool) bool {
	if !markAsPaid {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(ptName))
	for _, configured := range e.Cfg.Credit.CreditPaymentTypes {
		if lower == strings.ToLower(strings.TrimSpace(configured)) {
			return true
		}
	}
	return strings.Contains(lower, "crédito") ||
		strings.Contains(lower, "credito") ||
		strings.Contains(lower, "credit") ||
		strings.Contains(lower, "cartera") ||
		strings.Contains(lower, "cuenta corriente") ||
		strings.Contains(lower, "cuenta cliente")
}

// BuildLedger computes the complete Customer Credit Ledger across all customers, documents, split payments, and CLI notes.
func (e *Engine) BuildLedger(customerFilter string, minOverdueDays int) (*LedgerReport, error) {
	customers, err := db.ListCustomers(e.PosDB)
	if err != nil {
		return nil, err
	}
	docs, err := db.ListDocuments(e.PosDB, 0, e.Cfg.CashRegisterID)
	if err != nil {
		return nil, err
	}
	cliNotes, _ := db.ListCreditNotes(e.StateDB, "")
	customLimits, _ := db.GetCustomerCreditLimits(e.StateDB)

	custByID := make(map[int64]db.Customer)
	custByNameLower := make(map[string]db.Customer)
	for _, c := range customers {
		custByID[c.ID] = c
		custByNameLower[strings.ToLower(strings.TrimSpace(c.Name))] = c
	}

	notesByDocID := make(map[int64][]string)
	notesByCustID := make(map[int64][]string)
	notesByCustName := make(map[string][]string)
	for _, n := range cliNotes {
		formatted := n.Note
		if n.DuePromise != "" {
			formatted = fmt.Sprintf("%s (Vencimiento acordado: %s)", n.Note, n.DuePromise)
		}
		if n.DocumentID > 0 {
			notesByDocID[n.DocumentID] = append(notesByDocID[n.DocumentID], formatted)
		}
		if n.CustomerID > 0 {
			notesByCustID[n.CustomerID] = append(notesByCustID[n.CustomerID], formatted)
		}
		if n.CustomerName != "" {
			key := strings.ToLower(strings.TrimSpace(n.CustomerName))
			notesByCustName[key] = append(notesByCustName[key], formatted)
		}
	}

	accountsMap := make(map[string]*CustomerCreditAccount)

	getOrCreateAccount := func(custID int64, custName string) *CustomerCreditAccount {
		cleanName := strings.TrimSpace(custName)
		if cleanName == "" {
			cleanName = "Cliente General"
		}
		key := strings.ToLower(cleanName)
		if acc, ok := accountsMap[key]; ok {
			return acc
		}
		c, exists := custByID[custID]
		if !exists {
			if byName, ok := custByNameLower[key]; ok {
				c = byName
				custID = byName.ID
			}
		}
		limit := e.Cfg.Credit.DefaultCreditLimit
		if limit <= 0 {
			limit = 500.0
		}
		if customLim, ok := customLimits[custID]; ok && customLim > 0 {
			limit = customLim
		}
		acc := &CustomerCreditAccount{
			CustomerID:      custID,
			CustomerCode:    c.Code,
			CustomerName:    cleanName,
			TaxNumber:       c.TaxNumber,
			PhoneNumber:     c.PhoneNumber,
			Email:           c.Email,
			CreditLimit:     limit,
			AvailableCredit: limit,
			AccountStatus:   "AL_DIA",
		}
		if extr, ok := notesByCustID[custID]; ok && custID > 0 {
			acc.AccountNotes = append(acc.AccountNotes, extr...)
		}
		if extr, ok := notesByCustName[key]; ok {
			for _, noteStr := range extr {
				if !containsString(acc.AccountNotes, noteStr) {
					acc.AccountNotes = append(acc.AccountNotes, noteStr)
				}
			}
		}
		accountsMap[key] = acc
		return acc
	}

	var allTxs []CreditTransaction
	now := time.Now()

	for _, doc := range docs {
		resolvedCustID := doc.CustomerID
		resolvedCustName := strings.TrimSpace(doc.CustomerName)
		if (resolvedCustName == "" || strings.EqualFold(resolvedCustName, "Consumidor Final")) && e.Cfg.Credit.RouteByCustomerName {
			if routedName := extractRoutedCustomerName(doc, customers); routedName != "" {
				resolvedCustName = routedName
				if c, ok := custByNameLower[strings.ToLower(routedName)]; ok {
					resolvedCustID = c.ID
				}
			}
		}

		var cashPaid float64
		var creditDeclared float64
		var hasCreditPayment bool
		var paymentParts []string
		var latestPayDate string

		for _, p := range doc.Payments {
			isCred := e.isCreditPaymentType(p.PaymentTypeName, p.MarkAsPaid)
			if isCred {
				hasCreditPayment = true
				creditDeclared += p.Amount
				paymentParts = append(paymentParts, fmt.Sprintf("%s (Crédito): $%.2f", p.PaymentTypeName, p.Amount))
			} else {
				cashPaid += p.Amount
				paymentParts = append(paymentParts, fmt.Sprintf("%s (Pago): $%.2f", p.PaymentTypeName, p.Amount))
				if p.Date > latestPayDate {
					latestPayDate = p.Date
				}
			}
		}

		isUnpaidDoc := e.Cfg.Credit.DetectUnpaidStatus && doc.PaidStatus == 0
		isSplitUnderpaid := len(doc.Payments) > 0 && (cashPaid+0.01 < doc.Total)

		if !isUnpaidDoc && !hasCreditPayment && !isSplitUnderpaid {
			continue
		}

		var pending float64
		var effectivePaid float64

		if doc.PaidStatus != 0 && !hasCreditPayment && cashPaid >= doc.Total-0.01 {
			effectivePaid = doc.Total
			pending = 0
		} else if doc.PaidStatus != 0 && hasCreditPayment && len(doc.Payments) == 1 && creditDeclared >= doc.Total-0.01 {
			effectivePaid = 0
			pending = doc.Total
		} else {
			effectivePaid = math.Min(doc.Total, cashPaid)
			pending = math.Max(0, doc.Total-effectivePaid)
		}

		var itemParts []string
		for _, it := range doc.Items {
			itemParts = append(itemParts, fmt.Sprintf("%.0fx %s ($%.2f)", it.Quantity, it.ProductName, it.Total))
		}
		itemsSummary := strings.Join(itemParts, ", ")
		if itemsSummary == "" {
			itemsSummary = "(Sin detalle de ítems)"
		}

		var aroniumNotes []string
		if strings.TrimSpace(doc.Note) != "" {
			aroniumNotes = append(aroniumNotes, strings.TrimSpace(doc.Note))
		}
		if strings.TrimSpace(doc.InternalNote) != "" && doc.InternalNote != doc.Note {
			aroniumNotes = append(aroniumNotes, strings.TrimSpace(doc.InternalNote))
		}
		aroniumNoteStr := strings.Join(aroniumNotes, " | ")
		docCLINotes := notesByDocID[doc.ID]

		var combined []string
		if aroniumNoteStr != "" {
			combined = append(combined, aroniumNoteStr)
		}
		combined = append(combined, docCLINotes...)

		dueDays := e.Cfg.Credit.DefaultDueDays
		if dueDays <= 0 {
			dueDays = 15
		}
		docTime := parseFlexibleDate(doc.Date)
		dueDateStr := doc.DueDate
		var dueTime time.Time
		if dueDateStr != "" {
			dueTime = parseFlexibleDate(dueDateStr)
		}
		if dueTime.IsZero() && !docTime.IsZero() {
			dueTime = docTime.AddDate(0, 0, dueDays)
			dueDateStr = dueTime.Format("2006-01-02")
		}

		overdueDays := 0
		if pending > 0.01 && !dueTime.IsZero() && now.After(dueTime) {
			overdueDays = int(now.Sub(dueTime).Hours() / 24)
		}

		txStatus := "PENDIENTE"
		if pending <= 0.01 {
			txStatus = "PAGADO"
		} else if overdueDays > 0 {
			txStatus = "VENCIDO"
		} else if effectivePaid > 0.01 {
			txStatus = "ABONADO_PARCIAL"
		}

		acc := getOrCreateAccount(resolvedCustID, resolvedCustName)
		tx := CreditTransaction{
			DocumentID:       doc.ID,
			DocumentNumber:   doc.Number,
			Date:             doc.Date,
			DueDate:          dueDateStr,
			CashRegisterID:   doc.CashRegisterID,
			CustomerID:       acc.CustomerID,
			CustomerName:     acc.CustomerName,
			CustomerPhone:    acc.PhoneNumber,
			ItemsSummary:     itemsSummary,
			TotalDocument:    doc.Total,
			PaidAmount:       effectivePaid,
			PendingBalance:   pending,
			PaymentBreakdown: strings.Join(paymentParts, "; "),
			AroniumNote:      aroniumNoteStr,
			CLINotes:         docCLINotes,
			CombinedNotes:    strings.Join(combined, " | "),
			OverdueDays:      overdueDays,
			Status:           txStatus,
		}

		acc.Transactions = append(acc.Transactions, tx)
		acc.TotalCreditIssued += doc.Total
		acc.TotalPaymentsPaid += effectivePaid
		acc.PendingBalance += pending
		if doc.Date > acc.LastPurchaseDate {
			acc.LastPurchaseDate = doc.Date
		}
		if latestPayDate > acc.LastPaymentDate {
			acc.LastPaymentDate = latestPayDate
		}
		if overdueDays > acc.MaxOverdueDays {
			acc.MaxOverdueDays = overdueDays
		}

		allTxs = append(allTxs, tx)
	}

	for _, c := range customers {
		key := strings.ToLower(strings.TrimSpace(c.Name))
		if _, exists := accountsMap[key]; !exists {
			if len(notesByCustID[c.ID]) > 0 || len(notesByCustName[key]) > 0 {
				getOrCreateAccount(c.ID, c.Name)
			}
		}
	}

	report := &LedgerReport{
		GeneratedAt:     now.UTC(),
		StoreID:         e.Cfg.StoreID,
		CashRegisterID:  e.Cfg.CashRegisterID,
		AllTransactions: allTxs,
	}

	filterLower := strings.ToLower(strings.TrimSpace(customerFilter))
	for _, acc := range accountsMap {
		acc.AvailableCredit = math.Max(0, acc.CreditLimit-acc.PendingBalance)
		acc.ExceedsCreditLimit = acc.PendingBalance > acc.CreditLimit && acc.CreditLimit > 0

		switch {
		case acc.PendingBalance <= 0.01:
			acc.AccountStatus = "AL_DIA"
		case acc.ExceedsCreditLimit:
			acc.AccountStatus = "LIMITE_EXCEDIDO"
		case acc.MaxOverdueDays > 0:
			acc.AccountStatus = "VENCIDO"
		default:
			acc.AccountStatus = "PENDIENTE"
		}

		if filterLower != "" && !strings.Contains(strings.ToLower(acc.CustomerName), filterLower) && fmt.Sprintf("%d", acc.CustomerID) != filterLower {
			continue
		}
		if minOverdueDays > 0 && acc.MaxOverdueDays < minOverdueDays {
			continue
		}

		report.Accounts = append(report.Accounts, *acc)
		report.TotalPendingPortfolio += acc.PendingBalance
		if acc.PendingBalance > 0.01 {
			report.ActiveDebtorCount++
		}
		if acc.ExceedsCreditLimit {
			report.OverLimitCount++
		}
		if acc.MaxOverdueDays > 0 {
			report.OverdueCount++
		}
	}

	sort.Slice(report.Accounts, func(i, j int) bool {
		if report.Accounts[i].PendingBalance != report.Accounts[j].PendingBalance {
			return report.Accounts[i].PendingBalance > report.Accounts[j].PendingBalance
		}
		return report.Accounts[i].CustomerName < report.Accounts[j].CustomerName
	})

	return report, nil
}

func extractRoutedCustomerName(doc db.Document, customers []db.Customer) string {
	candidates := []string{doc.OrderNumber, doc.Note, doc.InternalNote}
	for _, text := range candidates {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			continue
		}
		for _, prefix := range []string{"cliente:", "cuenta:", "crédito:", "credito:"} {
			lower := strings.ToLower(trimmed)
			if idx := strings.Index(lower, prefix); idx >= 0 {
				rest := strings.TrimSpace(trimmed[idx+len(prefix):])
				if sep := strings.IndexAny(rest, "|;\n"); sep >= 0 {
					rest = strings.TrimSpace(rest[:sep])
				}
				if rest != "" {
					return rest
				}
			}
		}
		for _, c := range customers {
			cName := strings.TrimSpace(c.Name)
			if cName != "" && !strings.EqualFold(cName, "Consumidor Final") && strings.Contains(strings.ToLower(trimmed), strings.ToLower(cName)) {
				return cName
			}
		}
	}
	return ""
}

// GenerateStatement renders a customer's credit account statement ready for digital messaging, text, or Drive storage (TODO-020, TODO-049).
func GenerateStatement(acc CustomerCreditAccount, storeName, dualCurrency string, dualRate float64) string {
	var sb strings.Builder
	sb.WriteString("====================================================\n")
	sb.WriteString("   ESTADO DE CUENTA DE CRÉDITO DE CLIENTE\n")
	sb.WriteString(fmt.Sprintf("   Comercio: %s\n", storeName))
	sb.WriteString(fmt.Sprintf("   Fecha:    %s\n", time.Now().Format("2006-01-02 15:04")))
	sb.WriteString("====================================================\n")
	sb.WriteString(fmt.Sprintf("Cliente:         %s (ID: %d)\n", acc.CustomerName, acc.CustomerID))
	if acc.PhoneNumber != "" {
		sb.WriteString(fmt.Sprintf("Teléfono:        %s\n", acc.PhoneNumber))
	}
	sb.WriteString(fmt.Sprintf("Estado Cuenta:   %s\n", acc.AccountStatus))
	sb.WriteString(fmt.Sprintf("Total Crédito:   $%.2f\n", acc.TotalCreditIssued))
	sb.WriteString(fmt.Sprintf("Total Pagado:    $%.2f\n", acc.TotalPaymentsPaid))
	sb.WriteString(fmt.Sprintf("SALDO PENDIENTE: $%.2f\n", acc.PendingBalance))
	if dualCurrency != "" && dualRate > 0 {
		sb.WriteString(fmt.Sprintf("Equivalente %s:  %.2f %s (Tasa: %.2f)\n", dualCurrency, acc.PendingBalance/dualRate, dualCurrency, dualRate))
	}
	sb.WriteString(fmt.Sprintf("Límite Crédito:  $%.2f (Disponible: $%.2f)\n", acc.CreditLimit, acc.AvailableCredit))
	if len(acc.AccountNotes) > 0 {
		sb.WriteString(fmt.Sprintf("Anotaciones:     %s\n", strings.Join(acc.AccountNotes, " | ")))
	}
	sb.WriteString("----------------------------------------------------\n")
	sb.WriteString("DETALLE DE DOCUMENTOS Y PAGOS:\n")
	for _, tx := range acc.Transactions {
		sb.WriteString(fmt.Sprintf("- [%s] Factura #%s (%s) | Total: $%.2f | Pagado: $%.2f | Saldo: $%.2f | %s\n",
			tx.Date, tx.DocumentNumber, tx.CashRegisterID, tx.TotalDocument, tx.PaidAmount, tx.PendingBalance, tx.Status))
		sb.WriteString(fmt.Sprintf("  Productos: %s\n", tx.ItemsSummary))
		if tx.CombinedNotes != "" {
			sb.WriteString(fmt.Sprintf("  Nota:      %s\n", tx.CombinedNotes))
		}
	}
	sb.WriteString("====================================================\n")
	return sb.String()
}

func parseFlexibleDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, l := range layouts {
		if len(s) >= len(l) {
			if t, err := time.Parse(l, s[:len(l)]); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

func containsString(slice []string, target string) bool {
	for _, s := range slice {
		if s == target {
			return true
		}
	}
	return false
}
