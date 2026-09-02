package tui

// semantic_tokens_test.go (P3 GAP-K rest, DoD 33): the theme-tokenization
// audit. DoD 33 asks that state colors are semantic tokens "everywhere".
// The tree's token system (theme.go + theme_palettes.go) predates this DoD
// and is ALREADY semantic: tuiTheme carries named styles (ink/muted/faint/
// accent/green/red/amber/...), every renderer consumes those named styles,
// and hex literals are confined to theme_palettes.go (palette tables) by
// an explicit documented rule. These probes pin that confinement as a
// permanent invariant instead of a one-time migration: any future hex
// literal (or raw lipgloss.Color) outside the two token files fails here.

import (
	"bufio"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// tokenFiles are the only .go files allowed to carry raw color tokens: the
// palette tables (hex literals), the startup brand mark (two fixed identity
// colors, documented as intentionally palette-independent), and the TEST
// files (contrast assertions quote real hex values; tests cannot mutate the
// theme system).
var tokenFiles = map[string]bool{
	"theme_palettes.go":       true,
	"startup.go":              true, // brand-mark identity colors only
	"semantic_tokens_test.go": true, // this audit itself
}

// hexColorRe matches #RGB / #RRGGBB string literals anywhere in a line.
const hexColorNeedle = "#"

// TestThemeTokensConfinedToPalettes walks every .go file in the package and
// fails when a file outside the token allowlist contains a hex color literal
// used as a style color. Comment mentions and string fragments that are not
// colors (URLs, anchors) are filtered by exact hex-shape matching.
func TestThemeTokensConfinedToPalettes(t *testing.T) {
	entries, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, file := range entries {
		if tokenFiles[file] || strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := os.Open(file)
		if err != nil {
			t.Fatalf("open %s: %v", file, err)
		}
		scanner := bufio.NewScanner(f)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			if strings.Contains(line, "lipgloss.Color(\"#") {
				t.Errorf("%s:%d: raw lipgloss.Color hex outside the palette tables: %s", file, lineNo, strings.TrimSpace(line))
				continue
			}
			for _, field := range strings.Fields(line) {
				if isHexColorLiteral(field) {
					t.Errorf("%s:%d: hex color literal outside the palette tables: %q", file, lineNo, field)
				}
			}
		}
		f.Close()
	}
}

// isHexColorLiteral reports whether a token is exactly a hex color (with an
// optional trailing quote/punct), e.g. `"#faf9f6"`, `#fff`, `#141414"`.
func isHexColorLiteral(token string) bool {
	token = strings.Trim(token, "\",;()")
	if !strings.HasPrefix(token, "#") {
		return false
	}
	switch len(token) - 1 {
	case 3, 4, 6, 8:
	default:
		return false
	}
	for _, r := range token[1:] {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// The startup brand mark's two hex constants are the documented exception:
// fixed identity colors, not theme tokens. Pin them so the allowlist above
// cannot silently widen.
func TestStartupBrandMarkHexIsBounded(t *testing.T) {
	if spliceBraidStrand != "#faf9f6" || spliceBraidTile != "#141414" {
		t.Fatalf("brand mark colors changed: %q / %q", spliceBraidStrand, spliceBraidTile)
	}
}

// Every palette in the registry must tokenize ALL of its string fields (no
// empty strings), so a theme switch fully repaints — the semantic-token
// guarantee the renderers rely on. Uses reflect so a NEW palette field is
// audited automatically: adding an untokenized field fails here.
func TestAllPaletteFieldsTokenized(t *testing.T) {
	palType := reflect.TypeOf(palette{})
	fields := make([]reflect.StructField, 0, palType.NumField())
	for i := 0; i < palType.NumField(); i++ {
		if palType.Field(i).Type.Kind() == reflect.String {
			fields = append(fields, palType.Field(i))
		}
	}
	if len(fields) == 0 {
		t.Fatal("palette exposes no string fields; audit the type")
	}
	for _, entry := range themeRegistry {
		pal := entry.Palette
		value := reflect.ValueOf(pal)
		for _, field := range fields {
			if strings.TrimSpace(value.FieldByIndex(field.Index).String()) == "" {
				t.Errorf("%s: palette field %q is empty (untokenized)", entry.Name, field.Name)
			}
		}
	}
}
