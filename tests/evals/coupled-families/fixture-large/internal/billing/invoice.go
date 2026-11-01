// Package billing owns invoices, plans, and usage metering for demo
// accounts.
package billing

import (
	"errors"
	"fmt"
	"time"
)

var ErrInvoiceFinalized = errors.New("invoice already finalized")

// Invoice is one billing cycle for one account.
type Invoice struct {
	ID        string
	AccountID string
	Period    time.Time
	Lines     []Line
	Finalized bool
}

// Line is one charge on an invoice.
type Line struct {
	SKU      string
	Quantity int
	Cents    int64
}

// Service computes and stores invoices.
type Service struct {
	invoices map[string]Invoice
}

// NewService wires the billing service.
func NewService() *Service {
	return &Service{invoices: map[string]Invoice{}}
}

// Add appends a line item to the account's open invoice.
func (b *Service) Add(accountID, sku string, quantity int, cents int64) error {
	inv, ok := b.open(accountID)
	if !ok {
		return fmt.Errorf("no open invoice for account %s", accountID)
	}
	inv.Lines = append(inv.Lines, Line{SKU: sku, Quantity: quantity, Cents: cents})
	b.invoices[inv.ID] = inv
	return nil
}

// Finalize closes the open invoice for the account.
func (b *Service) Finalize(accountID string) error {
	inv, ok := b.open(accountID)
	if !ok {
		return fmt.Errorf("no open invoice for account %s", accountID)
	}
	if inv.Finalized {
		return ErrInvoiceFinalized
	}
	inv.Finalized = true
	b.invoices[inv.ID] = inv
	return nil
}

func (b *Service) open(accountID string) (Invoice, bool) {
	for _, inv := range b.invoices {
		if inv.AccountID == accountID && !inv.Finalized {
			return inv, true
		}
	}
	return Invoice{}, false
}

// Total returns the sum of one invoice line charges.
func (inv Invoice) Total() int64 {
	var sum int64
	for _, line := range inv.Lines {
		sum += int64(line.Quantity) * line.Cents
	}
	return sum
}

// invoiceNormalize0 drops empty entries and keeps the first occurrence of
// each value, in input order.
func invoiceNormalize0(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// invoiceWindow1 counts uses inside one period and refuses uses past the
// limit until the period resets.
type invoiceWindow1 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *invoiceWindow1) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *invoiceWindow1) reset() {
	w.used = 0
}

// invoiceClamp2 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func invoiceClamp2(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// invoiceDescribe3 renders a status code for operator logs.
func invoiceDescribe3(code int) string {
	switch {
	case code < 0:
		return "invalid"
	case code == 0:
		return "idle"
	case code < 10:
		return "active"
	case code < 100:
		return "degraded"
	default:
		return "saturated"
	}
}

// invoicePage4 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func invoicePage4(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// invoiceNormalize5 drops empty entries and keeps the first occurrence of
// each value, in input order.
func invoiceNormalize5(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// invoiceWindow6 counts uses inside one period and refuses uses past the
// limit until the period resets.
type invoiceWindow6 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *invoiceWindow6) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *invoiceWindow6) reset() {
	w.used = 0
}

// invoiceClamp7 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func invoiceClamp7(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
