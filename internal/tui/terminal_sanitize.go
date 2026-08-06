package tui

import "strings"

// sanitizeTerminalOutput removes terminal control sequences from untrusted
// output in one forward scan. It removes sequence payloads as well as their
// introducers, while preserving ordinary Unicode text byte-for-byte when the
// input is valid UTF-8.
func sanitizeTerminalOutput(input string, keepNewline bool) string {
	runes := []rune(input)
	var out strings.Builder
	out.Grow(len(input))
	for index := 0; index < len(runes); {
		r := runes[index]
		switch {
		case r == '\x1b':
			index = skipEscapeSequence(runes, index)
		case r == '\x9b': // C1 CSI
			index = skipCSISequence(runes, index+1)
		case r == '\x9d': // C1 OSC
			index = skipOSCSequence(runes, index+1)
		case r == '\x90' || r == '\x98' || r == '\x9e' || r == '\x9f': // DCS, SOS, PM, APC
			index = skipSTSequence(runes, index+1)
		case r == '\n':
			if keepNewline {
				out.WriteRune(r)
			}
			index++
		case r == '\r':
			if keepNewline {
				out.WriteRune('\n')
			}
			index++
			if index < len(runes) && runes[index] == '\n' {
				index++
			}
		case r == '\t':
			out.WriteString("    ")
			index++
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			// Drop C0, DEL, and C1 controls. C1 sequence introducers are
			// handled above so their payloads do not remain visible.
			index++
		default:
			out.WriteRune(r)
			index++
		}
	}
	return out.String()
}

func skipEscapeSequence(runes []rune, index int) int {
	if index+1 >= len(runes) {
		return len(runes)
	}
	switch runes[index+1] {
	case '[':
		return skipCSISequence(runes, index+2)
	case ']':
		return skipOSCSequence(runes, index+2)
	case 'P', '_', '^', 'X':
		return skipSTSequence(runes, index+2)
	default:
		// Other ESC forms are ESC plus one character.
		return index + 2
	}
}

func skipCSISequence(runes []rune, index int) int {
	for ; index < len(runes); index++ {
		r := runes[index]
		if r == '\n' || r == '\r' {
			return index
		}
		if r >= '@' && r <= '~' {
			return index + 1
		}
	}
	return len(runes)
}

func skipOSCSequence(runes []rune, index int) int {
	for ; index < len(runes); index++ {
		switch runes[index] {
		case '\a', '\x9c': // BEL and C1 ST
			return index + 1
		case '\n', '\r':
			// Do not let malformed OSC consume later lines.
			return index
		case '\x1b':
			if index+1 < len(runes) && runes[index+1] == '\\' {
				return index + 2
			}
		}
	}
	return len(runes)
}

func skipSTSequence(runes []rune, index int) int {
	for ; index < len(runes); index++ {
		if runes[index] == '\n' || runes[index] == '\r' {
			return index
		}
		if runes[index] == '\x1b' && index+1 < len(runes) && runes[index+1] == '\\' {
			return index + 2
		}
	}
	return len(runes)
}
