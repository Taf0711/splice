package tui

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

var asciiGlyphs = map[rune]string{
	'—':      "-",
	'·':      ".",
	'…':      ".",
	'→':      ">",
	'←':      "<",
	'↑':      "^",
	'↓':      "v",
	'■':      "#",
	'□':      "-",
	'│':      "|",
	'─':      "-",
	'╭':      "+",
	'╮':      "+",
	'╰':      "+",
	'╯':      "+",
	'└':      "+",
	'✓':      "v",
	'✗':      "x",
	'●':      "*",
	'○':      "o",
	'❯':      ">",
	'▸':      ">",
	'▌':      "|",
	'•':      "*",
	'≥':      ">",
	'−':      "-",
	'∞':      "8",
	'²':      "2",
	'🧵':      "::",
	'⏎':      "<",
	'›':      ">",
	'▾':      "v",
	'⇆':      "=",
	'\ue0a0': "*", // powerline branch
	'★':      "*",
	'↳':      ">",
	'µ':      "u",
	'é':      "e",
	'※':      "*",
	'⊘':      "x",
	'◆':      "*",
	'–':      "-",
	'⚠':      "!",
	'↩':      "<",
	'◐':      "~",
	'↻':      "@",
	'◔':      "o",
	'◉':      "*",
	'▲':      "^",
	// The running-stage arc spinner keeps animating as | / - \.
	'◜': "/",
	'◠': "-",
	'◝': "\\",
	'◞': "/",
	'◡': "-",
	'◟': "\\",
}

// asciiRangeGlyph folds whole Unicode blocks the TUI draws from: box
// drawing by shape, block elements (bars, fills) to '#', and braille
// spinner frames to '*'. Liveness survives through the elapsed timer.
func asciiRangeGlyph(glyph rune) (string, bool) {
	switch {
	case glyph >= 0x2500 && glyph <= 0x257F:
		switch glyph {
		case '─', '━', '┄', '┅', '┈', '┉', '╌', '╍', '═', '╴', '╶', '╸', '╺':
			return "-", true
		case '│', '┃', '┆', '┇', '┊', '┋', '╎', '╏', '║', '╵', '╷', '╹', '╻':
			return "|", true
		}
		return "+", true
	case glyph >= 0x2580 && glyph <= 0x259F:
		return "#", true
	case glyph >= 0x2800 && glyph <= 0x28FF:
		return "*", true
	}
	return "", false
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
		if replacement, ok := asciiRangeGlyph(glyph); ok && runewidth.RuneWidth(glyph) == 1 {
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
