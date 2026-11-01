package config

// The service catalog. Every kind that a subsystem can emit must be listed
// here: operators enable and route kinds from these lists, and the server
// refuses a kind that is not listed.

// NotificationKinds lists the notification template kinds the service may send.
var NotificationKinds = []string{
	"password_reset",
}

// WebhookEvents lists the webhook event types the service may deliver.
var WebhookEvents = []string{
	"invoice.paid",
}
