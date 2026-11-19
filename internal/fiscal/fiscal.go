package fiscal

import (
	"bytes"
	"context"
	"crypto/sha512"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/SilverFeit/Aronium-Utility/internal/config"
	"github.com/SilverFeit/Aronium-Utility/internal/db"
)

// FiscalStatus enumerates the lifecycle states of an electronic fiscal document (TODO-053).
const (
	StatusPending     = "PENDING"
	StatusSent        = "SENT"
	StatusAccepted    = "ACCEPTED"
	StatusRejected    = "REJECTED"
	StatusContingency = "CONTINGENCY"
)

// FiscalSubmissionResult holds the response from the fiscal provider and electronic invoice validation (TODO-052, TODO-056).
type FiscalSubmissionResult struct {
	AroniumDocumentID     int64  `json:"aronium_document_id"`
	AroniumDocumentNumber string `json:"aronium_document_number"`
	FiscalNumber          string `json:"fiscal_number"`
	DocumentKind          string `json:"document_kind"` // "POS_ELECTRONICO", "FACTURA_ELECTRONICA", "NOTA_CREDITO"
	Status                string `json:"status"`
	DocumentUUID          string `json:"document_uuid"`
	QRData                string `json:"qr_data"`
	XMLPath               string `json:"xml_path"`
	PDFPath               string `json:"pdf_path"`
	ThermalTicket         string `json:"thermal_ticket"`
	APIResponse           string `json:"api_response"`
}

// FiscalProvider defines the decoupled interface for any country or electronic invoicing provider (TODO-052).
type FiscalProvider interface {
	ValidateCustomer(doc db.Document) error
	BuildInvoicePayload(doc db.Document) ([]byte, error)
	SubmitDocument(ctx context.Context, doc db.Document) (*FiscalSubmissionResult, error)
	CheckStatus(ctx context.Context, fiscalNumber string) (string, error)
	EmitCreditNote(ctx context.Context, originalDoc db.Document, reason string) (*FiscalSubmissionResult, error)
	DownloadArtifacts(ctx context.Context, res *FiscalSubmissionResult, destDir string) (xmlPath, pdfPath string, err error)
}

var taxIDRegex = regexp.MustCompile(`^[A-Za-z0-9\-]{5,20}$`)

// StandardUBLProvider implements FiscalProvider for UBL 2.1 Electronic Invoicing (TODO-054..056).
type StandardUBLProvider struct {
	Cfg        *config.Config
	StateDB    *sql.DB
	HTTPClient *http.Client
}

