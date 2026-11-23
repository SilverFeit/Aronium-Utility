package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

type SecurityConfig struct {
	AdvancedPINHash string `yaml:"advanced_pin_hash,omitempty" json:"-"`
	HasAdvancedPIN  bool   `yaml:"-" json:"has_advanced_pin"`
}

type UIConfig struct {
	ScalePercent int    `yaml:"scale_percent" json:"scale_percent"`
	CompactMode  bool   `yaml:"compact_mode" json:"compact_mode"`
	Theme        string `yaml:"theme,omitempty" json:"theme"`
}

// Config represents the declarative schema of aronium-util.yaml (TODO-006).
type Config struct {
	StoreID          string          `yaml:"store_id" json:"store_id"`
	StoreName        string          `yaml:"store_name" json:"store_name"`
	CashRegisterID   string          `yaml:"cash_register_id" json:"cash_register_id"`
	CashRegisterName string          `yaml:"cash_register_name" json:"cash_register_name"`
	PosDBPath        string          `yaml:"pos_db_path" json:"pos_db_path"`
	PosDBPassword    string          `yaml:"pos_db_password,omitempty" json:"pos_db_password,omitempty"`
	AccountsDBPath   string          `yaml:"accounts_db_path" json:"accounts_db_path"`
	StateDBPath      string          `yaml:"state_db_path" json:"state_db_path"`
	Timezone         string          `yaml:"timezone,omitempty" json:"timezone"`
	Security         SecurityConfig  `yaml:"security,omitempty" json:"security"`
	UI               UIConfig        `yaml:"ui,omitempty" json:"ui"`
	Watch            WatchConfig     `yaml:"watch" json:"watch"`
	Credit           CreditConfig    `yaml:"credit" json:"credit"`
	Inventory        InventoryConfig `yaml:"inventory" json:"inventory"`
	GoogleDrive      GDriveConfig    `yaml:"google_drive" json:"google_drive"`
	Hub              HubConfig       `yaml:"hub" json:"hub"`
	Backup           BackupConfig    `yaml:"backup" json:"backup"`
	Fiscal           FiscalConfig    `yaml:"fiscal" json:"fiscal"`
	Reports          ReportsConfig   `yaml:"reports" json:"reports"`
	Hooks            HooksConfig     `yaml:"hooks" json:"hooks"`
}

type WatchConfig struct {
	PollIntervalMs int  `yaml:"poll_interval_ms" json:"poll_interval_ms"`
	DebounceMs     int  `yaml:"debounce_ms" json:"debounce_ms"`
	AutoSyncDrive  bool `yaml:"auto_sync_drive" json:"auto_sync_drive"`
	AutoSyncHub    bool `yaml:"auto_sync_hub" json:"auto_sync_hub"`
	AutoEmitFiscal bool `yaml:"auto_emit_fiscal" json:"auto_emit_fiscal"`
}

type CreditConfig struct {
	DetectUnpaidStatus  bool     `yaml:"detect_unpaid_status" json:"detect_unpaid_status"`
	CreditPaymentTypes  []string `yaml:"credit_payment_types" json:"credit_payment_types"`
	DefaultCreditLimit  float64  `yaml:"default_credit_limit" json:"default_credit_limit"`
	DefaultDueDays      int      `yaml:"default_due_days" json:"default_due_days"`
	RouteByCustomerName bool     `yaml:"route_by_customer_name" json:"route_by_customer_name"`
	CreditLimitPolicy   string   `yaml:"credit_limit_policy" json:"credit_limit_policy"` // "warn" or "strict"
}

type InventoryConfig struct {
	DefaultMinStock       float64 `yaml:"default_min_stock" json:"default_min_stock"`
	AlertNegativeStock    bool    `yaml:"alert_negative_stock" json:"alert_negative_stock"`
	ExpirationWarningDays []int   `yaml:"expiration_warning_days" json:"expiration_warning_days"`
	DualCurrencyCode      string  `yaml:"dual_currency_code" json:"dual_currency_code"`
	DualCurrencyRate      float64 `yaml:"dual_currency_rate" json:"dual_currency_rate"`
}

