package db

import "time"

// Aronium Core Domain Models mapped from pos.db (TODO-012)

type Customer struct {
	ID          int64   `json:"id"`
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	TaxNumber   string  `json:"tax_number"`
	Address     string  `json:"address"`
	PostalCode  string  `json:"postal_code"`
	City        string  `json:"city"`
	Country     string  `json:"country"`
	Email       string  `json:"email"`
	PhoneNumber string  `json:"phone_number"`
	IsEnabled   bool    `json:"is_enabled"`
	IsCustomer  bool    `json:"is_customer"`
	IsSupplier  bool    `json:"is_supplier"`
	DueDatePeriod int   `json:"due_date_period"`
}

type Document struct {
	ID               int64          `json:"id"`
	Number           string         `json:"number"`
	UserID           int64          `json:"user_id"`
	UserName         string         `json:"user_name"`
	CustomerID       int64          `json:"customer_id"`
	CustomerName     string         `json:"customer_name"`
	CustomerTaxID    string         `json:"customer_tax_id"`
	CustomerPhone    string         `json:"customer_phone"`
	CustomerEmail    string         `json:"customer_email"`
	CashRegisterID   string         `json:"cash_register_id"`
	CashRegisterName string         `json:"cash_register_name"`
	OrderNumber      string         `json:"order_number"`
	Date             string         `json:"date"`
	StockDate        string         `json:"stock_date"`
	Total            float64        `json:"total"`
	IsClockedOut     bool           `json:"is_clocked_out"`
	DocumentTypeID   int64          `json:"document_type_id"`
	DocumentTypeName string         `json:"document_type_name"`
	WarehouseID      int64          `json:"warehouse_id"`
	ReferenceDocNum  string         `json:"reference_document_number"`
	InternalNote     string         `json:"internal_note"`
	Note             string         `json:"note"`
	DueDate          string         `json:"due_date"`
	Discount         float64        `json:"discount"`
	DiscountType     int            `json:"discount_type"`
	PaidStatus       int            `json:"paid_status"` // 0 = Unpaid / Fiado, 1 = Paid (or 2 = Paid in some versions)
	DateCreated      string         `json:"date_created"`
	DateUpdated      string         `json:"date_updated"`
	Items            []DocumentItem `json:"items,omitempty"`
	Payments         []Payment      `json:"payments,omitempty"`
}

type DocumentItem struct {
	ID                  int64   `json:"id"`
	DocumentID          int64   `json:"document_id"`
	ProductID           int64   `json:"product_id"`
	ProductCode         string  `json:"product_code"`
	ProductName         string  `json:"product_name"`
	UnitOfMeasurement   string  `json:"unit_of_measurement"`
	Quantity            float64 `json:"quantity"`
	ExpectedQuantity    float64 `json:"expected_quantity"`
	Price               float64 `json:"price"`
	PriceBeforeTax      float64 `json:"price_before_tax"`
	Discount            float64 `json:"discount"`
	DiscountType        int     `json:"discount_type"`
	ProductCost         float64 `json:"product_cost"`
	PriceBeforeDiscount float64 `json:"price_before_discount"`
	Total               float64 `json:"total"`
	ExpirationDate      string  `json:"expiration_date,omitempty"`
}

type Payment struct {
	ID              int64   `json:"id"`
	DocumentID      int64   `json:"document_id"`
	PaymentTypeID   int64   `json:"payment_type_id"`
	PaymentTypeName string  `json:"payment_type_name"`
	MarkAsPaid      bool    `json:"mark_as_paid"`
	Amount          float64 `json:"amount"`
	Date            string  `json:"date"`
	UserID          int64   `json:"user_id"`
	DateCreated     string  `json:"date_created"`
}