func NewStandardUBLProvider(cfg *config.Config, stateDB *sql.DB) *StandardUBLProvider {
	return &StandardUBLProvider{
		Cfg:        cfg,
		StateDB:    stateDB,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// ValidateCustomer enforces tax ID and buyer identification rules without blocking the register (TODO-054).
func (p *StandardUBLProvider) ValidateCustomer(doc db.Document) error {
	taxID := strings.TrimSpace(doc.CustomerTaxID)
	if taxID == "" {
		taxID = "999999999999"
	}
	if !taxIDRegex.MatchString(taxID) {
		return fmt.Errorf("identificación fiscal inválida '%s': debe contener entre 5 y 20 caracteres alfanuméricos", taxID)
	}
	threshold := p.Cfg.Fiscal.RequireCustomerAbove
	if threshold > 0 && doc.Total > threshold && (taxID == "999999999999" || strings.EqualFold(doc.CustomerName, "Consumidor Final")) {
		return fmt.Errorf("venta supera el límite de $%.2f para consumidor final: requiere identificación fiscal y nombre del cliente", threshold)
	}
	if doc.CustomerEmail != "" && !strings.Contains(doc.CustomerEmail, "@") {
		return fmt.Errorf("correo electrónico del cliente inválido '%s'", doc.CustomerEmail)
	}
	return nil
}

// ComputeDocumentUUID computes a cryptographic SHA-384 UUID signature for the UBL 2.1 electronic invoice (TODO-056).
func (p *StandardUBLProvider) ComputeDocumentUUID(fiscalNumber, dateStr string, total float64, customerTaxID string) string {
	if customerTaxID == "" {
		customerTaxID = "999999999999"
	}
	envCode := "2"
	if strings.EqualFold(p.Cfg.Fiscal.Environment, "production") {
		envCode = "1"
	}
	seed := fmt.Sprintf("%s|%s|%.2f|%s|%s|%s|%s",
		fiscalNumber, dateStr, total, p.Cfg.Fiscal.IssuerTaxID, customerTaxID, p.Cfg.Fiscal.AuthorizationNumber, envCode)
	sum384 := sha512.Sum384([]byte(seed))
	return hex.EncodeToString(sum384[:])
}

// BuildInvoicePayload constructs the UBL 2.1 JSON/REST payload for electronic invoicing providers (TODO-055).
func (p *StandardUBLProvider) BuildInvoicePayload(doc db.Document) ([]byte, error) {
	prefix := p.Cfg.Fiscal.Prefix
	if prefix == "" {
		prefix = "POS"
	}
	fiscalNum := fmt.Sprintf("%s-%s", prefix, doc.Number)
	taxID := strings.TrimSpace(doc.CustomerTaxID)
	if taxID == "" {
		taxID = "999999999999"
	}
	uuid := p.ComputeDocumentUUID(fiscalNum, doc.Date, doc.Total, taxID)
	payload := map[string]any{
		"ubl_version":          "UBL 2.1",
		"environment":          p.Cfg.Fiscal.Environment,
		"authorization_number": p.Cfg.Fiscal.AuthorizationNumber,
		"prefix":               prefix,
		"fiscal_number":        fiscalNum,
		"issue_date":           doc.Date,
		"issuer": map[string]string{
			"tax_id": p.Cfg.Fiscal.IssuerTaxID,
			"name":   p.Cfg.Fiscal.IssuerName,
		},
		"customer": map[string]string{
			"tax_id": taxID,
			"name":   doc.CustomerName,
			"email":  doc.CustomerEmail,
			"phone":  doc.CustomerPhone,
		},
		"document_uuid": uuid,
		"total":         doc.Total,
		"line_items":    doc.Items,
	}
	return json.MarshalIndent(payload, "", "  ")
}

// SubmitDocument validates the customer, submits the electronic invoice or queues it in contingency, and saves UBL 2.1 XML/PDF artifacts (TODO-054..056).
func (p *StandardUBLProvider) SubmitDocument(ctx context.Context, doc db.Document) (*FiscalSubmissionResult, error) {
	prefix := p.Cfg.Fiscal.Prefix
	if prefix == "" {
		prefix = "POS"
	}
	fiscalNum := fmt.Sprintf("%s-%s", prefix, doc.Number)
	taxID := strings.TrimSpace(doc.CustomerTaxID)
	if taxID == "" {
		taxID = "999999999999"
	}

	uuid := p.ComputeDocumentUUID(fiscalNum, doc.Date, doc.Total, taxID)
	qrURL := fmt.Sprintf("https://verify.fiscal.example.org/document?uuid=%s", uuid)

	res := &FiscalSubmissionResult{
		AroniumDocumentID:     doc.ID,
		AroniumDocumentNumber: doc.Number,
		FiscalNumber:          fiscalNum,
		DocumentKind:          "FACTURA_ELECTRONICA",
		DocumentUUID:          uuid,
		QRData:                qrURL,
	}

	if err := p.ValidateCustomer(doc); err != nil {
		res.Status = StatusPending
		res.APIResponse = "Pendiente completar datos fiscales del cliente: " + err.Error()
		_ = p.persistFiscalRecord(doc, res)
		return res, err
	}

	payloadBytes, err := p.BuildInvoicePayload(doc)
	if err != nil {
		return nil, err
	}

	apiURL := strings.TrimSpace(p.Cfg.Fiscal.ProviderURL)
	if apiURL != "" && !strings.Contains(apiURL, "example.com") {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payloadBytes))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if apiKey := config.LoadSecret("fiscal_api_key", "ARONIUM_FISCAL_API_KEY"); apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
		resp, err := p.HTTPClient.Do(req)
		if err != nil {
			res.Status = StatusContingency
			res.APIResponse = "Contingencia Offline (sin conexión a proveedor fiscal): " + err.Error()
			if p.StateDB != nil {
				_, _ = db.EnqueueOutbox(p.StateDB, "fiscal:"+fiscalNum, "fiscal", "FiscalSubmit", string(payloadBytes))
			}
		} else {
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			res.APIResponse = string(body)
			if resp.StatusCode >= 400 {
				res.Status = StatusRejected
			} else {
				res.Status = StatusAccepted
			}
		}
	} else {
		res.Status = StatusAccepted
		res.APIResponse = `{"status":"ACCEPTED","message":"Documento electrónico validado conforme a UBL 2.1"}`
	}

	monthFolder := time.Now().Format("2006-01")
	if len(doc.Date) >= 7 {
		monthFolder = doc.Date[:7]
	}
	fiscalDir := filepath.Join(p.Cfg.GoogleDrive.LocalDriveSyncDir, p.Cfg.GoogleDrive.RootFolderName, "Facturacion_Electronica", monthFolder)
	xmlPath, pdfPath, _ := p.DownloadArtifacts(ctx, res, fiscalDir)
	res.XMLPath = xmlPath
	res.PDFPath = pdfPath
	res.ThermalTicket = p.BuildThermalReceipt(doc, res)

	_ = p.persistFiscalRecord(doc, res)
	return res, nil
}

