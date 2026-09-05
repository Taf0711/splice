package billing

import "fmt"

// DunningLevel tracks how many reminders an account has received.
type DunningLevel int

const (
    LevelNone DunningLevel = iota
    LevelFirst
    LevelFinal
    LevelSuspended
)

// Next advances the dunning level.
func (d DunningLevel) Next() DunningLevel {
    if d >= LevelSuspended {
        return d
    }
    return d + 1
}

// Describe renders the level for the admin UI.
func (d DunningLevel) Describe() string {
    switch d {
    case LevelNone:
        return "in good standing"
    case LevelFinal:
        return "final notice"
    case LevelSuspended:
        return "suspended"
    default:
        return fmt.Sprintf("reminder %d", int(d))
    }
}
