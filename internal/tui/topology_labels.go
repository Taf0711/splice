package tui

import splicerun "github.com/Taf0711/splice/internal/splice"

// stageTierLabels enumerates the model-backed stages of the active topology, so
// a custom topology's model-backed nodes appear in the model wizard instead of
// only the builtin roster. A malformed config falls back to the embedded
// default labels here; the run itself fails loud on the same file, so the
// fallback never hides an error from the user. The wizard reads the error
// through stageTierLabelsWithError and shows it.
func (m model) stageTierLabels() map[string]string {
	labels, _ := m.stageTierLabelsWithError()
	return labels
}

// stageTierLabelsWithError is stageTierLabels plus the topology error. On an
// error the labels are the default stages, and the error is non-nil.
func (m model) stageTierLabelsWithError() (map[string]string, error) {
	topology, _, _, err := splicerun.ResolveTopology(splicerun.TopologySourcesFor(m.cwd, m.trusted))
	if err != nil {
		return splicerun.StageTierLabels(), err
	}
	return splicerun.StageTierLabelsFor(topology), nil
}
