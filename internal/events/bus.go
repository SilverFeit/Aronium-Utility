package events

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/SilverFeit/Aronium-Utility/internal/config"
	"github.com/SilverFeit/Aronium-Utility/internal/db"
)

// EventType enumerates all typed domain events emitted by Aronium Utility (TODO-015).
type EventType string

const (
	EventSaleCreated           EventType = "SaleCreated"
	EventCreditAccountUpdated  EventType = "CreditAccountUpdated"
	EventPaymentRecorded       EventType = "PaymentRecorded"
	EventStockChanged          EventType = "StockChanged"
	EventProductCatalogChanged EventType = "ProductCatalogChanged"
	EventCustomerUpdated       EventType = "CustomerUpdated"
	EventLowStockAlert         EventType = "LowStockAlert"
	EventDayClose              EventType = "DayClose"
)

// Event carries a typed domain event across the internal Event Bus.
type Event struct {
	ID             string         `json:"id"`
	Type           EventType      `json:"type"`
	StoreID        string         `json:"store_id"`
	CashRegisterID string         `json:"cash_register_id"`
	TimestampUTC   time.Time      `json:"timestamp_utc"`
	Document       *db.Document   `json:"document,omitempty"`
	Payload        map[string]any `json:"payload,omitempty"`
}

type Subscriber func(ctx context.Context, ev Event) error

// Bus coordinates asynchronous and synchronous event subscribers (TODO-015).
type Bus struct {
	mu          sync.RWMutex
	subscribers map[EventType][]Subscriber
	allSubs     []Subscriber
	hooks       config.HooksConfig
}

func NewBus(hooks config.HooksConfig) *Bus {
	return &Bus{
		subscribers: make(map[EventType][]Subscriber),
		hooks:       hooks,
	}
}

func (b *Bus) Subscribe(eventType EventType, sub Subscriber) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subscribers[eventType] = append(b.subscribers[eventType], sub)
}

func (b *Bus) SubscribeAll(sub Subscriber) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.allSubs = append(b.allSubs, sub)
}

// Publish dispatches an event to all registered subscribers and configured hooks (TODO-015, TODO-046).
func (b *Bus) Publish(ctx context.Context, ev Event) []error {
	if ev.TimestampUTC.IsZero() {
		ev.TimestampUTC = time.Now().UTC()
	}
	b.mu.RLock()
	subs := append([]Subscriber{}, b.subscribers[ev.Type]...)
	subs = append(subs, b.allSubs...)
	b.mu.RUnlock()

	var errs []error
	var errMu sync.Mutex
	var wg sync.WaitGroup

	for _, s := range subs {
		wg.Add(1)
		go func(fn Subscriber) {
			defer wg.Done()
			if err := fn(ctx, ev); err != nil {
				errMu.Lock()
				errs = append(errs, err)
				errMu.Unlock()
			}
		}(s)
	}

	// Execute configured external webhooks / scripts (TODO-046)
	hookTargets := b.hooksForEvent(ev.Type)
	for _, target := range hookTargets {
		wg.Add(1)
		go func(t string) {
			defer wg.Done()
			if err := executeHook(ctx, t, ev); err != nil {
				errMu.Lock()
				errs = append(errs, err)
				errMu.Unlock()
			}
		}(target)
	}

	wg.Wait()
	return errs
}

func (b *Bus) hooksForEvent(t EventType) []string {
	switch t {
	case EventSaleCreated:
		return b.hooks.OnSale
	case EventCreditAccountUpdated:
		return b.hooks.OnCreditAccount
	case EventLowStockAlert, EventStockChanged:
		return b.hooks.OnLowStock
	case EventDayClose:
		return b.hooks.OnDayClose
	default:
		return nil
	}
}

func executeHook(ctx context.Context, target string, ev Event) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil
	}
	raw, _ := json.Marshal(ev)
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(raw))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Aronium-Event", string(ev.Type))
		client := &http.Client{Timeout: 8 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return fmt.Errorf("webhook %s returned HTTP %d", target, resp.StatusCode)
		}
		return nil
	}
	// External script execution
	cmd := exec.CommandContext(ctx, target, string(ev.Type))
	cmd.Stdin = bytes.NewReader(raw)
	return cmd.Run()
}

// Watcher monitors pos.db, pos.db-wal, and pos.db-shm with debounce and polling fallback (TODO-014).
type Watcher struct {
	PosDBPath    string
	PosDB        *sql.DB
	StateDB      *sql.DB
	StoreID      string
	RegisterID   string
	PollInterval time.Duration
	Debounce     time.Duration
	CreditTypes  map[string]bool
	Bus          *Bus
}

func NewWatcher(cfg *config.Config, posDB, stateDB *sql.DB, bus *Bus) *Watcher {
	poll := time.Duration(cfg.Watch.PollIntervalMs) * time.Millisecond
	if poll < 250*time.Millisecond {
		poll = 2 * time.Second
	}
	deb := time.Duration(cfg.Watch.DebounceMs) * time.Millisecond
	if deb < 50*time.Millisecond {
		deb = 500 * time.Millisecond
	}
	ct := make(map[string]bool)
	for _, t := range cfg.Credit.CreditPaymentTypes {
		ct[strings.ToLower(strings.TrimSpace(t))] = true
	}
	return &Watcher{
		PosDBPath:    cfg.PosDBPath,
		PosDB:        posDB,
		StateDB:      stateDB,
		StoreID:      cfg.StoreID,
		RegisterID:   cfg.CashRegisterID,
		PollInterval: poll,
		Debounce:     deb,
		CreditTypes:  ct,
		Bus:          bus,
	}
}

