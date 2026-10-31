package main

import (
	"slices"
	"testing"

	"demo/internal/config"
	"demo/internal/notifications"
	"demo/internal/webhooks"
	"demo/internal/worker"
)

func TestEveryNotificationKindIsInTheCatalog(t *testing.T) {
	for kind := range notifications.Templates {
		if !slices.Contains(config.NotificationKinds, kind) {
			t.Errorf("notification kind %q has a template but the service catalog does not list it", kind)
		}
	}
}

func TestEveryWebhookEventIsInTheCatalog(t *testing.T) {
	for event := range webhooks.Events {
		if !slices.Contains(config.WebhookEvents, event) {
			t.Errorf("webhook event %q is described but the service catalog does not list it", event)
		}
	}
}

func TestEveryScheduledJobHasAnOwner(t *testing.T) {
	for _, s := range worker.DefaultSchedules {
		if jobOwners[s.Name] == "" {
			t.Errorf("scheduled job %q has no owning team", s.Name)
		}
	}
}
