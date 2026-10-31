package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
)

// model_catalog_refresh.go keeps the model catalog (names, context windows,
// prices, and each model's supported effort levels) current while Splice
// runs. Startup refreshes a stale models.dev cache in the background; Ctrl+R
// in /model forces a refresh. Either way, a newly written cache rebuilds the
// catalog in place, so new models and their effort levels appear without a
// restart.

type modelCatalogRefreshedMsg struct {
	written bool
	forced  bool
	err     error
}

func (m model) modelCatalogRefreshCmd(force bool) tea.Cmd {
	refresh := m.refreshModelCatalog
	if refresh == nil {
		return nil
	}
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		written, err := refresh(ctx, force)
		return modelCatalogRefreshedMsg{written: written, forced: force, err: err}
	}
}

func (m model) applyModelCatalogRefresh(msg modelCatalogRefreshedMsg) (model, tea.Cmd) {
	if msg.written {
		m.rebuildModelCatalog()
		// An open /model picker re-reads the catalog so new rows and their
		// effort rings show now, not on the next open.
		if m.picker != nil && m.picker.kind == pickerModel {
			if picker := m.newModelPicker(); picker != nil {
				picker.query = m.picker.query
				picker.applyQuery()
				m.picker = picker
			}
		}
	}
	if !msg.forced {
		return m, nil
	}
	switch {
	case msg.err != nil:
		return m.appendSystemNotice("Model catalog refresh failed: " + msg.err.Error()), nil
	case msg.written:
		return m.appendSystemNotice("Model catalog refreshed from models.dev."), nil
	default:
		return m.appendSystemNotice("Model catalog refresh is disabled (SPLICE_DISABLE_MODELS_FETCH)."), nil
	}
}
