package billing

// Plan is one subscription tier.
type Plan struct {
    Name        string
    MonthlyCents int64
    Seats       int
}

// Catalog lists the available subscription plans.
var Catalog = []Plan{
    {Name: "free", MonthlyCents: 0, Seats: 1},
    {Name: "team", MonthlyCents: 2900, Seats: 10},
    {Name: "scale", MonthlyCents: 9900, Seats: 100},
}

// Lookup returns the plan by name.
func Lookup(name string) (Plan, bool) {
    for _, p := range Catalog {
        if p.Name == name {
            return p, true
        }
    }
    return Plan{}, false
}