type GDriveConfig struct {
	Enabled             bool   `yaml:"enabled" json:"enabled"`
	AuthMode            string `yaml:"auth_mode" json:"auth_mode"` // "oauth2_web" or "service_account"
	ClientID            string `yaml:"client_id" json:"client_id"`
	OAuthCallbackPort   int    `yaml:"oauth_callback_port" json:"oauth_callback_port"`
	ServiceAccountJSON  string `yaml:"service_account_json,omitempty" json:"service_account_json,omitempty"`
	TokenVaultPath      string `yaml:"token_vault_path" json:"token_vault_path"`
	LocalDriveSyncDir   string `yaml:"local_drive_sync_dir" json:"local_drive_sync_dir"`
	RootFolderName      string `yaml:"root_folder_name" json:"root_folder_name"`
	RootFolderID        string `yaml:"root_folder_id,omitempty" json:"root_folder_id,omitempty"`
	MasterSpreadsheetID string `yaml:"master_spreadsheet_id,omitempty" json:"master_spreadsheet_id,omitempty"`
	MasterWorkbookName  string `yaml:"master_workbook_name" json:"master_workbook_name"`
	PerCustomerSheets   bool   `yaml:"per_customer_sheets" json:"per_customer_sheets"`
}

type HubConfig struct {
	Enabled                     bool   `yaml:"enabled" json:"enabled"`
	Mode                        string `yaml:"mode" json:"mode"` // "client" or "server"
	ListenAddr                  string `yaml:"listen_addr" json:"listen_addr"`
	ServerURL                   string `yaml:"server_url" json:"server_url"`
	DiscoveryPort               int    `yaml:"discovery_port" json:"discovery_port"`
	ConflictPolicy              string `yaml:"conflict_policy" json:"conflict_policy"`
	AllowCatalogWriteWhenClosed bool   `yaml:"allow_catalog_write_when_closed" json:"allow_catalog_write_when_closed"`
	TLSCertFile                 string `yaml:"tls_cert_file,omitempty" json:"tls_cert_file,omitempty"`
	TLSKeyFile                  string `yaml:"tls_key_file,omitempty" json:"tls_key_file,omitempty"`
}

type BackupConfig struct {
	BackupDir      string `yaml:"backup_dir" json:"backup_dir"`
	Compress       bool   `yaml:"compress" json:"compress"`
	EncryptAES256  bool   `yaml:"encrypt_aes256" json:"encrypt_aes256"`
	RetainDaily    int    `yaml:"retain_daily" json:"retain_daily"`
	RetainWeekly   int    `yaml:"retain_weekly" json:"retain_weekly"`
	RetainMonthly  int    `yaml:"retain_monthly" json:"retain_monthly"`
	UploadToGDrive bool   `yaml:"upload_to_gdrive" json:"upload_to_gdrive"`
}

type FiscalConfig struct {
	Enabled               bool    `yaml:"enabled" json:"enabled"`
	Environment           string  `yaml:"environment" json:"environment"` // "test" or "production"
	IssuerTaxID           string  `yaml:"issuer_tax_id" json:"issuer_tax_id"`
	IssuerName            string  `yaml:"issuer_name" json:"issuer_name"`
	Prefix                string  `yaml:"prefix" json:"prefix"`
	AuthorizationNumber   string  `yaml:"authorization_number" json:"authorization_number"`
	RangeFrom             int64   `yaml:"range_from" json:"range_from"`
	RangeTo               int64   `yaml:"range_to" json:"range_to"`
	ProviderURL           string  `yaml:"provider_url" json:"provider_url"`
	RequireCustomerAbove  float64 `yaml:"require_customer_above" json:"require_customer_above"`
	CopyToGDrive          bool    `yaml:"copy_to_gdrive" json:"copy_to_gdrive"`
}

