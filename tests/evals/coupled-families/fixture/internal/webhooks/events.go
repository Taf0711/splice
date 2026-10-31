package webhooks

// Events maps each webhook event type to its human description.
var Events = map[string]string{
	"invoice.paid": "An invoice was paid",
}

// Describe returns the description of one event type.
func Describe(event string) (string, bool) {
	d, ok := Events[event]
	return d, ok
}
