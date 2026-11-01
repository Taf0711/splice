package web

import (
	"encoding/json"
	"fmt"

	"github.com/Taf0711/splice/internal/agent"
)

// actionPlaceholder is the text in assets/index.html that the server replaces
// with the current action token.
const actionPlaceholder = "__SPLICE_ACTION__"

// StageFrame is the JSON payload for one run event on the surface stream. It is
// marshalled once per event, and every client receives the same bytes.
//
//   - kind: "stage" for a stage event, "notice" for a notice line, or "run" for a run start or end.
//   - name: the stage name. Empty for notice and run events.
//   - iteration: the 0-based pipeline pass for the stage.
//   - status: the stage status, or the run status ("running", "completed", "failed").
//   - detail: the redacted detail text, or the notice text.
//   - progress: the stage progress, 0 to 100.
//   - changed_files: the paths a completed stage changed. Never null.
type StageFrame struct {
	Kind         string   `json:"kind"`
	Name         string   `json:"name"`
	Iteration    int      `json:"iteration"`
	Status       string   `json:"status"`
	Detail       string   `json:"detail"`
	Progress     int      `json:"progress"`
	ChangedFiles []string `json:"changed_files"`
}

// sessionFrame is the payload of the session-changed event.
type sessionFrame struct {
	Session string `json:"session"`
}

// Sink returns the event sink for a pipeline run. Each event becomes one
// StageFrame. A stage event keys on "<name>/<iteration>", so the latest status of
// a stage wins within one flush. A notice and a run event each get a key that
// never merges with another event.
func (s *Server) Sink() func(agent.StageEvent) {
	epoch := s.hub.currentEpoch()
	return func(event agent.StageEvent) {
		frame := StageFrame{
			Kind:         event.Kind,
			Name:         event.Name,
			Iteration:    event.Iteration,
			Status:       event.Status,
			Detail:       event.Detail,
			Progress:     event.Progress,
			ChangedFiles: append([]string{}, event.ChangedFiles...),
		}
		key := fmt.Sprintf("%s/%d", event.Name, event.Iteration)
		switch event.Kind {
		case "":
			frame.Kind = "stage"
		case "notice":
			key = fmt.Sprintf("notice/%d", s.notices.Add(1))
		case "run":
			key = "run/" + event.Status
		}
		data, err := json.Marshal(frame)
		if err != nil {
			return
		}
		s.hub.publishFrom(epoch, Event{Key: key, Data: string(data)})
	}
}