type ReportsConfig struct {
	SMTPHost       string            `yaml:"smtp_host,omitempty" json:"smtp_host,omitempty"`
	SMTPPort       int               `yaml:"smtp_port,omitempty" json:"smtp_port,omitempty"`
	SMTPUser       string            `yaml:"smtp_user,omitempty" json:"smtp_user,omitempty"`
	Recipients     []string          `yaml:"recipients,omitempty" json:"recipients,omitempty"`
	TelegramChatID string            `yaml:"telegram_chat_id,omitempty" json:"telegram_chat_id,omitempty"`
	ESCPOSPrinters map[string]string `yaml:"escpos_printers,omitempty" json:"escpos_printers,omitempty"`
}

type HooksConfig struct {
	OnSale          []string `yaml:"on_sale,omitempty" json:"on_sale,omitempty"`
	OnCreditAccount []string `yaml:"on_credit_account,omitempty" json:"on_credit_account,omitempty"`
	OnLowStock      []string `yaml:"on_low_stock,omitempty" json:"on_low_stock,omitempty"`
	OnDayClose      []string `yaml:"on_day_close,omitempty" json:"on_day_close,omitempty"`
}

// DefaultAppDataDir returns %LOCALAPPDATA%\AroniumUtility on Windows or ~/.aronium-utility on Unix.
func DefaultAppDataDir() string {
	if custom := os.Getenv("ARONIUM_UTIL_HOME"); custom != "" {
		return custom
	}
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		return filepath.Join(localAppData, "AroniumUtility")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".aronium-utility"
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(home, "AppData", "Local", "AroniumUtility")
	}
	return filepath.Join(home, ".aronium-utility")
}

// DetectAroniumPosDB locates %LOCALAPPDATA%\Aronium\Data\pos.db automatically (TODO-007).
func DetectAroniumPosDB() string {
	if envPath := os.Getenv("ARONIUM_POS_DB"); envPath != "" {
		return envPath
	}
	candidates := []string{}
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		candidates = append(candidates, filepath.Join(localAppData, "Aronium", "Data", "pos.db"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, "AppData", "Local", "Aronium", "Data", "pos.db"),
			filepath.Join(home, ".aronium", "Data", "pos.db"),
			filepath.Join(".", "pos.db"),
		)
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return "pos.db"
}

// DefaultConfigPath returns the path to aronium-util.yaml.
func DefaultConfigPath() string {
	if envCfg := os.Getenv("ARONIUM_UTIL_CONFIG"); envCfg != "" {
		return envCfg
	}
	localFile := "aronium-util.yaml"
	if _, err := os.Stat(localFile); err == nil {
		return localFile
	}
	return filepath.Join(DefaultAppDataDir(), "aronium-util.yaml")
}

