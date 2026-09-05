package billing

import "time"

// Prorate computes the charge for a partial month.
func Prorate(monthlyCents int64, from, until time.Time) int64 {
    days := until.Sub(from).Hours() / 24
    if days < 0 {
        days = 0
    }
    return int64(float64(monthlyCents) * days / 30.0)
}
