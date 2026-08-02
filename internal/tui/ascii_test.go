package tui

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

func TestASCIIReplacementWidths(t *testing.T) {
	// This pins every mapped replacement to the original glyph's terminal width.
	for glyph, replacement := range asciiGlyphs {
		if got, want := runewidth.StringWidth(replacement), runewidth.RuneWidth(glyph); got != want {
			t.Errorf("glyph %q (U+%04X): replacement %q has width %d, want %d", glyph, glyph, replacement, got, want)
		}
	}
	// This pins width-based fallback for representative unmapped narrow and wide glyphs.
	for _, glyph := range []rune{'é', 'λ', '界', '😀'} {
		replacement := foldASCII(string(glyph), true)
		if got, want := runewidth.StringWidth(replacement), runewidth.RuneWidth(glyph); got != want {
			t.Errorf("unmapped glyph %q (U+%04X): replacement %q has width %d, want %d", glyph, glyph, replacement, got, want)
		}
	}
}

func TestFoldASCIIOffIsByteIdentical(t *testing.T) {
	// This pins the escape hatch's disabled path to byte-identical UTF-8 passthrough.
	input := "ASCII — … → 🧵 日本語"
	if got := foldASCII(input, false); got != input {
		t.Fatalf("fold off changed input: got %q, want %q", got, input)
	}
}

func TestFoldASCIIOnProducesASCII(t *testing.T) {
	// This pins the enabled path to ASCII output for every mapped glyph.
	var input strings.Builder
	for glyph := range asciiGlyphs {
		input.WriteRune(glyph)
	}
	for i, byteValue := range []byte(foldASCII(input.String(), true)) {
		if byteValue >= 0x80 {
			t.Fatalf("folded byte %d is non-ASCII: 0x%02x", i, byteValue)
		}
	}
}

func TestSpliceASCIIEnabled(t *testing.T) {
	// This pins unset and "0" to off and any other non-empty value to on.
	for _, test := range []struct {
		name  string
		value string
		want  bool
	}{
		{name: "unset", want: false},
		{name: "zero", value: "0", want: false},
		{name: "one", value: "1", want: true},
		{name: "other", value: "yes", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := spliceASCIIEnabled(func(string) string { return test.value }); got != test.want {
				t.Fatalf("enabled(%q) = %v, want %v", test.value, got, test.want)
			}
		})
	}
}

func TestFoldASCIIIdempotent(t *testing.T) {
	// This pins repeated folding to the same output as one fold.
	input := "— … → 🧵 日本語"
	once := foldASCII(input, true)
	if twice := foldASCII(once, true); twice != once {
		t.Fatalf("fold is not idempotent: once %q, twice %q", once, twice)
	}
}
