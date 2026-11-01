package splice

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func writeDecisionRecords(dir, sessionID string, records []schemas.DecisionDispositionRecord) (err error) {
	if sessionID == "" || filepath.Base(sessionID) != sessionID || sessionID == "." || sessionID == ".." {
		return fmt.Errorf("invalid decision record session id %q", sessionID)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create decision record directory %q: %w", dir, err)
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("open decision records %q: %w", path, err)
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close decision records %q: %w", path, closeErr)
		}
	}()
	if err := file.Chmod(0600); err != nil {
		return fmt.Errorf("protect decision records %q: %w", path, err)
	}
	encoder := json.NewEncoder(file)
	for i, record := range records {
		if err := encoder.Encode(record); err != nil {
			return fmt.Errorf("write decision record %d to %q: %w", i, path, err)
		}
	}
	return nil
}