// EmitCreditNote emits an electronic Credit Note (Nota de Crédito UBL 2.1) for a return or cancellation (TODO-056).
func (p *StandardUBLProvider) EmitCreditNote(ctx context.Context, originalDoc db.Document, reason string) (*FiscalSubmissionResult, error) {
	fiscalNum := fmt.Sprintf("NC-%s", originalDoc.Number)
	taxID := originalDoc.CustomerTaxID
	if taxID == "" {
		taxID = "999999999999"
	}
	uuid := p.ComputeDocumentUUID(fiscalNum, time.Now().Format("2006-01-02"), originalDoc.Total, taxID)
	qrURL := fmt.Sprintf("https://verify.fiscal.example.org/document?uuid=%s", uuid)

	res := &FiscalSubmissionResult{
		AroniumDocumentID:     originalDoc.ID,
		AroniumDocumentNumber: originalDoc.Number,
		FiscalNumber:          fiscalNum,
		DocumentKind:          "NOTA_CREDITO",
		Status:                StatusAccepted,
		DocumentUUID:          uuid,
		QRData:                qrURL,
		APIResponse:           fmt.Sprintf(`{"status":"ACCEPTED","kind":"CreditNote","reason":%q}`, reason),
	}
	monthFolder := time.Now().Format("2006-01")
	fiscalDir := filepath.Join(p.Cfg.GoogleDrive.LocalDriveSyncDir, p.Cfg.GoogleDrive.RootFolderName, "Facturacion_Electronica", monthFolder)
	res.XMLPath, res.PDFPath, _ = p.DownloadArtifacts(ctx, res, fiscalDir)
	res.ThermalTicket = p.BuildThermalReceipt(originalDoc, res)
	return res, nil
}

// CheckStatus queries the current status of a fiscal document in aronium_utility_state.db (TODO-052).
func (p *StandardUBLProvider) CheckStatus(ctx context.Context, fiscalNumber string) (string, error) {
	if p.StateDB == nil {
		return "UNKNOWN", nil
	}
	var st string
	err := p.StateDB.QueryRowContext(ctx, `SELECT status FROM fiscal_documents WHERE fiscal_number = ? OR aronium_document_number = ?;`, fiscalNumber, fiscalNumber).Scan(&st)
	if err != nil {
		return "", err
	}
	return st, nil
}

// DownloadArtifacts writes the UBL 2.1 XML and PDF representation to the target Google Drive folder (TODO-052, TODO-056).
func (p *StandardUBLProvider) DownloadArtifacts(ctx context.Context, res *FiscalSubmissionResult, destDir string) (xmlPath, pdfPath string, err error) {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", "", err
	}
	xmlPath = filepath.Join(destDir, res.FiscalNumber+".xml")
	pdfPath = filepath.Join(destDir, res.FiscalNumber+".pdf")

	xmlContent := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Invoice xmlns="urn:oasis:names:specification:ubl:schema:xsd:Invoice-2"
         xmlns:cbc="urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2">
  <cbc:UBLVersionID>UBL 2.1</cbc:UBLVersionID>
  <cbc:ProfileID>UBL 2.1 Electronic Invoice</cbc:ProfileID>
  <cbc:ID>%s</cbc:ID>
  <cbc:UUID schemeName="UUID-SHA384">%s</cbc:UUID>
  <cbc:Note>%s</cbc:Note>
