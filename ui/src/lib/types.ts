export interface RegisterIdentity {
  register_id: string;
  register_name: string;
  store_id: string;
  hostname: string;
  ip_address: string;
  last_heartbeat: string;
}

export interface RegisterSalesSummary {
  register_id: string;
  register_name: string;
  transaction_count: number;
  total_sales: number;
  cash_sales: number;
  credit_sales: number;
  average_ticket: number;
}

export interface InventoryItem {
  product_id: number;
  code: string;
  barcode: string;
  name: string;
  category: string;
  measurement_unit: string;
  stock_quantity: number;
  min_stock: number;
  cost: number;
  sale_price: number;
  total_cost_value: number;
  total_retail_value: number;
  estimated_margin_pct: number;
  status: 'OK' | 'STOCK_BAJO' | 'AGOTADO' | 'STOCK_NEGATIVO';
  expiration_date?: string;
  days_to_expire?: number;
  batch_lot?: string;
}

export interface InventoryReport {
  generated_at: string;
  store_id: string;
  total_products: number;
  total_units: number;
  total_cost_value: number;
  total_retail_value: number;
  estimated_profit: number;
  low_stock_count: number;
  out_of_stock_count: number;
  negative_stock_count: number;
  expiring_soon?: InventoryItem[];
  alerts?: InventoryItem[];
  items: InventoryItem[];
}

export interface KardexEntry {
  id: number;
  document_id: number;
  document_number: string;
  product_id: number;
  product_code: string;
  product_name: string;
  delta_qty: number;
  resulting_qty: number;
  register_id: string;
  recorded_at: string;
}

export interface CreditTransaction {
  document_id: number;
  document_number: string;
  date: string;
  due_date: string;
  cash_register_id: string;
  customer_id: number;
  customer_name: string;
  customer_phone: string;
  items_summary: string;
  total_document: number;
  paid_amount: number;
  pending_balance: number;
  payment_breakdown: string;
  aronium_note: string;
  cli_notes?: string[];
  combined_notes: string;
  overdue_days: number;
  status: string;
}

export interface CustomerCreditAccount {
  customer_id: number;
  customer_code: string;
  customer_name: string;
  tax_number: string;
  phone_number: string;
  email: string;
  total_credit_issued: number;
  total_payments_paid: number;
  pending_balance: number;
  credit_limit: number;
  available_credit: number;
  exceeds_credit_limit: boolean;
  max_overdue_days: number;
  last_purchase_date: string;
  last_payment_date: string;
  account_status: string;
  account_notes?: string[];
  transactions: CreditTransaction[];
}

export interface LedgerReport {
  generated_at: string;
  store_id: string;
  cash_register_id: string;
  total_pending_portfolio: number;
  active_debtor_count: number;
  over_limit_count: number;
  overdue_count: number;
  accounts: CustomerCreditAccount[];
  all_transactions: CreditTransaction[];
}

export interface DashboardSnapshot {
  generated_at: string;
  store_id: string;
  store_name: string;
  registers: RegisterIdentity[];
  sales_by_register: RegisterSalesSummary[];
  inventory_summary: InventoryReport;
  credit_portfolio: LedgerReport;
}

export interface ProductSalesRank {
  product_id: number;
  product_name: string;
  quantity: number;
  revenue: number;
}

export interface DailyClosingReport {
  date: string;
  store_id: string;
  store_name: string;
  total_transactions: number;
  total_sales: number;
  dual_currency_code: string;
  dual_currency_rate: number;
  total_sales_dual: number;
  sales_by_payment_method: Record<string, number>;
  sales_by_register: Record<string, number>;
  new_credit_issued: number;
  payments_received: number;
  total_portfolio: number;
  top_products: ProductSalesRank[];
}

export interface DoctorReport {
  timestamp: string;
  healthy: boolean;
  pos_db_path: string;
  pos_db_readable: boolean;
  quick_check_result: string;
  aronium_schema_version: string;
  detected_tables: string[];
  missing_core_tables: string[];
  state_db_path: string;
  state_db_ready: boolean;
  cash_register_id: string;
  register_id_conflict: boolean;
  gdrive_configured: boolean;
  gdrive_token_valid: boolean;
  pending_outbox_count: number;
  warnings: string[];
}

export interface DriveStatus {
  connected: boolean;
  sync_dir: string;
  master_workbook_name: string;
  spreadsheet_id?: string;
  account_email?: string;
  expiry?: string;
}

export interface SyncResult {
  timestamp: string;
  master_workbook_path: string;
  customer_files: string[];
  inventory_files: string[];
  uploaded_drive_ids?: Record<string, string>;
  updated_sheet_id?: string;
  queued_offline: boolean;
  flushed_outbox_items: number;
}

export interface BackupManifest {
  archive_path: string;
  checksum_path: string;
  sha256: string;
  encrypted: boolean;
  compressed: boolean;
  size_bytes: number;
  store_id: string;
  cash_register_id: string;
  created_at: string;
}
