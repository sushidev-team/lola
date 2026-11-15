// Package qr is a stdlib-only QR encoder (byte mode, versions 1 to 40) and a
// terminal renderer. It is a port of desktop/frontend/src/lib/qr.ts and must
// produce the same matrix for the same input: testdata/vectors.json pins that.
//
// It imports nothing outside the standard library so the CLI and the daemon can
// share it without a dependency edge. There is no decoder: nothing here reads a
// QR.
package qr

import (
	"errors"
	"fmt"
)

// Level is the error-correction level.
type Level int

const (
	L Level = iota
	M
	Q
	H
)

func (l Level) String() string {
	switch l {
	case L:
		return "L"
	case M:
		return "M"
	case Q:
		return "Q"
	case H:
		return "H"
	}
	return fmt.Sprintf("Level(%d)", int(l))
}

// ErrTooLong is returned when the payload does not fit a version 40 code at
// the requested level.
var ErrTooLong = errors.New("qr: payload does not fit a version 40 code")

// Matrix is an encoded code without its quiet zone.
type Matrix struct {
	// Size is modules per side, 21 + 4*(version-1).
	Size    int
	version int
	mask    int
	modules [][]bool // modules[y][x]; true is dark
}

// Dark reports whether the module at (x, y) is dark. Coordinates outside the
// matrix are light, which is what the quiet zone is.
func (m *Matrix) Dark(x, y int) bool {
	if m == nil || x < 0 || y < 0 || x >= m.Size || y >= m.Size {
		return false
	}
	return m.modules[y][x]
}

// Version is the symbol version, 1 to 40.
func (m *Matrix) Version() int { return m.version }

// formatBitsOf is the two-bit field the format information carries. It is not
// the Level's numeric value.
var formatBitsOf = [4]int{L: 1, M: 0, Q: 3, H: 2}

// Error-correction codewords per block and block counts, indexed [level][version];
// index 0 is unused so a version indexes directly.
var eccCodewordsPerBlock = [4][41]int{
	L: {-1, 7, 10, 15, 20, 26, 18, 20, 24, 30, 18, 20, 24, 26, 30, 22, 24, 28, 30, 28, 28, 28, 28, 30, 30, 26, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30},
	M: {-1, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26, 30, 22, 22, 24, 24, 28, 28, 26, 26, 26, 26, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28},
	Q: {-1, 13, 22, 18, 26, 18, 24, 18, 22, 20, 24, 28, 26, 24, 20, 30, 24, 28, 28, 26, 30, 28, 30, 30, 30, 30, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30},
	H: {-1, 17, 28, 22, 16, 22, 28, 26, 26, 24, 28, 24, 28, 22, 24, 24, 30, 28, 28, 26, 28, 30, 24, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30},
}

var numEccBlocks = [4][41]int{
	L: {-1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 4, 4, 4, 4, 4, 6, 6, 6, 6, 7, 8, 8, 9, 9, 10, 12, 12, 12, 13, 14, 15, 16, 17, 18, 19, 19, 20, 21, 22, 24, 25},
	M: {-1, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5, 5, 8, 9, 9, 10, 10, 11, 13, 14, 16, 17, 17, 18, 20, 21, 23, 25, 26, 28, 29, 31, 33, 35, 37, 38, 40, 43, 45, 47, 49},
	Q: {-1, 1, 1, 2, 2, 4, 4, 6, 6, 8, 8, 8, 10, 12, 16, 12, 17, 16, 18, 21, 20, 23, 23, 25, 27, 29, 34, 34, 35, 38, 40, 43, 45, 48, 51, 53, 56, 59, 62, 65, 68},
	H: {-1, 1, 1, 2, 4, 4, 4, 5, 5, 8, 8, 11, 11, 16, 16, 18, 16, 19, 21, 25, 25, 25, 34, 30, 32, 35, 37, 40, 42, 45, 48, 51, 54, 57, 60, 63, 66, 70, 74, 77, 81},
}

// rawDataModules is every module that can carry data or ECC: all but finders,
// separators, timing, alignment, format and (from version 7) version areas.
func rawDataModules(version int) int {
	n := (16*version+128)*version + 64
	if version >= 2 {
		numAlign := version/7 + 2
		n -= (25*numAlign-10)*numAlign - 55
		if version >= 7 {
			n -= 36
		}
	}
	return n
}

