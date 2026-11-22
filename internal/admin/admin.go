package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/SilverFeit/Aronium-Utility/internal/accounts"
	"github.com/SilverFeit/Aronium-Utility/internal/config"
	"github.com/SilverFeit/Aronium-Utility/internal/credit"
	"github.com/SilverFeit/Aronium-Utility/internal/inventory"
	"github.com/SilverFeit/Aronium-Utility/internal/registers"
)

// DashboardSnapshot aggregates all administrative panels (PRO-FR-005, TODO-037).
type DashboardSnapshot struct {
	GeneratedAt      time.Time                        `json:"generated_at"`
	StoreID          string                           `json:"store_id"`
	StoreName        string                           `json:"store_name"`
	Registers        []registers.RegisterIdentity     `json:"registers"`
	SalesByRegister  []registers.RegisterSalesSummary `json:"sales_by_register"`
	InventorySummary *inventory.InventoryReport       `json:"inventory_summary"`
	CreditPortfolio  *credit.LedgerReport             `json:"credit_portfolio"`
	AccountsSummary  *accounts.AccountsLedgerReport   `json:"accounts_summary,omitempty"`
}

// Console provides the read-only Administrative Console (TUI + Local HTTP Web Dashboard on localhost:8080).
type Console struct {
	Cfg     *config.Config
	PosDB   *sql.DB
	StateDB *sql.DB
}

func NewConsole(cfg *config.Config, posDB, stateDB *sql.DB) *Console {
	return &Console{
		Cfg:     cfg,
		PosDB:   posDB,
		StateDB: stateDB,
	}
}

// BuildSnapshot collects consolidated sales by register, live inventory, customer credit portfolio, and terminal health.
func (c *Console) BuildSnapshot(filterRegister string) (*DashboardSnapshot, error) {
	regMgr := registers.NewManager(c.PosDB, c.StateDB, c.Cfg.StoreID)
	regList, _ := regMgr.ListRegisters()
	sales, err := regMgr.QuerySales(c.Cfg.CashRegisterID, filterRegister, "", "")
	if err != nil {
		return nil, err
	}
	invEng := inventory.NewEngine(c.Cfg, c.PosDB, c.StateDB)
	invRep, err := invEng.BuildReport(30)
	if err != nil {
		return nil, err
	}
	creditEng := credit.NewEngine(c.Cfg, c.PosDB, c.StateDB)
	creditRep, err := creditEng.BuildLedger("", 0)
	if err != nil {
		return nil, err
	}

	var accSummary *accounts.AccountsLedgerReport
	if strings.TrimSpace(c.Cfg.AccountsDBPath) != "" {
		if _, statErr := os.Stat(c.Cfg.AccountsDBPath); statErr == nil {
			if accMgr, accErr := accounts.NewManager(c.Cfg.AccountsDBPath); accErr == nil {
				accSummary, _ = accMgr.ListAccounts("")
				_ = accMgr.Close()
			}
		}
	}

	return &DashboardSnapshot{
		GeneratedAt:      time.Now().UTC(),
		StoreID:          c.Cfg.StoreID,
		StoreName:        c.Cfg.StoreName,
		Registers:        regList,
		SalesByRegister:  sales,
		InventorySummary: invRep,
		CreditPortfolio:  creditRep,
		AccountsSummary:  accSummary,
	}, nil
}

// RenderTerminalTUI renders a structured console administrative view (Management Modules equivalent - TODO-037).
func (c *Console) RenderTerminalTUI(filterRegister string) (string, error) {
	snap, err := c.BuildSnapshot(filterRegister)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString("╔══════════════════════════════════════════════════════════════════════════╗\n")
	sb.WriteString(fmt.Sprintf("║  ARONIUM UTILITY — CONSOLA ADMINISTRATIVA (%-28s) ║\n", snap.StoreName))
	sb.WriteString("╠══════════════════════════════════════════════════════════════════════════╣\n")

	// 1. Terminals & Sales by Register
	sb.WriteString("║ [1] VENTAS CONSOLIDADAS POR CAJA REGISTRADORA (PRO-FR-004 / PRO-FR-005)  ║\n")
	for _, s := range snap.SalesByRegister {
		sb.WriteString(fmt.Sprintf("║   • %-10s | Docs: %-4d | Total: $%-12.2f | Crédito: $%-8.2f ║\n",
			s.RegisterID, s.TransactionCount, s.TotalSales, s.CreditSales))
	}

	// 2. Inventory & Alerts
	sb.WriteString("╠══════════════════════════════════════════════════════════════════════════╣\n")
	sb.WriteString("║ [2] ESTADO DE INVENTARIO GLOBAL Y ALERTAS                                ║\n")
	sb.WriteString(fmt.Sprintf("║   • Productos: %-5d | Costo: $%-12.2f | Venta: $%-14.2f ║\n",
		snap.InventorySummary.TotalProducts, snap.InventorySummary.TotalCostValue, snap.InventorySummary.TotalRetailValue))
	sb.WriteString(fmt.Sprintf("║   • Alertas: Stock Bajo=%d | Agotados=%d | Stock Negativo=%d | Por Vencer=%d ║\n",
		snap.InventorySummary.LowStockCount, snap.InventorySummary.OutOfStockCount, snap.InventorySummary.NegativeCount, len(snap.InventorySummary.ExpiringSoon)))

	// 3. Customer Credit Portfolio
	sb.WriteString("╠══════════════════════════════════════════════════════════════════════════╣\n")
	sb.WriteString("║ [3] CARTERA TOTAL DE CUENTAS DE CRÉDITO POR CLIENTE                      ║\n")
	sb.WriteString(fmt.Sprintf("║   • Cartera Pendiente: $%-12.2f | Clientes con Saldo: %-4d        ║\n",
		snap.CreditPortfolio.TotalPendingPortfolio, snap.CreditPortfolio.ActiveDebtorCount))
	for _, acc := range snap.CreditPortfolio.Accounts {
		if acc.PendingBalance > 0 {
			sb.WriteString(fmt.Sprintf("║     - %-24s | Saldo: $%-10.2f | Estado: %-10s ║\n",
				truncateStr(acc.CustomerName, 24), acc.PendingBalance, acc.AccountStatus))
		}
	}
	sb.WriteString("╚══════════════════════════════════════════════════════════════════════════╝\n")
	return sb.String(), nil
}

// Handler returns the HTTP handler for the read-only local administrative web server (`localhost:8080` - TODO-037).
func (c *Console) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/admin/snapshot", func(w http.ResponseWriter, r *http.Request) {
		reg := r.URL.Query().Get("register")
		snap, err := c.BuildSnapshot(reg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snap)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		reg := r.URL.Query().Get("register")
		tui, err := c.RenderTerminalTUI(reg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!DOCTYPE html><html lang="es"><head><meta charset="utf-8"/><title>Aronium Utility Admin</title>
<style>body{background:#0f172a;color:#e2e8f0;font-family:monospace;padding:24px;}pre{background:#1e293b;padding:20px;border-radius:10px;border:1px solid #334155;overflow-x:auto;}</style></head>
<body><h2>Aronium Utility — Panel Administrativo Local (Solo Lectura)</h2><pre>%s</pre></body></html>`, tui)
	})
	return mux
}

// ServeHTTP starts the local read-only admin server until ctx is canceled.
func (c *Console) ServeHTTP(ctx context.Context, addr string) error {
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	srv := &http.Server{Addr: addr, Handler: c.Handler()}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	return srv.ListenAndServe()
}

func truncateStr(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen-1]) + "…"
}
