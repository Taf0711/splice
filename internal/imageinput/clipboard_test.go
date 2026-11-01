package imageinput

import (
	"reflect"
	"runtime"
	"testing"

	"github.com/Taf0711/splice/internal/sandbox/procrun"
)

func TestImageMIMETypesFiltersAndPreservesRaw(t *testing.T) {
	// Only image/* lines are kept, trimmed. Crucially, a hostile/odd type is
	// returned VERBATIM (not sanitized) — safety comes from never passing it
	// through a shell, so the filter must not silently mangle a real type.
	list := []byte("text/plain\nimage/png\n  image/jpeg  \nTARGETS\n" +
		"image/png; rm -rf ~\n")
	got := imageMIMETypes(list)
	want := []string{"image/png", "image/jpeg", "image/png; rm -rf ~"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("imageMIMETypes = %#v, want %#v", got, want)
	}
}

func TestImageMIMETypesEmpty(t *testing.T) {
	if got := imageMIMETypes([]byte("text/plain\nTARGETS\n")); got != nil {
		t.Fatalf("expected no image types, got %#v", got)
	}
}

// TestRunClipboardStdoutEmitsProcrunAuditRecord pins the pairing contract for
// the clipboard helper path: every helper spawn emits one audit record under
// the tui.imageinput profile, and a launch failure still emits its record.
func TestRunClipboardStdoutEmitsProcrunAuditRecord(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	var records []procrun.AuditRecord
	previous := procrun.SetAuditSink(func(record procrun.AuditRecord) {
		records = append(records, record)
	})
	t.Cleanup(func() { procrun.SetAuditSink(previous) })

	out, err := runClipboardStdout("sh", "-c", "printf clipboard-ok")
	if err != nil || string(out) != "clipboard-ok" {
		t.Fatalf("runClipboardStdout = (%q, %v), want clipboard-ok", out, err)
	}
	if len(records) != 1 {
		t.Fatalf("audit records = %d, want 1 per spawn", len(records))
	}
	record := records[0]
	if record.ProfileID != procrun.ProfileImageInput {
		t.Fatalf("profile = %q, want %q", record.ProfileID, procrun.ProfileImageInput)
	}
	if record.Name != "sh" || len(record.Args) != 2 || record.Args[0] != "-c" {
		t.Fatalf("record identity = %+v, want the spawned argv", record)
	}
	if record.Decision.Rejected {
		t.Fatalf("decision = %+v, want a non-rejected spawn", record.Decision)
	}

	// A missing binary fails at Run, not at Prepare, so the audit record for
	// the attempt must still exist.
	records = nil
	if _, err := runClipboardStdout("definitely-not-a-real-binary-zzz"); err == nil {
		t.Fatal("expected an error for a missing binary")
	}
	if len(records) != 1 || records[0].Name != "definitely-not-a-real-binary-zzz" {
		t.Fatalf("audit records = %+v, want one attempt record for the missing binary", records)
	}
}

func TestReadClipboardImage(t *testing.T) {
	// ReadClipboardImage either returns nil (no image) or valid image bytes.
	// On CI there's no image → nil. On a dev machine with a screenshot copied,
	// it returns real bytes. Both paths are valid — the test verifies whichever
	// one the clipboard produces.
	data, mediaType, err := ReadClipboardImage()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data == nil {
		// No image in clipboard — valid, mediaType must be empty.
		if mediaType != "" {
			t.Fatalf("expected empty media type when no image, got %q", mediaType)
		}
		return
	}
	// Image found — mediaType must be a supported type.
	validTypes := map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}
	if !validTypes[mediaType] {
		t.Errorf("mediaType = %q, want one of png/jpeg/gif/webp", mediaType)
	}
}
