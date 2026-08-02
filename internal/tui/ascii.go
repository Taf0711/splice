package tui

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

var asciiGlyphs = map[rune]string{
	'—': "-",
	'·': ".",
	'…': ".",
	'→': ">",
	'│': "|",
	'─': "-",
	'╭': "+",
	'╮': "+",
	'╰': "+",
	'╯': "+",
	'└': "+",
	'✓': "v",
	'✗': "x",
	'●': "*",
	'○': "o",
	'❯': ">",
	'▸': ">",
	'▌': "|",
	'•': "*",
	'≥': ">",
	'−': "-",
	'∞': "8",
	'²': "2",
	'🧵': "::",
}

func spliceASCIIEnabled(getenv func(string) string) bool {
	value := getenv("SPLICE_ASCII")
	return value != "" && value != "0"
}

// foldASCII preserves terminal cell widths while replacing non-ASCII glyphs.
func foldASCII(content string, enabled bool) string {
	if !enabled {
		return content
	}

	var folded strings.Builder
	folded.Grow(len(content))
	for _, glyph := range content {
		if glyph < 0x80 {
			folded.WriteRune(glyph)
			continue
		}
		if replacement, ok := asciiGlyphs[glyph]; ok {
			folded.WriteString(replacement)
			continue
		}
		if runewidth.RuneWidth(glyph) == 2 {
			folded.WriteString("??")
		} else {
			folded.WriteByte('?')
		}
	}
	return folded.String()
}