// dataCodewords is the data capacity of a version and level, ECC subtracted.
func dataCodewords(version int, lvl Level) int {
	return rawDataModules(version)/8 - eccCodewordsPerBlock[lvl][version]*numEccBlocks[lvl][version]
}

// alignmentPositions computes the alignment-pattern centres from the
// specification's arithmetic rather than a hand-typed table.
func alignmentPositions(version int) []int {
	if version == 1 {
		return nil
	}
	size := 4*version + 17
	numAlign := version/7 + 2
	step := ((version*4 + 4) + (numAlign*2 - 2) - 1) / (numAlign*2 - 2) * 2
	if version == 32 { // the one case the general formula does not produce
		step = 26
	}
	out := make([]int, numAlign)
	out[0] = 6
	for i, pos := numAlign-1, size-7; i >= 1; i, pos = i-1, pos-step {
		out[i] = pos
	}
	return out
}

// GF(256) with the primitive polynomial x^8+x^4+x^3+x^2+1 (0x11D).
func gfMul(a, b int) int {
	r := 0
	for i := 7; i >= 0; i-- {
		r = (r << 1) ^ ((r >> 7) * 0x11d)
		r ^= ((b >> uint(i)) & 1) * a
	}
	return r & 0xff
}

// rsGenerator is the generator polynomial of the given degree, high-order term
// omitted.
func rsGenerator(degree int) []int {
	result := make([]int, degree)
	result[degree-1] = 1
	root := 1
	for i := 0; i < degree; i++ {
		for j := 0; j < degree; j++ {
			result[j] = gfMul(result[j], root)
			if j+1 < degree {
				result[j] ^= result[j+1]
			}
		}
		root = gfMul(root, 0x02)
	}
	return result
}

// rsRemainder is the Reed-Solomon remainder: the ECC codewords of one block.
func rsRemainder(data, generator []int) []int {
	result := make([]int, len(generator))
	for _, b := range data {
		factor := b ^ result[0]
		copy(result, result[1:])
		result[len(result)-1] = 0
		for i := range generator {
			result[i] ^= gfMul(generator[i], factor)
		}
	}
	return result
}

// formatBits is the 15-bit format information: 5 data bits, a BCH(15,5)
// remainder, XORed with 0x5412.
func formatBits(lvl Level, mask int) int {
	data := formatBitsOf[lvl]<<3 | mask
	rem := data
	for i := 0; i < 10; i++ {
		rem = (rem << 1) ^ ((rem >> 9) * 0x537)
	}
	return ((data<<10 | rem) ^ 0x5412) & 0x7fff
}

// versionBits is the 18-bit version information, present from version 7.
func versionBits(version int) int {
	rem := version
	for i := 0; i < 12; i++ {
		rem = (rem << 1) ^ ((rem >> 11) * 0x1f25)
	}
	return version<<12 | rem
}

func charCountBits(version int) int {
	if version <= 9 {
		return 8
	}
	return 16
}

func chooseVersion(byteLen int, lvl Level) (int, error) {
	for v := 1; v <= 40; v++ {
		if 4+charCountBits(v)+byteLen*8 <= dataCodewords(v, lvl)*8 {
			return v, nil
		}
	}
	return 0, ErrTooLong
}

func buildCodewords(data []byte, version int, lvl Level) []int {
	capacity := dataCodewords(version, lvl)
	bits := make([]int, 0, capacity*8)
	push := func(value, n int) {
		for i := n - 1; i >= 0; i-- {
			bits = append(bits, (value>>uint(i))&1)
		}
	}
	push(0b0100, 4) // byte mode
	push(len(data), charCountBits(version))
	for _, b := range data {
		push(int(b), 8)
	}
	capBits := capacity * 8
	term := capBits - len(bits)
	if term > 4 {
		term = 4
	}
	push(0, term)
	push(0, (8-len(bits)%8)%8)
	words := make([]int, 0, capacity)
	for i := 0; i < len(bits); i += 8 {
		w := 0
		for j := 0; j < 8; j++ {
			w = w<<1 | bits[i+j]
		}
		words = append(words, w)
	}
	for pad := 0xec; len(words) < capacity; pad ^= 0xec ^ 0x11 {
		words = append(words, pad)
	}
	return words
}

