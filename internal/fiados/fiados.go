package fiados

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

// FiadoTransaction represents a single credit bill or partial abono routed to a customer's account.
type FiadoTransaction struct {
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
	Status           string   `json:"status"` // "PENDIENTE", "ABONADO_PARCIAL", "PAGADO", "EN_MORA"
}

// CustomerCreditAccount aggregates all credit transactions, abonos, and notes for a specific customer (TODO-018, TODO-019).
type CustomerCreditAccount struct {
	CustomerID         int64              `json:"customer_id"`
	CustomerCode       string             `json:"customer_code"`
	CustomerName       string             `json:"customer_name"`
	TaxNumber          string             `json:"tax_number"`
	PhoneNumber        string             `json:"phone_number"`
	Email              string             `json:"email"`
	TotalCreditIssued  float64            `json:"total_credit_issued"`
	TotalAbonosPaid    float64            `json:"total_abonos_paid"`
	PendingBalance     float64            `json:"pending_balance"`
	CreditLimit        float64            `json:"credit_limit"`
	AvailableCredit    float64            `json:"available_credit"`
	ExceedsCreditLimit bool               `json:"exceeds_credit_limit"`
	MaxOverdueDays     int                `json:"max_overdue_days"`
	LastPurchaseDate   string             `json:"last_purchase_date"`
	LastPaymentDate    string             `json:"last_payment_date"`
	AccountStatus      string             `json:"account_status"` // "AL_DIA", "PENDIENTE", "CUPO_EXCEDIDO", "EN_MORA"
	AccountNotes       []string           `json:"account_notes,omitempty"`
	Transactions       []FiadoTransaction `json:"transactions"`
}

// LedgerReport contains the complete store-wide Fiados summary and per-customer accounts.
type LedgerReport struct {
	GeneratedAt        time.Time               `json:"generated_at"`
	StoreID            string                  `json:"store_id"`
	CashRegisterID     string                  `json:"cash_register_id"`
	TotalPendingPortfolio float64              `json:"total_pending_portfolio"`
	ActiveDebtorCount  int                     `json:"active_debtor_count"`
	OverLimitCount     int                     `json:"over_limit_count"`
	OverdueCount       int                     `json:"overdue_count"`
	Accounts           []CustomerCreditAccount `json:"accounts"`
	AllTransactions    []FiadoTransaction      `json:"all_transactions"`
}

// Engine computes customer credit accounts ("Fiados"), split payments, abonos, and notes (TODO-017..021).
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
	for _, configured := range e.Cfg.Fiados.CreditPaymentTypes {
		if lower == strings.ToLower(strings.TrimSpace(configured)) {
			return true
		}
	}
	return strings.Contains(lower, "fiado") ||
		strings.Contains(lower, "crédito") ||
		strings.Contains(lower, "credito") ||
		strings.Contains(lower, "cartera") ||
		strings.Contains(lower, "cuenta corriente")
}