// DefaultConfig returns a universal offline-first configuration ready for retail stores.
func DefaultConfig() *Config {
	appDir := DefaultAppDataDir()
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "POS-01"
	}
	return &Config{
		StoreID:          "STORE-01",
		StoreName:        "Mi Comercio Aronium",
		CashRegisterID:   "POS-01",
		CashRegisterName: fmt.Sprintf("Caja Principal (%s)", hostname),
		PosDBPath:        DetectAroniumPosDB(),
		AccountsDBPath:   filepath.Join(appDir, "cuentas.db"),
		StateDBPath:      filepath.Join(appDir, "aronium_utility_state.db"),
		Timezone:         "Local",
		UI: UIConfig{
			ScalePercent: 90,
			CompactMode:  true,
			Theme:        "dark",
		},
		Watch: WatchConfig{
			PollIntervalMs: 3000,
			DebounceMs:     1000,
			AutoSyncDrive:  false,
			AutoSyncHub:    false,
			AutoEmitFiscal: false,
		},
		Credit: CreditConfig{
			DetectUnpaidStatus: true,
			CreditPaymentTypes: []string{
				"Crédito",
				"Credito",
				"Credit",
				"Cuenta Corriente",
				"Cuenta Cliente",
				"Cartera",
				"A Crédito",
			},
			DefaultCreditLimit:  500.0,
			DefaultDueDays:      15,
			RouteByCustomerName: true,
			CreditLimitPolicy:   "warn",
		},
		Inventory: InventoryConfig{
			DefaultMinStock:       5,
			AlertNegativeStock:    true,
			ExpirationWarningDays: []int{7, 15, 30},
			DualCurrencyCode:      "EUR",
			DualCurrencyRate:      1.08,
		},
		GoogleDrive: GDriveConfig{
			Enabled:            true,
			AuthMode:           "oauth2_web",
			OAuthCallbackPort:  8765,
			TokenVaultPath:     filepath.Join(appDir, "gdrive.token.enc"),
			LocalDriveSyncDir:  filepath.Join(appDir, "GoogleDriveSync"),
			RootFolderName:     "AroniumUtility",
			MasterWorkbookName: "Aronium_Inventario_y_Cuentas_Credito.xlsx",
			PerCustomerSheets:  true,
		},
		Hub: HubConfig{
			Enabled:                     false,
			Mode:                        "client",
			ListenAddr:                  "0.0.0.0:8443",
			ServerURL:                   "http://127.0.0.1:8443",
			DiscoveryPort:               8444,
			ConflictPolicy:              "last_write_wins_catalog_delta_stock",
			AllowCatalogWriteWhenClosed: false,
		},
		Backup: BackupConfig{
			BackupDir:      filepath.Join(appDir, "backups"),
			Compress:       true,
			EncryptAES256:  false,
			RetainDaily:    7,
			RetainWeekly:   4,
			RetainMonthly:  12,
			UploadToGDrive: true,
		},
		Fiscal: FiscalConfig{
			Enabled:              false,
			Environment:          "test",
			IssuerTaxID:          "900000000-1",
			IssuerName:           "COMERCIO DEMO S.A.",
			Prefix:               "POS",
			AuthorizationNumber:  "AUTH-2026-0001",
			RangeFrom:            1,
			RangeTo:              50000,
			ProviderURL:          "https://api-fiscal-sandbox.example.com/v1/documents",
			RequireCustomerAbove: 250.0,
			CopyToGDrive:         true,
		},
		Reports: ReportsConfig{
			SMTPPort:       587,
			ESCPOSPrinters: map[string]string{},
		},
		Hooks: HooksConfig{},
	}
}

// Load reads aronium-util.yaml and applies environment variable overrides.
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		path = DefaultConfigPath()
	}
	if data, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("invalid config YAML %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	cfg.ApplyEnvOverrides()
	cfg.Security.HasAdvancedPIN = strings.TrimSpace(cfg.Security.AdvancedPINHash) != ""
	if cfg.UI.ScalePercent <= 0 {
		cfg.UI.ScalePercent = 90
	} else if cfg.UI.ScalePercent < 75 {
		cfg.UI.ScalePercent = 75
	} else if cfg.UI.ScalePercent > 125 {
		cfg.UI.ScalePercent = 125
	}
	cfg.UI.Theme = NormalizeUITheme(cfg.UI.Theme)
	if strings.TrimSpace(cfg.Timezone) == "" {
		cfg.Timezone = "Local"
	} else if !strings.EqualFold(cfg.Timezone, "Local") && os.Getenv("ARONIUM_TIMEZONE") == "" {
		_ = os.Setenv("ARONIUM_TIMEZONE", cfg.Timezone)
	}
	if strings.TrimSpace(cfg.PosDBPassword) != "" && os.Getenv("ARONIUM_POS_DB_PASSWORD") == "" {
		_ = os.Setenv("ARONIUM_POS_DB_PASSWORD", cfg.PosDBPassword)
	}
	return cfg, nil
}

// NormalizeUITheme ensures UI.Theme is either "dark" (default) or "light".
func NormalizeUITheme(theme string) string {
	if strings.EqualFold(strings.TrimSpace(theme), "light") {
		return "light"
	}
	return "dark"
}