// interleave splits into blocks, appends each block's ECC and lays the
// codewords out in the specification's interleaved order.
func interleave(words []int, version int, lvl Level) []int {
	numBlocks := numEccBlocks[lvl][version]
	eccLen := eccCodewordsPerBlock[lvl][version]
	rawWords := rawDataModules(version) / 8
	shortLen := rawWords/numBlocks - eccLen
	numShort := numBlocks - rawWords%numBlocks

	gen := rsGenerator(eccLen)
	blocks := make([][]int, numBlocks)
	eccBlocks := make([][]int, numBlocks)
	off := 0
	for i := 0; i < numBlocks; i++ {
		n := shortLen
		if i >= numShort {
			n++
		}
		blocks[i] = words[off : off+n]
		off += n
		eccBlocks[i] = rsRemainder(blocks[i], gen)
	}
	out := make([]int, 0, rawWords)
	for i := 0; i <= shortLen; i++ {
		for b := 0; b < numBlocks; b++ {
			if i < len(blocks[b]) {
				out = append(out, blocks[b][i])
			}
		}
	}
	for i := 0; i < eccLen; i++ {
		for b := 0; b < numBlocks; b++ {
			out = append(out, eccBlocks[b][i])
		}
	}
	return out
}

type grid struct {
	size     int
	modules  [][]bool
	reserved [][]bool // true where a function pattern lives
}

func newGrid(size int) *grid {
	blank := func() [][]bool {
		g := make([][]bool, size)
		for i := range g {
			g[i] = make([]bool, size)
		}
		return g
	}
	return &grid{size: size, modules: blank(), reserved: blank()}
}

func (g *grid) setFn(x, y int, dark bool) {
	if x < 0 || y < 0 || x >= g.size || y >= g.size {
		return
	}
	g.modules[y][x] = dark
	g.reserved[y][x] = true
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

func cheb(dx, dy int) int {
	if abs(dx) > abs(dy) {
		return abs(dx)
	}
	return abs(dy)
}

func (g *grid) drawFinder(cx, cy int) {
	for dy := -4; dy <= 4; dy++ {
		for dx := -4; dx <= 4; dx++ {
			d := cheb(dx, dy)
			g.setFn(cx+dx, cy+dy, d != 2 && d != 4)
		}
	}
}

func (g *grid) drawFunctionPatterns(version int, lvl Level) {
	size := g.size
	for i := 0; i < size; i++ {
		g.setFn(6, i, i%2 == 0)
		g.setFn(i, 6, i%2 == 0)
	}
	g.drawFinder(3, 3)
	g.drawFinder(size-4, 3)
	g.drawFinder(3, size-4)

	align := alignmentPositions(version)
	for i := range align {
		for j := range align {
			corner := (i == 0 && j == 0) || (i == 0 && j == len(align)-1) || (i == len(align)-1 && j == 0)
			if corner {
				continue
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					g.setFn(align[i]+dx, align[j]+dy, cheb(dx, dy) != 1)
				}
			}
		}
	}
	// Reserve the format modules with a placeholder mask; drawFormat rewrites
	// them once the mask is chosen.
	g.drawFormat(lvl, 0)
	if version >= 7 {
		g.drawVersion(version)
	}
}

func (g *grid) drawFormat(lvl Level, mask int) {
	size := g.size
	bits := formatBits(lvl, mask)
	bit := func(i int) bool { return (bits>>uint(i))&1 == 1 }
	for i := 0; i <= 5; i++ {
		g.setFn(8, i, bit(i))
	}
	g.setFn(8, 7, bit(6))
	g.setFn(8, 8, bit(7))
	g.setFn(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		g.setFn(14-i, 8, bit(i))
	}
	for i := 0; i < 8; i++ {
		g.setFn(size-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		g.setFn(8, size-15+i, bit(i))
	}
	g.setFn(8, size-8, true) // the dark module
}

func (g *grid) drawVersion(version int) {
	bits := versionBits(version)
	for i := 0; i < 18; i++ {
		dark := (bits>>uint(i))&1 == 1
		a := g.size - 11 + i%3
		b := i / 3
		g.setFn(a, b, dark)
		g.setFn(b, a, dark)
	}
}

// placeData lays the interleaved codewords out in the two-column zigzag.
func (g *grid) placeData(data []int) {
	size := g.size
	i := 0
	for right := size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5 // the vertical timing column is skipped entirely
		}
		for vert := 0; vert < size; vert++ {
			for j := 0; j < 2; j++ {
				x := right - j
				y := vert
				if (right+1)&2 == 0 {
					y = size - 1 - vert
				}
				if g.reserved[y][x] {
					continue
				}
				if i < len(data)*8 {
					g.modules[y][x] = (data[i>>3]>>uint(7-(i&7)))&1 == 1
					i++
				}
				// Remainder bits stay light.
			}
		}
	}
}

