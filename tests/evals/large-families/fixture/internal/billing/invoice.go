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
