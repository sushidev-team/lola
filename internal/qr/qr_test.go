package qr

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// The encoder is tested against PUBLISHED constants, not against itself: a
// round trip through our own decoder would prove only that two halves of one
// mistake agree.

var formatTable = map[Level][8]string{
	L: {"111011111000100", "111001011110011", "111110110101010", "111100010011101", "110011000101111", "110001100011000", "110110001000001", "110100101110110"},
	M: {"101010000010010", "101000100100101", "101111001111100", "101101101001011", "100010111111001", "100000011001110", "100111110010111", "100101010100000"},
	Q: {"011010101011111", "011000001101000", "011111100110001", "011101000000110", "010010010110100", "010000110000011", "010111011011010", "010101111101101"},
	H: {"001011010001001", "001001110111110", "001110011100111", "001100111010000", "000011101100010", "000001001010101", "000110100001100", "000100000111011"},
}

func TestFormatInformation(t *testing.T) {
	for lvl, row := range formatTable {
		for mask := 0; mask < 8; mask++ {
			got := strconv.FormatInt(int64(formatBits(lvl, mask)), 2)
			got = strings.Repeat("0", 15-len(got)) + got
			if got != row[mask] {
				t.Errorf("%v mask %d: got %s want %s", lvl, mask, got, row[mask])
			}
		}
	}
}

func TestVersionInformation(t *testing.T) {
	want := []int{
		0x07c94, 0x085bc, 0x09a99, 0x0a4d3, 0x0bbf6, 0x0c762, 0x0d847, 0x0e60d, 0x0f928, 0x10b78,
		0x1145d, 0x12a17, 0x13532, 0x149a6, 0x15683, 0x168c9, 0x177ec, 0x18ec4, 0x191e1, 0x1afab,
		0x1b08e, 0x1cc1a, 0x1d33f, 0x1ed75, 0x1f250, 0x209d5, 0x216f0, 0x228ba, 0x2379f, 0x24b0b,
		0x2542e, 0x26a64, 0x27541, 0x28c69,
	}
	for v := 7; v <= 40; v++ {
		if got := versionBits(v); got != want[v-7] {
			t.Errorf("version %d: got %#x want %#x", v, got, want[v-7])
		}
	}
}

func TestGeneratorPolynomials(t *testing.T) {
	// Published as alpha exponents; the log table is built independently here.
	var log [256]int
	x := 1
	for i := 0; i < 255; i++ {
		log[x] = i
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11d
		}
	}
	exps := func(deg int) []int {
		var out []int
		for _, v := range rsGenerator(deg) {
			out = append(out, log[v])
		}
		return out
	}
	cases := map[int][]int{
		7:  {87, 229, 146, 149, 238, 102, 21},
		10: {251, 67, 46, 61, 118, 70, 64, 94, 32, 45},
		13: {74, 152, 176, 100, 86, 100, 106, 104, 130, 218, 206, 140, 78},
		17: {43, 139, 206, 78, 43, 239, 123, 206, 214, 147, 24, 99, 150, 39, 243, 163, 136},
	}
	for deg, want := range cases {
		if got := exps(deg); !reflect.DeepEqual(got, want) {
			t.Errorf("degree %d: got %v want %v", deg, got, want)
		}
	}
}

func TestDataCapacityTable(t *testing.T) {
	if rawDataModules(1)/8 != 26 {
		t.Fatalf("version 1 raw codewords = %d", rawDataModules(1)/8)
	}
	cases := []struct {
		v    int
		l    Level
		want int
	}{
		{1, L, 19}, {1, M, 16}, {1, Q, 13}, {1, H, 9},
		{2, L, 34}, {2, M, 28}, {2, Q, 22}, {2, H, 16},
		{3, L, 55}, {3, M, 44}, {3, Q, 34}, {3, H, 26},
		{4, L, 80}, {4, M, 64}, {4, Q, 48}, {4, H, 36},
		{5, L, 108}, {5, M, 86}, {5, Q, 62}, {5, H, 46},
		{10, L, 274}, {10, M, 216}, {10, Q, 154}, {10, H, 122},
		{40, L, 2956}, {40, M, 2334}, {40, Q, 1666}, {40, H, 1276},
	}
	for _, c := range cases {
		if got := dataCodewords(c.v, c.l); got != c.want {
			t.Errorf("version %d level %v: got %d want %d", c.v, c.l, got, c.want)
		}
	}
}

func TestAlignmentPositions(t *testing.T) {
	cases := map[int][]int{
		2:  {6, 18},
		7:  {6, 22, 38},
		10: {6, 28, 50},
		20: {6, 34, 62, 90},
		32: {6, 34, 60, 86, 112, 138},
		40: {6, 30, 58, 86, 114, 142, 170},
	}
	if got := alignmentPositions(1); len(got) != 0 {
		t.Errorf("version 1: got %v", got)
	}
	for v, want := range cases {
		if got := alignmentPositions(v); !reflect.DeepEqual(got, want) {
			t.Errorf("version %d: got %v want %v", v, got, want)
		}
	}
}