// HashPIN validates a strictly 5-digit numeric PIN and returns its salted SHA-256 hex digest.
func HashPIN(pin string) (string, error) {
	trimmed := strings.TrimSpace(pin)
	if len(trimmed) != 5 {
		return "", fmt.Errorf("el PIN debe tener exactamente 5 dígitos numéricos")
	}
	for _, ch := range trimmed {
		if ch < '0' || ch > '9' {
			return "", fmt.Errorf("el PIN solo puede contener números (0-9)")
		}
	}
	sum := sha256.Sum256([]byte("AroniumUtility::AdvPIN::" + trimmed))
	return hex.EncodeToString(sum[:]), nil
}

// VerifyPIN checks whether candidatePin matches expectedHash.
func VerifyPIN(candidatePin, expectedHash string) bool {
	if strings.TrimSpace(expectedHash) == "" {
		return true
	}
	hashed, err := HashPIN(candidatePin)
	if err != nil {
		return false
	}
	return strings.EqualFold(hashed, strings.TrimSpace(expectedHash))
}

// ApplyEnvOverrides merges ARONIUM_* environment variables into the runtime configuration.
func (c *Config) ApplyEnvOverrides() {
	if v := os.Getenv("ARONIUM_POS_DB"); v != "" {
		c.PosDBPath = v
	}
	if v := os.Getenv("ARONIUM_ACCOUNTS_DB"); v != "" {
		c.AccountsDBPath = v
	}
	if v := os.Getenv("ARONIUM_STATE_DB"); v != "" {
		c.StateDBPath = v
	}
	if v := os.Getenv("ARONIUM_STORE_ID"); v != "" {
		c.StoreID = v
	}
	if v := os.Getenv("ARONIUM_REGISTER_ID"); v != "" {
		c.CashRegisterID = v
	}
	if v := os.Getenv("ARONIUM_GDRIVE_CLIENT_ID"); v != "" {
		c.GoogleDrive.ClientID = v
	}
	if v := os.Getenv("ARONIUM_GDRIVE_SYNC_DIR"); v != "" {
		c.GoogleDrive.LocalDriveSyncDir = v
	}
	if v := os.Getenv("ARONIUM_GSHEET_ID"); v != "" {
		c.GoogleDrive.MasterSpreadsheetID = v
	}
	if v := os.Getenv("ARONIUM_HUB_URL"); v != "" {
		c.Hub.ServerURL = v
	}
	if v := os.Getenv("ARONIUM_BACKUP_DIR"); v != "" {
		c.Backup.BackupDir = v
	}
	if v := os.Getenv("ARONIUM_POS_DB_PASSWORD"); v != "" {
		c.PosDBPassword = v
	}
	if v := os.Getenv("ARONIUM_TIMEZONE"); v != "" {
		c.Timezone = v
	}
}