// PollOnce checks pos.db against the saved checkpoint in aronium_utility_state.db and emits events for all deltas.
func (w *Watcher) PollOnce(ctx context.Context) (int, error) {
	prev, err := db.GetCheckpoint(w.StateDB)
	if err != nil {
		return 0, err
	}
	curr, err := db.ComputeDatabaseSnapshot(w.PosDB)
	if err != nil {
		return 0, err
	}

	emitted := 0

	// 1. Check new Documents
	if curr.LastDocumentID > prev.LastDocumentID {
		docs, err := db.ListDocuments(w.PosDB, prev.LastDocumentID, w.RegisterID)
		if err != nil {
			return emitted, err
		}
		for i := range docs {
			doc := docs[i]
			evSale := Event{
				ID:             fmt.Sprintf("doc-%d-%d", doc.ID, time.Now().UnixNano()),
				Type:           EventSaleCreated,
				StoreID:        w.StoreID,
				CashRegisterID: w.RegisterID,
				TimestampUTC:   time.Now().UTC(),
				Document:       &doc,
			}
			w.Bus.Publish(ctx, evSale)
			emitted++

			// Check if document is a customer credit account transaction (unpaid or credit payment type or split payment balance > 0)
			if w.isDocumentCredit(doc) {
				evCredit := Event{
					ID:             fmt.Sprintf("credit-%d-%d", doc.ID, time.Now().UnixNano()),
					Type:           EventCreditAccountUpdated,
					StoreID:        w.StoreID,
					CashRegisterID: w.RegisterID,
					TimestampUTC:   time.Now().UTC(),
					Document:       &doc,
				}
				w.Bus.Publish(ctx, evCredit)
				emitted++
			}
		}
	}

	// 2. Check new Payments (abonos / partial settlements)
	if curr.LastPaymentID > prev.LastPaymentID {
		evPay := Event{
			ID:             fmt.Sprintf("pay-%d", curr.LastPaymentID),
			Type:           EventPaymentRecorded,
			StoreID:        w.StoreID,
			CashRegisterID: w.RegisterID,
			TimestampUTC:   time.Now().UTC(),
			Payload: map[string]any{
				"prev_payment_id": prev.LastPaymentID,
				"last_payment_id": curr.LastPaymentID,
			},
		}
		w.Bus.Publish(ctx, evPay)
		emitted++
	}

	// 3. Check Stock changes
	if curr.StockHash != "" && curr.StockHash != prev.StockHash {
		evStock := Event{
			ID:             fmt.Sprintf("stock-%s", curr.StockHash),
			Type:           EventStockChanged,
			StoreID:        w.StoreID,
			CashRegisterID: w.RegisterID,
			TimestampUTC:   time.Now().UTC(),
			Payload: map[string]any{
				"stock_hash": curr.StockHash,
			},
		}
		w.Bus.Publish(ctx, evStock)
		emitted++
	}

	// 4. Check Catalog changes
	if curr.CatalogHash != "" && curr.CatalogHash != prev.CatalogHash {
		evCat := Event{
			ID:             fmt.Sprintf("cat-%s", curr.CatalogHash),
			Type:           EventProductCatalogChanged,
			StoreID:        w.StoreID,
			CashRegisterID: w.RegisterID,
			TimestampUTC:   time.Now().UTC(),
			Payload: map[string]any{
				"catalog_hash": curr.CatalogHash,
			},
		}
		w.Bus.Publish(ctx, evCat)
		emitted++
	}

	if emitted > 0 || prev.StockHash == "" {
		if err := db.SaveCheckpoint(w.StateDB, curr); err != nil {
			return emitted, err
		}
	}
	return emitted, nil
}

func (w *Watcher) isDocumentCredit(doc db.Document) bool {
	if doc.PaidStatus == 0 {
		return true
	}
	var effectivePaid float64
	for _, p := range doc.Payments {
		nameLower := strings.ToLower(strings.TrimSpace(p.PaymentTypeName))
		if w.CreditTypes[nameLower] || !p.MarkAsPaid || strings.Contains(nameLower, "crédito") || strings.Contains(nameLower, "credito") || strings.Contains(nameLower, "credit") || strings.Contains(nameLower, "cuenta") {
			return true
		}
		effectivePaid += p.Amount
	}
	return doc.Total > 0 && effectivePaid+0.01 < doc.Total
}

// Run starts the continuous file-system mtime + polling watcher with debounce until ctx is canceled (TODO-014).
func (w *Watcher) Run(ctx context.Context) error {
	watchedFiles := []string{
		w.PosDBPath,
		w.PosDBPath + "-wal",
		w.PosDBPath + "-shm",
	}
	lastMod := w.maxModTime(watchedFiles)

	fsTicker := time.NewTicker(250 * time.Millisecond)
	defer fsTicker.Stop()
	pollTicker := time.NewTicker(w.PollInterval)
	defer pollTicker.Stop()

	// Initial poll on startup
	_, _ = w.PollOnce(ctx)

	var debounceTimer *time.Timer
	var debounceCh <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-fsTicker.C:
			mod := w.maxModTime(watchedFiles)
			if mod.After(lastMod) {
				lastMod = mod
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				debounceTimer = time.NewTimer(w.Debounce)
				debounceCh = debounceTimer.C
			}
		case <-debounceCh:
			debounceCh = nil
			_, _ = w.PollOnce(ctx)
		case <-pollTicker.C:
			_, _ = w.PollOnce(ctx)
		}
	}
}

func (w *Watcher) maxModTime(paths []string) time.Time {
	var maxT time.Time
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil {
			if st.ModTime().After(maxT) {
				maxT = st.ModTime()
			}
		}
	}
	return maxT
}