// BuildLedger computes the complete Fiados ledger across all customers, documents, split payments, and CLI notes.
func (e *Engine) BuildLedger(customerFilter string, minOverdueDays int) (*LedgerReport, error) {
	customers, err := db.ListCustomers(e.PosDB)
	if err != nil {
		return nil, err
	}
	docs, err := db.ListDocuments(e.PosDB, 0, e.Cfg.CashRegisterID)
	if err != nil {
		return nil, err
	}
	cliNotes, _ := db.ListFiadoNotes(e.StateDB, "")
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
			formatted = fmt.Sprintf("%s (Compromiso: %s)", n.Note, n.DuePromise)
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
			cleanName = "Cliente Sin Nombre"
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
		limit := e.Cfg.Fiados.DefaultCreditLimit
		if limit <= 0 {
			limit = 500000
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

	var allTxs []FiadoTransaction
	now := time.Now()

	for _, doc := range docs {
		// Resolve customer by CustomerId, CustomerName, or note/order routing ("Cliente: <Nombre>") (TODO-018)
		resolvedCustID := doc.CustomerID
		resolvedCustName := strings.TrimSpace(doc.CustomerName)
		if (resolvedCustName == "" || strings.EqualFold(resolvedCustName, "Consumidor Final")) && e.Cfg.Fiados.RouteByCustomerName {
			if routedName := extractRoutedCustomerName(doc, customers); routedName != "" {
				resolvedCustName = routedName
				if c, ok := custByNameLower[strings.ToLower(routedName)]; ok {
					resolvedCustID = c.ID
				}
			}
		}

		// Calculate payment breakdown: effective cash/abonos vs credit/fiado payments (TODO-017, TODO-019)
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
				paymentParts = append(paymentParts, fmt.Sprintf("%s (Fiado): $%.2f", p.PaymentTypeName, p.Amount))
			} else {
				cashPaid += p.Amount
				paymentParts = append(paymentParts, fmt.Sprintf("%s (Abono): $%.2f", p.PaymentTypeName, p.Amount))
				if p.Date > latestPayDate {
					latestPayDate = p.Date
				}
			}
		}

		isUnpaidDoc := e.Cfg.Fiados.DetectUnpaidStatus && doc.PaidStatus == 0
		isSplitUnderpaid := len(doc.Payments) > 0 && (cashPaid+0.01 < doc.Total)

		// A document belongs to the Fiados ledger if it was unpaid, used a credit payment type, or had a split balance
		if !isUnpaidDoc && !hasCreditPayment && !isSplitUnderpaid {
			continue
		}

		var pending float64
		var effectivePaid float64

		if doc.PaidStatus != 0 && !hasCreditPayment && cashPaid >= doc.Total-0.01 {
			// Fully settled document that was previously unpaid
			effectivePaid = doc.Total
			pending = 0
		} else if doc.PaidStatus != 0 && hasCreditPayment && len(doc.Payments) == 1 && creditDeclared >= doc.Total-0.01 {
			// PaidStatus was marked 1 by Aronium simply because a payment row of type "Fiado" covered the total
			effectivePaid = 0
			pending = doc.Total
		} else {
			effectivePaid = math.Min(doc.Total, cashPaid)
			pending = math.Max(0, doc.Total-effectivePaid)
		}

		// Build itemized product summary
		var itemParts []string
		for _, it := range doc.Items {
			itemParts = append(itemParts, fmt.Sprintf("%.0fx %s ($%.0f)", it.Quantity, it.ProductName, it.Total))
		}
		itemsSummary := strings.Join(itemParts, ", ")
		if itemsSummary == "" {
			itemsSummary = "(Sin detalle de ítems)"
		}

		// Combine Aronium internal/public notes + CLI notes
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

		// Calculate overdue days
		dueDays := e.Cfg.Fiados.DefaultDueDays
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
			txStatus = "EN_MORA"
		} else if effectivePaid > 0.01 {
			txStatus = "ABONADO_PARCIAL"
		}

		acc := getOrCreateAccount(resolvedCustID, resolvedCustName)
		tx := FiadoTransaction{
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
		acc.TotalAbonosPaid += effectivePaid
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

	// Ensure customers that have CLI notes or credit limits also appear if filtered
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
			acc.AccountStatus = "CUPO_EXCEDIDO"
		case acc.MaxOverdueDays > 0:
			acc.AccountStatus = "EN_MORA"
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

// extractRoutedCustomerName checks if a note or order number names a specific customer when CustomerId was left default (TODO-018).
func extractRoutedCustomerName(doc db.Document, customers []db.Customer) string {
	candidates := []string{doc.OrderNumber, doc.Note, doc.InternalNote}
	for _, text := range candidates {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			continue
		}
		// Match explicit prefix "Cliente: Nombre" or "Fiado: Nombre" or "Cuenta: Nombre"
		for _, prefix := range []string{"cliente:", "fiado:", "cuenta:"} {
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
		// Or match an existing customer name in pos.db
		for _, c := range customers {
			cName := strings.TrimSpace(c.Name)
			if cName != "" && !strings.EqualFold(cName, "Consumidor Final") && strings.Contains(strings.ToLower(trimmed), strings.ToLower(cName)) {
				return cName
			}
		}
	}
	return ""
}

// GenerateStatement renders a customer's credit account statement ready for WhatsApp, text, or Drive storage (TODO-020, TODO-049).
func GenerateStatement(acc CustomerCreditAccount, storeName, dualCurrency string, dualRate float64) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("====================================================\n"))
	sb.WriteString(fmt.Sprintf("   ESTADO DE CUENTA / CARTERA DE FIADOS\n"))
	sb.WriteString(fmt.Sprintf("   Comercio: %s\n", storeName))
	sb.WriteString(fmt.Sprintf("   Fecha:    %s\n", time.Now().Format("2006-01-02 15:04")))
	sb.WriteString(fmt.Sprintf("====================================================\n"))
	sb.WriteString(fmt.Sprintf("Cliente:         %s (ID: %d)\n", acc.CustomerName, acc.CustomerID))
	if acc.PhoneNumber != "" {
		sb.WriteString(fmt.Sprintf("Teléfono:        %s\n", acc.PhoneNumber))
	}
	sb.WriteString(fmt.Sprintf("Estado Cuenta:   %s\n", acc.AccountStatus))
	sb.WriteString(fmt.Sprintf("Total Fiado:     $%.2f COP\n", acc.TotalCreditIssued))
	sb.WriteString(fmt.Sprintf("Total Abonado:   $%.2f COP\n", acc.TotalAbonosPaid))
	sb.WriteString(fmt.Sprintf("SALDO PENDIENTE: $%.2f COP\n", acc.PendingBalance))
	if dualCurrency != "" && dualRate > 0 {
		sb.WriteString(fmt.Sprintf("Equivalente %s:  %.2f %s (Tasa: %.2f)\n", dualCurrency, acc.PendingBalance/dualRate, dualCurrency, dualRate))
	}
	sb.WriteString(fmt.Sprintf("Cupo Asignado:   $%.2f COP (Disponible: $%.2f)\n", acc.CreditLimit, acc.AvailableCredit))
	if len(acc.AccountNotes) > 0 {
		sb.WriteString(fmt.Sprintf("Anotaciones:     %s\n", strings.Join(acc.AccountNotes, " | ")))
	}
	sb.WriteString("----------------------------------------------------\n")
	sb.WriteString("DETALLE DE FACTURAS Y ABONOS:\n")
	for _, tx := range acc.Transactions {
		sb.WriteString(fmt.Sprintf("- [%s] Factura #%s (%s) | Total: $%.2f | Abonado: $%.2f | Saldo: $%.2f | %s\n",
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
