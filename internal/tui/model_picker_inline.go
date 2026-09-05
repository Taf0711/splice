package tui

import (
	"math"
	"strings"

	"github.com/Taf0711/splice/internal/modelregistry"
)

// This file implements the inline controls on the /model picker: each row owns
// its reasoning-effort ring (adjusted in place with ←/→), its long-context
// toggle (Tab), and a price readout that updates as the cursor moves. Choosing a
// row commits the model AND the effort it was left on, so picking "this model,
// thinking harder" is one pass through one surface instead of /model followed by
// /effort.

// enrichModelPickerItem fills in the registry-backed fields the inline controls
// render from. It is applied to every model row regardless of which path built
// it (curated registry, provider catalog, or live discovery), so a discovered
// row still gets its effort ring and pricing when the catalog knows the ID.
// Rows the registry cannot resolve keep zero values and degrade to a plain
// label — no effort ring, no price, which is the honest rendering for a custom
// endpoint or a local Ollama tag.
func (m model) enrichModelPickerItem(item pickerItem) pickerItem {
	entry, ok := m.modelCatalog.Resolve(strings.TrimSpace(item.Value))
	if !ok {
		return item
	}
	item.Efforts = append([]modelregistry.ReasoningEffort{}, m.modelCatalog.ReasoningEfforts(entry.ID)...)
	item.EffortIndex = defaultEffortIndex(item.Efforts, entry.DefaultReasoningEffort)
	if cost := (pickerCost{
		InputPerMillion:       entry.Cost.InputPerMillion,
		CachedInputPerMillion: entry.Cost.CachedInputPerMillion,
		OutputPerMillion:      entry.Cost.OutputPerMillion,
	}); entry.Cost.Source != "" || !cost.free() {
		// A priced entry, or one whose zero rates are explicitly sourced (a genuinely
		// free model) — both are renderable. An unsourced all-zero cost is missing
		// data, not free, so it stays nil and renders as unknown.
		item.Cost = &cost
	}
	switch entry.Status {
	case modelregistry.ModelStatusPreview:
		item.Badge = badgeBeta
	case modelregistry.ModelStatusDeprecated:
		item.Badge = badgeDeprecated
	}
	item.LongContext = entry.Supports(modelregistry.ModelCapabilityLongContext)
	return item
}

// defaultEffortIndex picks the ring position a row opens on: the model's declared
// default effort when it has one, else auto. Starting on the model's own default
// means ←/→ adjusts relative to what you'd actually get, not an arbitrary tier.
func defaultEffortIndex(efforts []modelregistry.ReasoningEffort, def modelregistry.ReasoningEffort) int {
	if def == "" {
		return effortAuto
	}
	for index, effort := range efforts {
		if effort == def {
			return index
		}
	}
	return effortAuto
}

// activeModelEffortIndex overrides a row's ring position with the session's
// current effort, for the row that IS the active model. Without this, reopening
// /model would show the model's catalog default rather than the effort actually
// in force, and a stray ←/→ would silently revert a deliberate /effort choice.
func (m model) activeModelEffortIndex(item pickerItem) int {
	if m.reasoningEffort == "" {
		return effortAuto
	}
	for index, effort := range item.Efforts {
		if effort == m.reasoningEffort {
			return index
		}
	}
	return item.EffortIndex
}

// adjustModelPickerEffort steps the highlighted row's effort ring by delta and
// reports whether anything moved. The ring is clamped, not wrapped: ← at auto
// and → at the top tier are no-ops, so holding an arrow settles at an end
// instead of silently cycling back through every tier.
//
// The change is written to both items and allItems so it survives the query
// filter — typing to narrow the list and backspacing to widen it must not
// discard an effort the user just dialed in.
func (m model) adjustModelPickerEffort(delta int) (model, bool) {
	if m.picker == nil || m.picker.kind != pickerModel {
		return m, false
	}
	item, ok := m.picker.current()
	if !ok || len(item.Efforts) == 0 {
		return m, false
	}
	next := clampInt(item.EffortIndex+delta, effortAuto, len(item.Efforts)-1)
	if next == item.EffortIndex {
		return m, false
	}
	m.picker.setCurrentEffort(next)
	return m, true
}

// toggleModelPickerContext flips the highlighted row's long-context request.
// Only rows advertising the capability respond, which is why the Tab hint is
// rendered conditionally — an inert key with a visible label would be worse than
// no label.
func (m model) toggleModelPickerContext() (model, bool) {
	if m.picker == nil || m.picker.kind != pickerModel {
		return m, false
	}
	item, ok := m.picker.current()
	if !ok || !item.LongContext {
		return m, false
	}
	m.picker.setCurrentLongContext(!item.LongContextOn)
	return m, true
}

