package inventory

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/SilverFeit/Aronium-Utility/internal/accounts"
	"github.com/SilverFeit/Aronium-Utility/internal/config"
	"github.com/SilverFeit/Aronium-Utility/internal/db"
)

// ItemStatus represents the stock health of a product.
type ItemStatus string

const (
	StatusOK       ItemStatus = "OK"
	StatusLowStock ItemStatus = "STOCK_BAJO"
	StatusOut      ItemStatus = "AGOTADO"
	StatusNegative ItemStatus = "STOCK_NEGATIVO"
)

// InventoryItem represents an enriched product inventory record (TODO-022, TODO-104).
type InventoryItem struct {
	ProductID              int64      `json:"product_id"`
	Code                   string     `json:"code"`
	Barcode                string     `json:"barcode"`
	Name                   string     `json:"name"`
	Category               string     `json:"category"`
	MeasurementUnit        string     `json:"measurement_unit"`
	StockQuantity          float64    `json:"stock_quantity"`
	AroniumStock           float64    `json:"aronium_stock"`
	CommittedInAccountsQty float64    `json:"committed_in_accounts_qty"`
	NetStockQuantity       float64    `json:"net_stock_quantity"`
	MinStock               float64    `json:"min_stock"`
	Cost                   float64    `json:"cost"`
	SalePrice              float64    `json:"sale_price"`
	TotalCostValue         float64    `json:"total_cost_value"`
	TotalRetailValue       float64    `json:"total_retail_value"`
	EstimatedMargin        float64    `json:"estimated_margin_pct"`
	Status                 ItemStatus `json:"status"`
	ExpirationDate         string     `json:"expiration_date,omitempty"`
	DaysToExpire           int        `json:"days_to_expire,omitempty"`
	BatchLot               string     `json:"batch_lot,omitempty"`
}

// InventoryReport summarizes store-wide stock valuation, alerts, and items.
type InventoryReport struct {
	GeneratedAt      time.Time       `json:"generated_at"`
	StoreID          string          `json:"store_id"`
	TotalProducts    int             `json:"total_products"`
	TotalUnits       float64         `json:"total_units"`
	TotalCostValue   float64         `json:"total_cost_value"`
	TotalRetailValue float64         `json:"total_retail_value"`
	EstimatedProfit  float64         `json:"estimated_profit"`
	LowStockCount    int             `json:"low_stock_count"`
	OutOfStockCount  int             `json:"out_of_stock_count"`
	NegativeCount    int             `json:"negative_stock_count"`
	ExpiringSoon     []InventoryItem `json:"expiring_soon,omitempty"`
	Alerts           []InventoryItem `json:"alerts,omitempty"`
	Items            []InventoryItem `json:"items"`
}

// KardexEntry represents a simplified historical stock movement row (TODO-023).
type KardexEntry struct {
	ID             int64   `json:"id"`
	DocumentID     int64   `json:"document_id"`
	DocumentNumber string  `json:"document_number"`
	ProductID      int64   `json:"product_id"`
	ProductCode    string  `json:"product_code"`
	ProductName    string  `json:"product_name"`
	DeltaQty       float64 `json:"delta_qty"`
	ResultingQty   float64 `json:"resulting_qty"`
	RegisterID     string  `json:"register_id"`
	RecordedAt     string  `json:"recorded_at"`
}

// PriceListItem represents a differentiated price list entry (wholesale/retail/custom - TODO-051).
type PriceListItem struct {
	ListName     string  `json:"list_name"`
	ProductID    int64   `json:"product_id"`
	ProductCode  string  `json:"product_code"`
	ProductName  string  `json:"product_name"`
	BasePrice    float64 `json:"base_price"`
	CustomPrice  float64 `json:"custom_price"`
	Currency     string  `json:"currency"`
	DualPrice    float64 `json:"dual_price,omitempty"`
}

// Engine manages inventory valuation, Kardex movement tracking, stock alerts, and expirations.
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

type expMeta struct {
	BatchLot string
	ExpDate  string
	MinStock float64
}

