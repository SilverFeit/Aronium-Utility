package registers

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/SilverFeit/Aronium-Utility/internal/db"
)

// RegisterIdentity represents a POS workstation's cash register identity (PRO-FR-003, TODO-029).
type RegisterIdentity struct {
	RegisterID    string `json:"register_id"`
	RegisterName  string `json:"register_name"`
	StoreID       string `json:"store_id"`
	Hostname      string `json:"hostname"`
	IPAddress     string `json:"ip_address"`
	PrintersJSON  string `json:"printers_json"`
	LastHeartbeat string `json:"last_heartbeat"`
}

// OriginMetadata tags every transaction, backup, and report with its workstation origin (TODO-030).
type OriginMetadata struct {
	StoreID        string `json:"store_id"`
	CashRegisterID string `json:"cash_register_id"`
	Hostname       string `json:"hostname"`
	UserID         int64  `json:"user_id"`
	TimestampUTC   string `json:"timestamp_utc"`
}

// RegisterSalesSummary aggregates sales metrics for a specific cash register terminal (PRO-FR-004, TODO-031).
type RegisterSalesSummary struct {
	RegisterID       string        `json:"register_id"`
	RegisterName     string        `json:"register_name"`
	TransactionCount int           `json:"transaction_count"`
	TotalSales       float64       `json:"total_sales"`
	CashSales        float64       `json:"cash_sales"`
	CreditFiadoSales float64       `json:"credit_fiado_sales"`
	AverageTicket    float64       `json:"average_ticket"`
	Documents        []db.Document `json:"documents,omitempty"`
}

// Manager handles cash register assignment, duplicate identity prevention, and sales traceability.
type Manager struct {
	PosDB   *sql.DB
	StateDB *sql.DB
	StoreID string
}

func NewManager(posDB, stateDB *sql.DB, storeID string) *Manager {
	return &Manager{
		PosDB:   posDB,
		StateDB: stateDB,
		StoreID: storeID,
	}
}

// ClaimIdentity registers a cash register ID for the given hostname and rejects accidental duplicates across hosts (TODO-029, TC-029).
func (m *Manager) ClaimIdentity(registerID, registerName, hostname string, force bool) (*RegisterIdentity, error) {
	registerID = strings.TrimSpace(registerID)
	if registerID == "" {
		return nil, fmt.Errorf("register_id cannot be empty")
	}
	if hostname == "" {
		hostname, _ = os.Hostname()
	}
	if registerName == "" {
		registerName = registerID
	}

	var existingHost string
	err := m.StateDB.QueryRow(`SELECT hostname FROM register_metadata WHERE register_id = ?;`, registerID).Scan(&existingHost)
	if err == nil && existingHost != "" && !strings.EqualFold(existingHost, hostname) && !force {
		return nil, fmt.Errorf("conflict (TC-029): cash register '%s' is already assigned to workstation '%s' (current workstation: '%s')", registerID, existingHost, hostname)
	}

	nowStr := time.Now().UTC().Format(time.RFC3339)
	_, err = m.StateDB.Exec(`
		INSERT INTO register_metadata (register_id, register_name, store_id, hostname, last_heartbeat)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(register_id) DO UPDATE SET
			register_name = excluded.register_name,
			store_id = excluded.store_id,
			hostname = excluded.hostname,
			last_heartbeat = excluded.last_heartbeat;
	`, registerID, registerName, m.StoreID, hostname, nowStr)
	if err != nil {
		return nil, err
	}

	return &RegisterIdentity{
		RegisterID:    registerID,
		RegisterName:  registerName,
		StoreID:       m.StoreID,
		Hostname:      hostname,
		LastHeartbeat: nowStr,
	}, nil
}

// ListRegisters returns all configured cash registers from stateDB.
func (m *Manager) ListRegisters() ([]RegisterIdentity, error) {
	rows, err := m.StateDB.Query(`
		SELECT register_id, register_name, store_id, hostname, ip_address, printers_json, COALESCE(CAST(last_heartbeat AS TEXT), '')
		FROM register_metadata
		ORDER BY register_id ASC;
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []RegisterIdentity
	for rows.Next() {
		var r RegisterIdentity
		if err := rows.Scan(&r.RegisterID, &r.RegisterName, &r.StoreID, &r.Hostname, &r.IPAddress, &r.PrintersJSON, &r.LastHeartbeat); err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	return list, nil
}

// TagOrigin builds the OriginMetadata tag for any transaction or artifact (TODO-030).
func (m *Manager) TagOrigin(registerID string, userID int64) OriginMetadata {
	hostname, _ := os.Hostname()
	return OriginMetadata{
		StoreID:        m.StoreID,
		CashRegisterID: registerID,
		Hostname:       hostname,
		UserID:         userID,
		TimestampUTC:   time.Now().UTC().Format(time.RFC3339),
	}
}

// QuerySales filters and compares sales by cash register and date range (PRO-FR-004, TODO-031, AC-014..AC-016).
func (m *Manager) QuerySales(defaultRegisterID, filterRegisterID, fromDate, toDate string) ([]RegisterSalesSummary, error) {
	docs, err := db.ListDocuments(m.PosDB, 0, defaultRegisterID)
	if err != nil {
		return nil, err
	}

	byReg := make(map[string]*RegisterSalesSummary)
	for _, doc := range docs {
		regID := doc.CashRegisterID
		// Also support explicit register tag in OrderNumber or InternalNote if multi-register documents were consolidated
		for _, prefix := range []string{"POS-", "CAJA-"} {
			if idx := strings.Index(strings.ToUpper(doc.InternalNote), prefix); idx >= 0 {
				token := strings.Fields(doc.InternalNote[idx:])[0]
				regID = strings.Trim(token, "|;,. ")
				doc.CashRegisterID = regID
				break
			}
		}
		if regID == "" {
			regID = defaultRegisterID
		}

		if filterRegisterID != "" && !strings.EqualFold(regID, filterRegisterID) {
			continue
		}
		if fromDate != "" && len(doc.Date) >= len(fromDate) && doc.Date[:len(fromDate)] < fromDate {
			continue
		}
		if toDate != "" && len(doc.Date) >= len(toDate) && doc.Date[:len(toDate)] > toDate {
			continue
		}

		sum, ok := byReg[regID]
		if !ok {
			sum = &RegisterSalesSummary{
				RegisterID:   regID,
				RegisterName: regID,
			}
			byReg[regID] = sum
		}
		sum.TransactionCount++
		sum.TotalSales += doc.Total
		if doc.PaidStatus == 0 {
			sum.CreditFiadoSales += doc.Total
		} else {
			sum.CashSales += doc.Total
		}
		sum.Documents = append(sum.Documents, doc)
	}

	var out []RegisterSalesSummary
	for _, s := range byReg {
		if s.TransactionCount > 0 {
			s.AverageTicket = s.TotalSales / float64(s.TransactionCount)
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].RegisterID < out[j].RegisterID
	})
	return out, nil
}