func TestReedSolomonWorkedExample(t *testing.T) {
	data := []int{0x20, 0x5b, 0x0b, 0x78, 0xd1, 0x72, 0xdc, 0x4d, 0x43, 0x40, 0xec, 0x11, 0xec, 0x11, 0xec, 0x11}
	want := []int{0xc4, 0x23, 0x27, 0x77, 0xeb, 0xd7, 0xe7, 0xe2, 0x5d, 0x17}
	if got := rsRemainder(data, rsGenerator(10)); !reflect.DeepEqual(got, want) {
		t.Errorf("got %x want %x", got, want)
	}
}

func mustEncode(t *testing.T, s string, l Level) *Matrix {
	t.Helper()
	m, err := Encode([]byte(s), l)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestStructure(t *testing.T) {
	m := mustEncode(t, "lola", M)
	if m.Size != 4*m.Version()+17 || len(m.modules) != m.Size {
		t.Fatalf("size %d version %d", m.Size, m.Version())
	}
	for _, o := range [][2]int{{0, 0}, {m.Size - 7, 0}, {0, m.Size - 7}} {
		for y := 0; y < 7; y++ {
			for x := 0; x < 7; x++ {
				ring := cheb(x-3, y-3)
				if m.Dark(o[0]+x, o[1]+y) != (ring != 2) {
					t.Fatalf("finder at %v cell %d,%d", o, x, y)
				}
			}
		}
	}
	for i := 0; i < 8; i++ {
		if m.Dark(i, 7) || m.Dark(7, i) {
			t.Fatalf("separator dark at %d", i)
		}
	}
	for i := 8; i < m.Size-8; i++ {
		if m.Dark(i, 6) != (i%2 == 0) || m.Dark(6, i) != (i%2 == 0) {
			t.Fatalf("timing at %d", i)
		}
	}
	if !m.Dark(8, m.Size-8) {
		t.Error("dark module missing")
	}
	if m.Dark(-1, 0) || m.Dark(0, m.Size) {
		t.Error("outside the matrix must be light")
	}
}

func TestVersionSelection(t *testing.T) {
	if v := mustEncode(t, strings.Repeat("a", 17), L).Version(); v != 1 {
		t.Errorf("17 bytes L: version %d", v)
	}
	if v := mustEncode(t, strings.Repeat("a", 18), L).Version(); v != 2 {
		t.Errorf("18 bytes L: version %d", v)
	}
	if v := mustEncode(t, strings.Repeat("a", 17), H).Version(); v <= 1 {
		t.Errorf("17 bytes H: version %d", v)
	}
	// Bytes, not characters.
	if a, b := mustEncode(t, "你好世界", H).Version(), mustEncode(t, strings.Repeat("a", 12), H).Version(); a != b {
		t.Errorf("utf-8: %d vs %d", a, b)
	}
	if _, err := Encode([]byte(strings.Repeat("a", 3000)), H); !errors.Is(err, ErrTooLong) {
		t.Errorf("3000 bytes H: err %v", err)
	}
	if _, err := Encode([]byte("x"), Level(9)); err == nil {
		t.Error("unknown level accepted")
	}
	if m, err := Encode(nil, L); err != nil || m.Version() != 1 {
		t.Errorf("empty payload: %v", err)
	}
}

func TestMaskIsLowestPenalty(t *testing.T) {
	data := []byte("lola-insecure1.abcdef")
	m, err := Encode(data, M)
	if err != nil {
		t.Fatal(err)
	}
	words := interleave(buildCodewords(data, m.Version(), M), m.Version(), M)
	best := -1
	for mask := 0; mask < 8; mask++ {
		g := newGrid(m.Size)
		g.drawFunctionPatterns(m.Version(), M)
		g.placeData(words)
		g.applyMask(mask)
		g.drawFormat(M, mask)
		if s := penalty(g.modules); best < 0 || s < best {
			best = s
		}
	}
	if got := penalty(m.modules); got != best {
		t.Errorf("penalty %d, minimum %d", got, best)
	}
	blank := make([][]bool, 21)
	for i := range blank {
		blank[i] = make([]bool, 21)
	}
	if penalty(blank) <= 1000 {
		t.Error("all-light grid must be heavily penalised")
	}
}

func TestDeterministic(t *testing.T) {
	a, b := mustEncode(t, "lola", Q), mustEncode(t, "lola", Q)
	if a.mask != b.mask || !reflect.DeepEqual(a.modules, b.modules) {
		t.Error("encoding is not deterministic")
	}
}

const connectCode = "lola1.eyJ2IjoxLCJhZGRycyI6WyIxMjcuMC4wLjEiLCI6OjEiXSwicG9ydCI6NzcxNywicGluIjoiQzR0ZDR1eWVKTVN5eGZvQXNCM2k5OEtkNkpoa3BPVGYzT3hpcGlxK3N4ST0iLCJrZXkiOiIwMTIzNDU2Nzg5YWJjZGVmMDEyMzQ1Njc4OWFiY2RlZiJ9"

func TestConnectCodeSize(t *testing.T) {
	if m := mustEncode(t, connectCode, M); m.Version() != 10 || m.Size != 57 || m.mask != 4 {
		t.Errorf("M: version %d size %d mask %d", m.Version(), m.Size, m.mask)
	}
	if m := mustEncode(t, connectCode, L); m.Version() != 9 {
		t.Errorf("L: version %d", m.Version())
	}
}

func TestMatchesTSVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Name    string   `json:"name"`
		Data    string   `json:"data"`
		Level   string   `json:"level"`
		Version int      `json:"version"`
		Mask    int      `json:"mask"`
		Size    int      `json:"size"`
		Rows    []string `json:"rows"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 4 {
		t.Fatalf("%d vectors, want 4", len(vectors))
	}
	levels := map[string]Level{"L": L, "M": M, "Q": Q, "H": H}
	for _, v := range vectors {
		m, err := Encode([]byte(v.Data), levels[v.Level])
		if err != nil {
			t.Fatalf("%s: %v", v.Name, err)
		}
		if m.Version() != v.Version || m.mask != v.Mask || m.Size != v.Size {
			t.Errorf("%s: version/mask/size %d/%d/%d, TS %d/%d/%d", v.Name, m.Version(), m.mask, m.Size, v.Version, v.Mask, v.Size)
			continue
		}
		for y, row := range v.Rows {
			for x, c := range row {
				if m.Dark(x, y) != (c == '1') {
					t.Fatalf("%s: module (%d,%d) differs from the TS encoder", v.Name, x, y)
				}
			}
		}
	}
}

func TestTerminalSGRDarkModules(t *testing.T) {
	m := mustEncode(t, "lola", L)
	out, err := Terminal(m, TerminalOptions{Columns: 80})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	span := m.Size + 8
	if want := (span + 1) / 2; len(lines) != want {
		t.Fatalf("%d lines, want %d", len(lines), want)
	}
	for i, ln := range lines {
		if !strings.HasPrefix(ln, "\x1b[30;47m") || !strings.HasSuffix(ln, "\x1b[0m") {
			t.Fatalf("line %d not wrapped: %q", i, ln)
		}
		body := strings.TrimSuffix(strings.TrimPrefix(ln, "\x1b[30;47m"), "\x1b[0m")
		if n := len([]rune(body)); n != span {
			t.Fatalf("line %d: %d columns, want %d", i, n, span)
		}
		for x, r := range []rune(body) {
			top, bot := m.Dark(x-4, 2*i-4), m.Dark(x-4, 2*i+1-4)
			want := ' '
			switch {
			case top && bot:
				want = '█'
			case top:
				want = '▀'
			case bot:
				want = '▄'
			}
			if r != want {
				t.Fatalf("line %d col %d: %q want %q", i, x, r, want)
			}
		}
	}
	// The quiet zone is blank in the first four module rows.
	if body := strings.TrimPrefix(lines[0], "\x1b[30;47m"); !strings.HasPrefix(body, strings.Repeat(" ", span)) {
		t.Error("top quiet zone not light")
	}
}

func TestTerminalNoColorInverts(t *testing.T) {
	m := mustEncode(t, "lola", L)
	out, err := Terminal(m, TerminalOptions{NoColor: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\x1b") {
		t.Fatal("NoColor emitted an escape")
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	span := m.Size + 8
	for i, ln := range lines {
		rs := []rune(ln)
		if len(rs) != span {
			t.Fatalf("line %d: %d columns", i, len(rs))
		}
		for x, r := range rs {
			top, bot := !m.Dark(x-4, 2*i-4), !m.Dark(x-4, 2*i+1-4)
			want := ' '
			switch {
			case top && bot:
				want = '█'
			case top:
				want = '▀'
			case bot:
				want = '▄'
			}
			if r != want {
				t.Fatalf("line %d col %d: %q want %q", i, x, r, want)
			}
		}
	}
	// Quiet zone is light, so it is drawn solid.
	if !strings.HasPrefix(lines[0], strings.Repeat("█", span)) {
		t.Error("top quiet zone not solid")
	}
}

func TestTerminalTooNarrow(t *testing.T) {
	m := mustEncode(t, strings.Repeat("AbCd0123-_", 102), M)
	if m.Size+8 <= 80 {
		t.Fatalf("fixture too small: %d", m.Size)
	}
	out, err := Terminal(m, TerminalOptions{Columns: 80})
	if !errors.Is(err, ErrTooWide) || out != "" {
		t.Fatalf("out %q err %v", out, err)
	}
	if _, err := Terminal(m, TerminalOptions{Columns: m.Size + 8}); err != nil {
		t.Errorf("exact fit refused: %v", err)
	}
	if _, err := Terminal(m, TerminalOptions{Columns: m.Size + 7}); !errors.Is(err, ErrTooWide) {
		t.Errorf("one short accepted: %v", err)
	}
	if _, err := Terminal(nil, TerminalOptions{}); err == nil {
		t.Error("nil matrix accepted")
	}
}
