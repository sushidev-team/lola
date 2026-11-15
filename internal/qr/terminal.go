package qr

import (
	"errors"
	"strings"
)

// quietZone is the light border, in modules, a scanner needs around a code.
const quietZone = 4

// ErrTooWide means the code plus its quiet zone needs more columns than the
// terminal has.
var ErrTooWide = errors.New("qr: terminal too narrow for the code")

// TerminalOptions controls Terminal.
type TerminalOptions struct {
	// NoColor draws without SGR escapes. Light modules are then the glyphs, so
	// the code reads correctly on a dark-background terminal.
	NoColor bool
	// Columns is the terminal width. Zero or less means "do not check".
	Columns int
}

const (
	sgrOn  = "\x1b[30;47m" // black on white: independent of the terminal theme
	sgrOff = "\x1b[0m"
)

// Terminal renders m with a 4-module quiet zone, two module rows per text line.
// Every line ends in "\n". Size+8 > Columns returns ErrTooWide and no output.
func Terminal(m *Matrix, o TerminalOptions) (string, error) {
	if m == nil || m.Size <= 0 {
		return "", errors.New("qr: no matrix")
	}
	span := m.Size + 2*quietZone
	if o.Columns > 0 && span > o.Columns {
		return "", ErrTooWide
	}
	// In colour mode dark modules are the glyph; without colour light ones are.
	glyphIs := !o.NoColor
	cell := func(x, y int) bool { return m.Dark(x-quietZone, y-quietZone) == glyphIs }

	var b strings.Builder
	for y := 0; y < span; y += 2 {
		if !o.NoColor {
			b.WriteString(sgrOn)
		}
		for x := 0; x < span; x++ {
			top := cell(x, y)
			bottom := y+1 < span && cell(x, y+1)
			if y+1 >= span {
				// The padding row below an odd-height code is quiet zone.
				bottom = !glyphIs
			}
			switch {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteByte(' ')
			}
		}
		if !o.NoColor {
			b.WriteString(sgrOff)
		}
		b.WriteByte('\n')
	}
	return b.String(), nil
}