func maskBit(mask, x, y int) bool {
	switch mask {
	case 0:
		return (x+y)%2 == 0
	case 1:
		return y%2 == 0
	case 2:
		return x%3 == 0
	case 3:
		return (x+y)%3 == 0
	case 4:
		return (y/2+x/3)%2 == 0
	case 5:
		return (x*y)%2+(x*y)%3 == 0
	case 6:
		return ((x*y)%2+(x*y)%3)%2 == 0
	case 7:
		return ((x+y)%2+(x*y)%3)%2 == 0
	}
	panic("qr: mask out of range")
}

func (g *grid) applyMask(mask int) {
	for y := 0; y < g.size; y++ {
		for x := 0; x < g.size; x++ {
			if !g.reserved[y][x] && maskBit(mask, x, y) {
				g.modules[y][x] = !g.modules[y][x]
			}
		}
	}
}

const (
	penalty1 = 3
	penalty2 = 3
	penalty3 = 40
	penalty4 = 10
)

// penalty is the specification's four penalty rules, summed. Lower is better.
func penalty(modules [][]bool) int {
	size := len(modules)
	score := 0

	runScore := func(run int) int {
		if run >= 5 {
			return penalty1 + (run - 5)
		}
		return 0
	}
	for i := 0; i < size; i++ {
		runH, runV := 1, 1
		for j := 1; j < size; j++ {
			if modules[i][j] == modules[i][j-1] {
				runH++
			} else {
				score += runScore(runH)
				runH = 1
			}
			if modules[j][i] == modules[j-1][i] {
				runV++
			} else {
				score += runScore(runV)
				runV = 1
			}
		}
		score += runScore(runH) + runScore(runV)
	}

	for y := 0; y < size-1; y++ {
		for x := 0; x < size-1; x++ {
			c := modules[y][x]
			if c == modules[y][x+1] && c == modules[y+1][x] && c == modules[y+1][x+1] {
				score += penalty2
			}
		}
	}

	// Rule 3: 1:1:3:1:1 with four light modules beside it; out-of-range
	// positions never match.
	pattern := [7]bool{true, false, true, true, true, false, true}
	match := func(get func(int) (bool, bool), base int, want []bool) bool {
		for k, w := range want {
			v, ok := get(base + k)
			if !ok || v != w {
				return false
			}
		}
		return true
	}
	light4 := []bool{false, false, false, false}
	for i := 0; i < size; i++ {
		i := i
		row := func(k int) (bool, bool) {
			if k < 0 || k >= size {
				return false, false
			}
			return modules[i][k], true
		}
		col := func(k int) (bool, bool) {
			if k < 0 || k >= size {
				return false, false
			}
			return modules[k][i], true
		}
		for _, get := range []func(int) (bool, bool){row, col} {
			for j := 0; j+7 <= size; j++ {
				if !match(get, j, pattern[:]) {
					continue
				}
				if match(get, j-4, light4) || match(get, j+7, light4) {
					score += penalty3
				}
			}
		}
	}

	dark := 0
	for _, row := range modules {
		for _, m := range row {
			if m {
				dark++
			}
		}
	}
	total := size * size
	k := (abs(dark*20-total*10)+total-1)/total - 1
	if k > 0 {
		score += k * penalty4
	}
	return score
}

// Encode encodes data in byte mode at the smallest version that fits, choosing
// the mask with the lowest penalty. The result is deterministic.
func Encode(data []byte, lvl Level) (*Matrix, error) {
	if lvl < L || lvl > H {
		return nil, fmt.Errorf("qr: unknown error-correction level %d", int(lvl))
	}
	version, err := chooseVersion(len(data), lvl)
	if err != nil {
		return nil, err
	}
	words := interleave(buildCodewords(data, version, lvl), version, lvl)

	var best *grid
	bestMask, bestScore := 0, 0
	for mask := 0; mask < 8; mask++ {
		g := newGrid(4*version + 17)
		g.drawFunctionPatterns(version, lvl)
		g.placeData(words)
		g.applyMask(mask)
		g.drawFormat(lvl, mask)
		if s := penalty(g.modules); best == nil || s < bestScore {
			best, bestMask, bestScore = g, mask, s
		}
	}
	return &Matrix{Size: best.size, version: version, mask: bestMask, modules: best.modules}, nil
}