// setCurrentEffort writes an effort index to the highlighted row in both the
// filtered view and the unfiltered backing list, matched by Value.
func (p *commandPicker) setCurrentEffort(index int) {
	p.mutateCurrent(func(item *pickerItem) { item.EffortIndex = index })
}

func (p *commandPicker) setCurrentLongContext(on bool) {
	p.mutateCurrent(func(item *pickerItem) { item.LongContextOn = on })
}

// mutateCurrent applies fn to the highlighted row and to its twin in allItems,
// keeping the filtered and unfiltered lists in agreement.
func (p *commandPicker) mutateCurrent(fn func(*pickerItem)) {
	if p.selected < 0 || p.selected >= len(p.items) {
		return
	}
	fn(&p.items[p.selected])
	value := p.items[p.selected].Value
	for index := range p.allItems {
		if p.allItems[index].Value == value {
			fn(&p.allItems[index])
		}
	}
}

// selectedEffort resolves the highlighted row's ring position to the effort
// string handleEffortCommand takes: "" for auto, else the tier name.
func (item pickerItem) selectedEffort() string {
	if item.EffortIndex == effortAuto || item.EffortIndex >= len(item.Efforts) {
		return ""
	}
	return string(item.Efforts[item.EffortIndex])
}

// effortLabel is the ring's right-hand readout.
func (item pickerItem) effortLabel() string {
	if effort := item.selectedEffort(); effort != "" {
		return effort
	}
	return "auto"
}

// blendedRate collapses a model's rates into one number for positioning it on
// the shared cost rail. Agent turns re-send a large transcript against a much
// smaller completion, so input dominates spend — the 3:1 input:output weighting
// reflects that. It is a comparison aid for ordering models on one axis, not a
// spend estimate; the exact per-million rates are printed beneath it.
func (c pickerCost) blendedRate() float64 {
	return 0.75*c.InputPerMillion + 0.25*c.OutputPerMillion
}

// costRailPosition maps a blended rate onto 0..1 across the range of priced
// models in the picker. Rates span orders of magnitude ($0.07 to $30+), so a
// linear scale would crush every affordable model into the first character cell
// — the log scale keeps the cheap end legible, which is where the meaningful
// choices cluster.
func costRailPosition(rate, low, high float64) float64 {
	if rate <= 0 || low <= 0 || high <= low {
		return 0
	}
	position := (math.Log(rate) - math.Log(low)) / (math.Log(high) - math.Log(low))
	return math.Max(0, math.Min(1, position))
}

// costRailBounds returns the cheapest and priciest blended rates among the
// picker's priced rows, which anchor the rail. Rows without pricing are skipped
// so an unknown-cost custom model cannot drag the scale to zero.
func costRailBounds(items []pickerItem) (low, high float64) {
	for _, item := range items {
		if item.Cost == nil {
			continue
		}
		rate := item.Cost.blendedRate()
		if rate <= 0 {
			continue
		}
		if low == 0 || rate < low {
			low = rate
		}
		if rate > high {
			high = rate
		}
	}
	return low, high
}

// applyPickedModelEffort commits the effort the chosen /model row was left on.
// It runs AFTER the model switch so it validates against the newly active
// model's supported ring.
//
// Silence is deliberate here: the row's effort was visible on screen at the
// moment of Enter, and handleModelCommand already emits a card for the switch.
// A second "reasoning effort set to high" line for something the user watched
// themselves set would be noise. A row left on auto writes nothing at all —
// handleModelCommand's own effort reconciliation stays in charge of that case.
func (m model) applyPickedModelEffort(item pickerItem) model {
	effort := item.selectedEffort()
	if effort == "" {
		return m
	}
	// The model switch can be refused (no provider profile for the target, an
	// unresolvable ID). The effort was dialed in for the model on that ROW, so
	// applying it after a refused switch would retune whatever model is still
	// active — a change the user never asked for and did not see. Confirm the
	// switch actually landed before touching effort.
	if strings.TrimSpace(m.modelName) != strings.TrimSpace(item.Value) {
		return m
	}
	requested := modelregistry.ReasoningEffort(effort)
	// Only accept an effort the now-active model actually supports. The ring was
	// built from the catalog, but a provider switch can land on a different entry
	// for the same ID, so re-check rather than trusting the row.
	if !reasoningEffortAllowed(m.availableReasoningEfforts(), requested) {
		return m
	}
	m.reasoningEffort = requested
	return m
}