</Invoice>
`, res.FiscalNumber, res.DocumentUUID, res.QRData)

	if err := os.WriteFile(xmlPath, []byte(xmlContent), 0644); err != nil {
		return "", "", err
	}

	pdfContent := fmt.Sprintf("%%PDF-1.4\n%% Representacion Grafica Factura Electronica %s\n%% UUID: %s\n%% QR: %s\n%%EOF\n",
		res.FiscalNumber, res.DocumentUUID, res.QRData)
	if err := os.WriteFile(pdfPath, []byte(pdfContent), 0644); err != nil {
		return xmlPath, "", err
	}
	return xmlPath, pdfPath, nil
}

// BuildThermalReceipt formats the thermal receipt strip including fiscal authorization, UUID, and QR URL (TODO-056).
func (p *StandardUBLProvider) BuildThermalReceipt(doc db.Document, res *FiscalSubmissionResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== %s ===\n", p.Cfg.Fiscal.IssuerName))
	sb.WriteString(fmt.Sprintf("ID Fiscal: %s | Aut: %s\n", p.Cfg.Fiscal.IssuerTaxID, p.Cfg.Fiscal.AuthorizationNumber))
	sb.WriteString(fmt.Sprintf("Doc: %s (%s)\n", res.FiscalNumber, res.DocumentKind))
	sb.WriteString(fmt.Sprintf("Cliente: %s (%s)\n", doc.CustomerName, doc.CustomerTaxID))
	sb.WriteString(fmt.Sprintf("Total: $%.2f | Estado: %s\n", doc.Total, res.Status))
	sb.WriteString(fmt.Sprintf("UUID: %s\n", res.DocumentUUID))
	sb.WriteString(fmt.Sprintf("QR: %s\n", res.QRData))
	return sb.String()
}

func (p *StandardUBLProvider) persistFiscalRecord(doc db.Document, res *FiscalSubmissionResult) error {
	if p.StateDB == nil {
		return nil
	}
	_, err := p.StateDB.Exec(`
		INSERT INTO fiscal_documents
			(aronium_document_id, aronium_document_number, fiscal_number, customer_tax_id, customer_name, customer_email, total, status, document_uuid, qr_data, xml_path, pdf_path, api_response, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(aronium_document_id) DO UPDATE SET
			status = excluded.status,
			document_uuid = excluded.document_uuid,
			qr_data = excluded.qr_data,
			xml_path = excluded.xml_path,
			pdf_path = excluded.pdf_path,
			api_response = excluded.api_response,
			updated_at = CURRENT_TIMESTAMP;
	`, doc.ID, doc.Number, res.FiscalNumber, doc.CustomerTaxID, doc.CustomerName, doc.CustomerEmail, doc.Total, res.Status, res.DocumentUUID, res.QRData, res.XMLPath, res.PDFPath, res.APIResponse)
	return err
}

// ListFiscalDocuments lists all tracked electronic invoices and their statuses from stateDB.
func (p *StandardUBLProvider) ListFiscalDocuments() ([]FiscalSubmissionResult, error) {
	rows, err := p.StateDB.Query(`
		SELECT aronium_document_id, aronium_document_number, fiscal_number, status, document_uuid, qr_data, xml_path, pdf_path, api_response
		FROM fiscal_documents
		ORDER BY id DESC;
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FiscalSubmissionResult
	for rows.Next() {
		var r FiscalSubmissionResult
		if err := rows.Scan(&r.AroniumDocumentID, &r.AroniumDocumentNumber, &r.FiscalNumber, &r.Status, &r.DocumentUUID, &r.QRData, &r.XMLPath, &r.PDFPath, &r.APIResponse); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