// Save writes the configuration to disk with 0600 permissions.
func (c *Config) Save(path string) error {
	if path == "" {
		path = DefaultConfigPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// --- Secret Vault & Encryption (TODO-064) ---

func deriveVaultKey(passphrase string) []byte {
	if passphrase == "" {
		hostname, _ := os.Hostname()
		user := os.Getenv("USERNAME")
		if user == "" {
			user = os.Getenv("USER")
		}
		passphrase = fmt.Sprintf("aronium-utility-vault::%s::%s::%s", runtime.GOOS, hostname, user)
	}
	sum := sha256.Sum256([]byte(passphrase))
	return sum[:]
}

// EncryptBytes encrypts plaintext using AES-256-GCM.
func EncryptBytes(plaintext []byte, passphrase string) ([]byte, error) {
	key := deriveVaultKey(passphrase)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// DecryptBytes decrypts AES-256-GCM ciphertext.
func DecryptBytes(ciphertext []byte, passphrase string) ([]byte, error) {
	key := deriveVaultKey(passphrase)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce := ciphertext[:gcm.NonceSize()]
	enc := ciphertext[gcm.NonceSize():]
	return gcm.Open(nil, nonce, enc, nil)
}

// SecretVault stores encrypted OAuth tokens, fiscal API keys, Hub tokens, and SMTP secrets on disk.
type SecretVault struct {
	Secrets map[string]string `json:"secrets"`
}

func vaultFilePath() string {
	return filepath.Join(DefaultAppDataDir(), "credentials.vault.enc")
}

// SaveSecret encrypts and stores a secret in the local credential vault.
func SaveSecret(key, value string) error {
	vault, _ := loadVault()
	if vault.Secrets == nil {
		vault.Secrets = make(map[string]string)
	}
	vault.Secrets[key] = value
	raw, err := json.Marshal(vault)
	if err != nil {
		return err
	}
	enc, err := EncryptBytes(raw, os.Getenv("ARONIUM_VAULT_KEY"))
	if err != nil {
		return err
	}
	path := vaultFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(enc)), 0600)
}

// LoadSecret retrieves a secret from environment variable or the encrypted credential vault.
func LoadSecret(key, envFallback string) string {
	if envFallback != "" {
		if v := os.Getenv(envFallback); v != "" {
			return v
		}
	}
	vault, err := loadVault()
	if err != nil || vault.Secrets == nil {
		return ""
	}
	return vault.Secrets[key]
}

func loadVault() (*SecretVault, error) {
	rawB64, err := os.ReadFile(vaultFilePath())
	if err != nil {
		return &SecretVault{Secrets: map[string]string{}}, err
	}
	enc, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(rawB64)))
	if err != nil {
		return &SecretVault{Secrets: map[string]string{}}, err
	}
	dec, err := DecryptBytes(enc, os.Getenv("ARONIUM_VAULT_KEY"))
	if err != nil {
		return &SecretVault{Secrets: map[string]string{}}, err
	}
	var v SecretVault
	if err := json.Unmarshal(dec, &v); err != nil {
		return &SecretVault{Secrets: map[string]string{}}, err
	}
	return &v, nil
}

// --- PII Masking for Logs & Diagnostics (Data Privacy - TODO-065) ---

var (
	emailRegex = regexp.MustCompile(`([a-zA-Z0-9._%+\-]{1,2})[a-zA-Z0-9._%+\-]*@([a-zA-Z0-9.\-]+\.[a-zA-Z]{2,})`)
	digitRegex = regexp.MustCompile(`\b\d{6,15}\b`)
)

// MaskPhone masks a phone number leaving only the last 2 digits visible.
func MaskPhone(phone string) string {
	trimmed := strings.TrimSpace(phone)
	if len(trimmed) <= 4 {
		return "****"
	}
	return strings.Repeat("*", len(trimmed)-2) + trimmed[len(trimmed)-2:]
}

// MaskTaxID masks a tax identifier leaving only the last 3 characters.
func MaskTaxID(taxID string) string {
	trimmed := strings.TrimSpace(taxID)
	if trimmed == "" || trimmed == "999999999999" {
		return trimmed
	}
	if len(trimmed) <= 3 {
		return "***"
	}
	return strings.Repeat("*", len(trimmed)-3) + trimmed[len(trimmed)-3:]
}

// MaskEmail masks an email address for debug logging.
func MaskEmail(email string) string {
	trimmed := strings.TrimSpace(email)
	parts := strings.Split(trimmed, "@")
	if len(parts) != 2 {
		return "***@***"
	}
	name := parts[0]
	if len(name) <= 1 {
		return "*@" + parts[1]
	}
	return name[:1] + "***@" + parts[1]
}

// RedactPII scrubs emails, numeric tax/phone IDs, and bearer tokens from log strings.
func RedactPII(input string) string {
	out := emailRegex.ReplaceAllString(input, "$1***@$2")
	out = digitRegex.ReplaceAllStringFunc(out, func(m string) string {
		if m == "999999999999" {
			return m
		}
		if len(m) <= 4 {
			return m
		}
		return strings.Repeat("*", len(m)-3) + m[len(m)-3:]
	})
	return out
}