// BuildReport compiles the full live inventory report with net stock (Stock_Aronium - Unidades_En_Cuentas), valuation, and alerts (TODO-022..024, TODO-104).
func (e *Engine) BuildReport(maxExpireDays int) (*InventoryReport, error) {
	products, err := db.ListProductsWithStock(e.PosDB)
	if err != nil {
		return nil, err
	}

	committedUnits := make(map[int64]float64)
	if e.Cfg != nil && strings.TrimSpace(e.Cfg.AccountsDBPath) != "" {
		if _, err := os.Stat(e.Cfg.AccountsDBPath); err == nil {
			if accMgr, err := accounts.NewManager(e.Cfg.AccountsDBPath); err == nil {
				if cu, err := accMgr.ComputePendingUnitsByProduct(); err == nil {
					committedUnits = cu
				}
				_ = accMgr.Close()
			}
		}
	}

	expMap := make(map[int64]expMeta)
	if e.StateDB != nil {
		rows, err := e.StateDB.Query(`SELECT product_id, batch_lot, expiration_date, min_stock_override FROM product_expirations;`)
		if err == nil {
			for rows.Next() {
				var pid int64
				var m expMeta
				if err := rows.Scan(&pid, &m.BatchLot, &m.ExpDate, &m.MinStock); err == nil {
					expMap[pid] = m
				}
			}
			rows.Close()
		}
	}

	if maxExpireDays <= 0 {
		maxExpireDays = 30
	}
	now := time.Now()
	rep := &InventoryReport{
		GeneratedAt: now.UTC(),
		StoreID:     e.Cfg.StoreID,
	}

	for _, p := range products {
		minStock := e.Cfg.Inventory.DefaultMinStock
		var expDate, batch string
		if meta, ok := expMap[p.ID]; ok {
			expDate = meta.ExpDate
			batch = meta.BatchLot
			if meta.MinStock >= 0 {
				minStock = meta.MinStock
			}
		}

		committedQty := committedUnits[p.ID]
		netQty := p.StockQuantity - committedQty

		status := StatusOK
		switch {
		case netQty < 0:
			status = StatusNegative
			rep.NegativeCount++
		case netQty == 0 && !p.IsService:
			status = StatusOut
			rep.OutOfStockCount++
		case netQty <= minStock && !p.IsService:
			status = StatusLowStock
			rep.LowStockCount++
		}

		costVal := 0.0
		retailVal := 0.0
		if netQty > 0 {
			costVal = netQty * p.Cost
			retailVal = netQty * p.Price
		}
		margin := 0.0
		if p.Price > 0 && p.Cost > 0 {
			margin = ((p.Price - p.Cost) / p.Price) * 100.0
		}

		daysToExpire := 0
		hasExpWarning := false
		if expDate != "" {
			if expTime, err := time.Parse("2006-01-02", expDate); err == nil {
				daysToExpire = int(expTime.Sub(now).Hours() / 24)
				if daysToExpire <= maxExpireDays {
					hasExpWarning = true
				}
			}
		}

		item := InventoryItem{
			ProductID:              p.ID,
			Code:                   p.Code,
			Barcode:                p.Barcode,
			Name:                   p.Name,
			Category:               p.ProductGroupName,
			MeasurementUnit:        p.MeasurementUnit,
			StockQuantity:          p.StockQuantity,
			AroniumStock:           p.StockQuantity,
			CommittedInAccountsQty: committedQty,
			NetStockQuantity:       netQty,
			MinStock:               minStock,
			Cost:                   p.Cost,
			SalePrice:              p.Price,
			TotalCostValue:         costVal,
			TotalRetailValue:       retailVal,
			EstimatedMargin:        margin,
			Status:                 status,
			ExpirationDate:         expDate,
			DaysToExpire:           daysToExpire,
			BatchLot:               batch,
		}

		rep.Items = append(rep.Items, item)
		rep.TotalProducts++
		if netQty > 0 {
			rep.TotalUnits += netQty
		}
		rep.TotalCostValue += costVal
		rep.TotalRetailValue += retailVal

		if status != StatusOK {
			rep.Alerts = append(rep.Alerts, item)
		}
		if hasExpWarning {
			rep.ExpiringSoon = append(rep.ExpiringSoon, item)
		}
	}

	rep.EstimatedProfit = rep.TotalRetailValue - rep.TotalCostValue
	return rep, nil
}

// SyncKardexFromDocuments inspects documents and records stock deltas into kardex_history (TODO-023).
func (e *Engine) SyncKardexFromDocuments() (int, error) {
	docs, err := db.ListDocuments(e.PosDB, 0, e.Cfg.CashRegisterID)
	if err != nil {
		return 0, err
	}
	products, err := db.ListProductsWithStock(e.PosDB)
	if err != nil {
		return 0, err
	}
	currentStock := make(map[int64]float64)
	for _, p := range products {
		currentStock[p.ID] = p.StockQuantity
	}

	inserted := 0
	for _, doc := range docs {
		// DocumentTypeId=1 in Aronium is typically Purchase/Stock In; 2 is Sale/Out
		sign := -1.0
		if doc.DocumentTypeID == 1 || strings.HasPrefix(strings.ToUpper(doc.Number), "COMP") || strings.HasPrefix(strings.ToUpper(doc.Number), "IN") {
			sign = 1.0
		}
		for _, it := range doc.Items {
			delta := sign * it.Quantity
			res, err := e.StateDB.Exec(`
				INSERT OR IGNORE INTO kardex_history
					(document_id, document_number, product_id, product_code, product_name, delta_qty, resulting_qty, register_id, recorded_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, COALESCE(NULLIF(?, ''), CURRENT_TIMESTAMP));
			`, doc.ID, doc.Number, it.ProductID, it.ProductCode, it.ProductName, delta, currentStock[it.ProductID], doc.CashRegisterID, doc.Date)
			if err == nil {
				if aff, _ := res.RowsAffected(); aff > 0 {
					inserted++
				}
			}
		}
	}
	return inserted, nil
}

