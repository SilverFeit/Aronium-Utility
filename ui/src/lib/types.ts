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
  aronium_stock: number;
  committed_in_accounts_qty: number;
  net_stock_quantity: number;
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

export interface CatalogProduct {
  id: number;
  product_group_id: number;
  product_group_name: string;
  name: string;
  code: string;
  barcode: string;
  barcodes?: string[];
  measurement_unit: string;
  price: number;
  cost: number;
  is_enabled: boolean;
  stock_quantity: number;
  committed_in_accounts_qty: number;
  net_stock_quantity: number;
}

export interface CatalogSearchResponse {
  exact_match?: CatalogProduct | null;
  matches: CatalogProduct[];
  pos_db_mtime: string;
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

// --- Dedicated Accounts Ledger (cuentas.db) ---

export interface AccountMovementItemInput {
  aronium_product_id: number;
  codigo_barras: string;
  nombre_snapshot: string;
  precio_unitario_snapshot: number;
  cantidad: number;
}

export interface AccountMovementItem {
  id: number;
  movimiento_id: number;
  aronium_product_id: number;
  codigo_barras: string;
  nombre_snapshot: string;
  precio_unitario_snapshot: number;
  cantidad: number;
  subtotal: number;
}

export interface AccountMovement {
  id: number;
  cuenta_id: string;
  tipo: 'CARGO' | 'ABONO' | 'AJUSTE';
  monto: number;
  metodo_pago_usado: string;
  nota: string;
  fecha: string;
  items?: AccountMovementItem[];
}

export interface CustomerAccount {
  account_key: string;
  id: string;
  id_prefix?: string;
  id_number?: string;
  shared_id_count?: number;
  nombre: string;
  telefono: string;
  metodo_pago: string;
  tope_credito: number;
  descripcion: string;
  estado: 'ACTIVA' | 'BLOQUEADA' | 'CERRADA';
  creado_en: string;
  actualizado_en: string;
  total_cargos: number;
  total_abonos: number;
  total_ajustes: number;
  saldo_actual: number;
  credito_disponible: number;
  excede_tope: boolean;
  movimientos?: AccountMovement[];
}

export interface AccountsLedgerReport {
  generated_at: string;
  accounts_db_path: string;
  total_saldo_pendiente: number;
  cuentas_activas_con_saldo: number;
  total_cuentas: number;
  cuentas_sobre_tope: number;
  cuentas: CustomerAccount[];
}

export interface DashboardSnapshot {
  generated_at: string;
  store_id: string;
  store_name: string;
  registers: RegisterIdentity[];
  sales_by_register: RegisterSalesSummary[];
  inventory_summary: InventoryReport;
  accounts_summary?: AccountsLedgerReport;
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
  accounts_db_path: string;
  accounts_db_ready: boolean;
  cash_register_id: string;
  register_id_conflict: boolean;
  gdrive_configured: boolean;
  gdrive_token_valid: boolean;
  pending_outbox_count: number;
  warnings: string[];
}

export interface ConfigInfoResponse {
  config_path: string;
  config: {
    store_id: string;
    store_name: string;
    cash_register_id: string;
    pos_db_path: string;
    accounts_db_path: string;
    state_db_path: string;
  };
  default_pos_db_path: string;
  default_pos_db_exists: boolean;
  pos_db_exists: boolean;
  accounts_db_exists: boolean;
}

export interface DriveStatus {
  connected: boolean;
  client_id_configured?: boolean;
  sync_dir: string;
  master_workbook_name: string;
  spreadsheet_id?: string;
  account_email?: string;
  expiry?: string;
}

export interface SyncResult {
  timestamp: string;
  master_workbook_path: string;
  accounts_backup_path?: string;
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
  size_bytes: number;
  encrypted: boolean;
  created_at: string;
}

export interface UpdateCheckResult {
  checked_at: string;
  repository: string;
  current_version: string;
  latest_version: string;
  tag_name: string;
  update_available: boolean;
  release_title: string;
  release_notes: string;
  release_url: string;
  published_at: string;
  installer_asset_name: string;
  installer_download_url: string;
  installer_size_bytes: number;
  sha256_sums_url?: string;
}

export interface UpdateApplyResult {
  applied_at: string;
  current_version: string;
  target_version: string;
  update_available: boolean;
  downloaded: boolean;
  installer_path: string;
  sha256: string;
  verified_sha256: boolean;
  launched_installer: boolean;
  message: string;
}
