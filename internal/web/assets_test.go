package web

import (
	"io/fs"
	"strings"
	"testing"
)

// assetBudget is the total size limit for embedded assets. Plan section 7
// set 100 KiB. The lineage view (edge labels, path tracing, and the live run
// in the editor) raised it to 128 KiB, and the editor's live edge routing
// (routeFree) to 134 KiB. The assets are not minified.
const assetBudget = 134 * 1024

func TestEmbeddedAssetsWithinBudget(t *testing.T) {
	var total int64
	err := fs.WalkDir(assetFS, "assets", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		t.Fatalf("walk assets: %v", err)
	}
	if total == 0 || total >= assetBudget {
		t.Fatalf("embedded assets = %d bytes, want 0 < size < %d", total, assetBudget)
	}
}

func TestIndexPageHasNoScriptOrExternalReference(t *testing.T) {
	page, err := assetFS.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	for _, banned := range []string{"<script", "http://", "https://"} {
		if strings.Contains(strings.ToLower(string(page)), banned) {
			t.Errorf("index page contains %q; the surface must have no JS or external requests", banned)
		}
	}
}