// ListKardex returns recent Kardex movements from aronium_utility_state.db (TODO-023).
func (e *Engine) ListKardex(limit int) ([]KardexEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := e.StateDB.Query(`
		SELECT id, document_id, document_number, product_id, product_code, product_name, delta_qty, resulting_qty, register_id, COALESCE(CAST(recorded_at AS TEXT), '')
		FROM kardex_history
		ORDER BY id DESC
		LIMIT ?;
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KardexEntry
	for rows.Next() {
		var k KardexEntry
		if err := rows.Scan(&k.ID, &k.DocumentID, &k.DocumentNumber, &k.ProductID, &k.ProductCode, &k.ProductName, &k.DeltaQty, &k.ResultingQty, &k.RegisterID, &k.RecordedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, nil
}

// SetProductExpiration stores expiration metadata and optional minimum stock override in stateDB (TODO-048).
func (e *Engine) SetProductExpiration(productID int64, productName, batchLot, expirationDate string, minStockOverride float64) error {
	_, err := e.StateDB.Exec(`
		INSERT INTO product_expirations (product_id, product_name, batch_lot, expiration_date, min_stock_override, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(product_id) DO UPDATE SET
			product_name = excluded.product_name,
			batch_lot = excluded.batch_lot,
			expiration_date = excluded.expiration_date,
			min_stock_override = excluded.min_stock_override,
			updated_at = CURRENT_TIMESTAMP;
	`, productID, productName, batchLot, expirationDate, minStockOverride)
	return err
}

// SetPriceListEntry sets a differentiated price (e.g., Mayorista, Minorista, VIP) in stateDB (TODO-051).
func (e *Engine) SetPriceListEntry(listName string, productID int64, productName string, customPrice float64, currency string) error {
	if currency == "" {
		currency = "USD"
	}
	_, err := e.StateDB.Exec(`
		INSERT INTO price_lists (list_name, product_id, product_name, custom_price, currency, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(list_name, product_id) DO UPDATE SET
			product_name = excluded.product_name,
			custom_price = excluded.custom_price,
			currency = excluded.currency,
			updated_at = CURRENT_TIMESTAMP;
	`, listName, productID, productName, customPrice, currency)
	return err
}

// GetPriceList retrieves a differentiated price list with dual-currency support (TODO-049, TODO-051).
func (e *Engine) GetPriceList(listName string) ([]PriceListItem, error) {
	products, err := db.ListProductsWithStock(e.PosDB)
	if err != nil {
		return nil, err
	}
	customMap := make(map[int64]float64)
	rows, err := e.StateDB.Query(`SELECT product_id, custom_price FROM price_lists WHERE LOWER(list_name) = LOWER(?);`, listName)
	if err == nil {
		for rows.Next() {
			var pid int64
			var cp float64
			if err := rows.Scan(&pid, &cp); err == nil {
				customMap[pid] = cp
			}
		}
		rows.Close()
	}

	factor := 1.0
	switch strings.ToLower(listName) {
	case "mayorista", "wholesale":
		factor = 0.90 // 10% wholesale discount default when not explicitly overridden
	case "especial", "vip":
		factor = 0.95
	}

	var items []PriceListItem
	for _, p := range products {
		price := p.Price * factor
		if custom, ok := customMap[p.ID]; ok {
			price = custom
		}
		dual := 0.0
		if e.Cfg.Inventory.DualCurrencyRate > 0 {
			dual = price / e.Cfg.Inventory.DualCurrencyRate
		}
		items = append(items, PriceListItem{
			ListName:    listName,
			ProductID:   p.ID,
			ProductCode: p.Code,
			ProductName: p.Name,
			BasePrice:   p.Price,
			CustomPrice: price,
			Currency:    "USD",
			DualPrice:   dual,
		})
	}
	return items, nil
}

func FormatAlertsSummary(rep *InventoryReport) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Inventario Total: %d productos | Valor Costo: $%.2f | Valor Venta: $%.2f\n",
		rep.TotalProducts, rep.TotalCostValue, rep.TotalRetailValue))
	sb.WriteString(fmt.Sprintf("Alertas: %d Stock Bajo | %d Agotados | %d Stock Negativo | %d Próximos a Vencer\n",
		rep.LowStockCount, rep.OutOfStockCount, rep.NegativeCount, len(rep.ExpiringSoon)))
	return sb.String()
}