type PaymentType struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	Code              string `json:"code"`
	IsCustomerRequired bool  `json:"is_customer_required"`
	IsFiscal          bool   `json:"is_fiscal"`
	IsSlipRequired    bool   `json:"is_slip_required"`
	IsChangeAllowed   bool   `json:"is_change_allowed"`
	Ordinal           int    `json:"ordinal"`
	IsEnabled         bool   `json:"is_enabled"`
	IsQuickPayment    bool   `json:"is_quick_payment"`
	OpenCashDrawer    bool   `json:"open_cash_drawer"`
	MarkAsPaid        bool   `json:"mark_as_paid"`
}

type Product struct {
	ID                int64   `json:"id"`
	ProductGroupID    int64   `json:"product_group_id"`
	ProductGroupName  string  `json:"product_group_name"`
	Name              string  `json:"name"`
	Code              string  `json:"code"`
	Barcode           string  `json:"barcode"`
	PLU               int64   `json:"plu"`
	MeasurementUnit   string  `json:"measurement_unit"`
	Price             float64 `json:"price"`
	IsTaxInclusive    bool    `json:"is_tax_inclusive"`
	CurrencyID        int64   `json:"currency_id"`
	IsPriceChangeAllowed bool `json:"is_price_change_allowed"`
	IsService         bool    `json:"is_service"`
	IsUsingDefaultQty bool    `json:"is_using_default_quantity"`
	IsEnabled         bool    `json:"is_enabled"`
	Description       string  `json:"description"`
	DateCreated       string  `json:"date_created"`
	DateUpdated       string  `json:"date_updated"`
	Cost              float64 `json:"cost"`
	Markup            float64 `json:"markup"`
	StockQuantity     float64 `json:"stock_quantity"`
	WarehouseName     string  `json:"warehouse_name"`
}

// Extended State Database Models (aronium_utility_state.db - TODO-010)

type SyncCheckpoint struct {
	LastDocumentID int64     `json:"last_document_id"`
	LastPaymentID  int64     `json:"last_payment_id"`
	StockHash      string    `json:"stock_hash"`
	CatalogHash    string    `json:"catalog_hash"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type FiadoNote struct {
	ID          int64     `json:"id"`
	CustomerID  int64     `json:"customer_id"`
	CustomerName string   `json:"customer_name"`
	DocumentID  int64     `json:"document_id"`
	DocumentNum string    `json:"document_num"`
	Note        string    `json:"note"`
	DuePromise  string    `json:"due_promise,omitempty"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
}

type OutboxItem struct {
	ID             int64     `json:"id"`
	IdempotencyKey string    `json:"idempotency_key"`
	Target         string    `json:"target"` // "gdrive", "hub", "dian"
	EventType      string    `json:"event_type"`
	PayloadJSON    string    `json:"payload_json"`
	Status         string    `json:"status"` // "PENDING", "SENT", "FAILED"
	Attempts       int       `json:"attempts"`
	LastError      string    `json:"last_error"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type DoctorReport struct {
	Timestamp             string            `json:"timestamp"`
	Healthy               bool              `json:"healthy"`
	PosDBPath             string            `json:"pos_db_path"`
	PosDBReadable         bool              `json:"pos_db_readable"`
	QuickCheckResult      string            `json:"quick_check_result"`
	AroniumSchemaVersion  string            `json:"aronium_schema_version"`
	DetectedTables        []string          `json:"detected_tables"`
	MissingCoreTables     []string          `json:"missing_core_tables"`
	StateDBPath           string            `json:"state_db_path"`
	StateDBReady          bool              `json:"state_db_ready"`
	CashRegisterID        string            `json:"cash_register_id"`
	RegisterIDConflict    bool              `json:"register_id_conflict"`
	GDriveConfigured      bool              `json:"gdrive_configured"`
	GDriveTokenValid      bool              `json:"gdrive_token_valid"`
	PendingOutboxCount    int               `json:"pending_outbox_count"`
	Warnings              []string          `json:"warnings"`
	Details               map[string]string `json:"details"`
}
