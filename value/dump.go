package value

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/velox-io/json/internal/valueabi"
)

// diagramCellW is the display width of each word cell's content area.
const diagramCellW = 5

// Row layout: leading "│" + N × (5-char content + "│") = 1 + 6N screen columns.
// diagramDefaultCols is used when the terminal width cannot be detected (stdout
// piped to a file, under go test, CI without a pty). 32 cells ≈ 193 columns,
// which fits the wide default most terminals and editors assume.
const diagramDefaultCols = 32

// diagramMinCols / diagramMaxCols clamp the auto-detected row width so an
// absurd winsize cannot collapse the grid to one cell or stretch it past the
// palette's useful range.
const (
	diagramMinCols = 4
	diagramMaxCols = 64
)

// diagramPalette cycles through ANSI foreground colors for logical segments.
var diagramPalette = []string{
	"\x1b[31m", // red
	"\x1b[32m", // green
	"\x1b[33m", // yellow
	"\x1b[34m", // blue
	"\x1b[35m", // magenta
	"\x1b[36m", // cyan
}

const ansiReset = "\x1b[0m"
const ansiDim = "\x1b[2m"

// ansiCount is the fixed color for container element counts. It does not
// cycle with the segment palette: count is a semantic fact ("how many members")
// independent of which segment the container lives in, so giving it a stable
// color lets the eye separate it from the close index in any segment.
const ansiCount = "\x1b[32m" // green

const cellPad = "     " // diagramCellW spaces, for padding/empty cells

// cellPadMarker is what a padding cell (one inserted only to keep a number
// pair from splitting across rows) renders as. A visible dot is more honest
// than blank space: it tells the reader the cell is deliberately empty, not
// that a real tape word has no payload.
const cellPadMarker = "  ·  "

// TapeDiagram returns a Unicode box-drawn, ANSI-colored visualization of v's
// tape. Each cell shows the tag char over a payload summary.
//
// The row width adapts to the terminal: when stdout is a TTY, each row holds as
// many cells as the visible column count allows (clamped to [4, 64]); when the
// width cannot be detected (piped output, tests, CI), rows default to 32 cells.
// Pass an explicit width to TapeDiagramCols to override.
//
// Words are colored by logical segment: a maximal run of contiguous words
// (no seam crossed) takes one palette color, cycling per segment. Seam gaps and
// words outside the Value's [base,end) region are dimmed.
//
// Number tags occupy two words. The second cell shows the numeric payload
// with an empty tag position.
//
// String payloads are sanitized to printable ASCII so multi-byte or control
// bytes in JSON text cannot break column alignment; consult Str/Src for the
// faithful bytes.
func (v Value) TapeDiagram() string {
	return v.TapeDiagramCols(autoCols())
}

// TapeDiagramCols is TapeDiagram with an explicit per-row cell count. cols is
// clamped to [diagramMinCols, diagramMaxCols]. Use this when embedding the
// diagram in a fixed-width context or to make output deterministic for tests.
func (v Value) TapeDiagramCols(cols int) string {
	if v.desc.Doc == nil || len(v.desc.Doc.Tape) == 0 {
		return ""
	}
	if cols < diagramMinCols {
		cols = diagramMinCols
	}
	if cols > diagramMaxCols {
		cols = diagramMaxCols
	}
	tape := v.desc.Doc.Tape
	segOf := computeSegments(&v, tape)
	var b strings.Builder
	writeDiagramHeader(&b, &v, cols)
	writeDiagramGrid(&b, &v, tape, segOf, cols)
	return b.String()
}

// autoCols picks the per-row cell count from the terminal width, falling back
// to diagramDefaultCols when stdout is not a TTY. Row width is 1 + 6N, so the
// max N that fits termW is (termW - 1) / 6, then clamped to the safe range.
func autoCols() int {
	termW := terminalCols()
	if termW <= 0 {
		return diagramDefaultCols
	}
	cols := min(max((termW-1)/6, diagramMinCols), diagramMaxCols)
	return cols
}

// computeGaps marks words that a seam leaps over and are therefore dead: they
// are not members of any logical structure (the variant bind widens the seam in
// front of a consumed field to thread past it), so they must not participate in
// the key/value state machine or the container stack. The walk starts at word 0
// and follows every seam through the whole tape so gaps in foreign segments are
// marked too.
//
// The seam is followed through view A, because that is the view the diagram
// renders (see the shift argument threaded from the Value below).
//
// buildUnits still emits units for gap words (the reader wants to see what is
// in the gap), but treats them as inert: no key/value flip, no push, no pop.
func computeGaps(tape []uint64, shift int) []bool {
	gap := make([]bool, len(tape))
	pos := 0
	for pos < len(tape) {
		// The seam test precedes the tag switch: a seam has no tag byte, so
		// classifying one by its high byte is meaningless.
		if w := tape[pos]; valueabi.IsSeam(w) {
			target := pos + int((w>>uint(shift))&valueabi.SeamMask)
			if target <= pos {
				target = pos + 1
			}
			if target > len(tape) {
				target = len(tape)
			}
			for g := pos + 1; g < target; g++ {
				gap[g] = true
			}
			pos = target
			continue
		}
		switch byte(tape[pos] >> 56) {
		case valueabi.TagInt64, valueabi.TagUint64, valueabi.TagDouble:
			pos += 2
		default:
			pos++
		}
	}
	return gap
}

// computeSegments assigns each tape word a logical segment id for coloring.
// Words outside [base,end) and seam gap words get -1 (dim). The walk is linear
// with leap-on-seam, equivalent to the logical skipSeams walk because a seam is
// the sole source of non-contiguity. Number value words inherit their tag word's
// segment.
//
// Base is the absolute origin. Tidx and End are relative to Base, so the
// reachable region is [Base, Base+End).
func computeSegments(v *Value, tape []uint64) []int {
	segOf := make([]int, len(tape))
	for i := range segOf {
		segOf[i] = -1
	}
	base := max(int(v.desc.Base), 0)
	endAbs := min(base+int(v.desc.End), len(tape))
	segID := 0
	pos := base
	for pos < endAbs {
		segOf[pos] = segID
		if w := tape[pos]; valueabi.IsSeam(w) {
			target := pos + int((w>>uint(v.desc.Mode&valueabi.ViewShiftMask))&valueabi.SeamMask)
			if target <= pos { // malformed zero distance
				target = pos + 1
			}
			for g := pos + 1; g < target && g < endAbs; g++ {
				segOf[g] = -1 // gap between the seam and its target
			}
			pos = target
			segID++ // the seam's target starts a new segment
			continue
		}
		switch byte(tape[pos] >> 56) {
		case valueabi.TagInt64, valueabi.TagUint64, valueabi.TagDouble:
			if pos+1 < endAbs {
				segOf[pos+1] = segID // value word shares the tag's segment
			}
			pos += 2
		default:
			pos++
		}
	}
	return segOf
}

// renderCell is one grid cell. absIdx is the tape word; -1 marks padding
// (a row tail left empty so a number pair does not split across rows).
type renderCell struct {
	absIdx int
	isVal  bool // value word of a number pair
	isKey  bool // string cell is an object key (vs a value)
	segID  int
}

// renderUnit groups a tag word with its optional value word (for l/u/d).
type renderUnit struct {
	tagIdx int
	valIdx int // -1 if not a pair
	isPair bool
	isKey  bool // string tag is an object key
}

// buildUnits walks the tape linearly and groups l/u/d tag words with their
// value words. It also marks valueabi.TagString cells as object keys when they sit in
// key position: inside a valueabi.TagObjBeg container, members alternate key/value,
// starting with key. The flip happens after ANY value tag (string, number,
// atom, nested container), not just strings, so a non-string value correctly
// advances the key/value state machine.
//
// base is the Value's base offset: container close words store their close
// position relative to base, so converting to an absolute tape index needs
// base + stored. Without base the stack would pop at the wrong time on
// non-zero-base Values and key/value marking would drift on foreign segments.
//
// gap marks words leapt over by a seam (see computeGaps). Gap words are dead
// fragments left by the variant bind: they are emitted for rendering but are
// inert to the state machine. Treating them as members would desync key/value
// alternation, e.g. an orphaned number value in the gap (its key was leapt over)
// would flip the state and mark the next real key as a value. Skipping state on gap words keeps the on-path and foreign
// segments both correctly alternated.
func buildUnits(tape []uint64, base int, gap []bool) []renderUnit {
	units := make([]renderUnit, 0, len(tape))
	// Stack of open containers. When top is an object, keyNext is true when
	// the next member word is a key, false when it is a value.
	type openContainer struct {
		closeIdx int
		isObj    bool
		keyNext  bool
	}
	var stack []openContainer
	for i := 0; i < len(tape); {
		tag := byte(tape[i] >> 56)
		wordGap := i < len(gap) && gap[i]

		// Pop closed containers before processing this word. Skip on gap
		// words: a close that lives in a gap was bypassed by a jump, so its
		// open was never pushed (the open is in the same gap).
		if !wordGap {
			for len(stack) > 0 && i >= stack[len(stack)-1].closeIdx {
				stack = stack[:len(stack)-1]
			}
		}
		inObj := len(stack) > 0 && stack[len(stack)-1].isObj
		isKey := inObj && !wordGap && stack[len(stack)-1].keyNext

		isPair := (tag == valueabi.TagInt64 || tag == valueabi.TagUint64 || tag == valueabi.TagDouble) && i+1 < len(tape)
		if isPair {
			units = append(units, renderUnit{tagIdx: i, valIdx: i + 1, isPair: true, isKey: isKey})
		} else {
			units = append(units, renderUnit{tagIdx: i, valIdx: -1, isKey: isKey})
		}

		// Advance the key/value state machine only on real members. A seam word
		// threads through an object interior without being a member, and gap
		// words are dead fragments. A pair consumes two words but is one logical
		// value, so a single flip suffices.
		isSeam := valueabi.IsSeam(tape[i])
		if inObj && !isSeam && !wordGap {
			stack[len(stack)-1].keyNext = !stack[len(stack)-1].keyNext
		}

		// Push open containers so subsequent words are tracked as inside.
		// Skip on gap words: a container whose open is in a gap was bypassed
		// and its close is in the same gap, so it must not enter the stack.
		if !wordGap && (tag == valueabi.TagArrBeg || tag == valueabi.TagObjBeg) {
			payload := tape[i] & valueabi.PayloadMask
			// The paired index is relative to base and names the close word.
			closeIdx := base + int(payload&0xFFFFFFFF) + 1 // close at low32+base, past = +1
			stack = append(stack, openContainer{closeIdx: closeIdx, isObj: tag == valueabi.TagObjBeg, keyNext: true})
		}
		if isPair {
			i += 2
		} else {
			i++
		}
	}
	return units
}

// chunkUnits lays units into rows of at most cols cells. A number pair
// (l/u/d tag + value word) may split across rows: the tag lands at the row
// tail and the value at the next row's head. The blank tag position on the
// value cell signals it is a continuation of the preceding l/u/d tag, so
// splitting is still readable.
func chunkUnits(units []renderUnit, segOf []int, cols int) [][]renderCell {
	var rows [][]renderCell
	cur := make([]renderCell, 0, cols)
	flush := func() {
		rows = append(rows, cur)
		cur = make([]renderCell, 0, cols)
	}
	for _, u := range units {
		if len(cur) == cols {
			flush()
		}
		cur = append(cur, renderCell{absIdx: u.tagIdx, isKey: u.isKey, segID: segOf[u.tagIdx]})
		if u.isPair {
			if len(cur) == cols {
				flush()
			}
			cur = append(cur, renderCell{absIdx: u.valIdx, isVal: true, segID: segOf[u.valIdx]})
		}
	}
	if len(cur) > 0 {
		rows = append(rows, cur)
	}
	return rows
}

func writeDiagramHeader(b *strings.Builder, v *Value, cols int) {
	b.WriteString("tape: ")
	b.WriteString(strconv.Itoa(len(v.desc.Doc.Tape)))
	b.WriteString(" words  base=")
	b.WriteString(strconv.Itoa(int(v.desc.Base)))
	b.WriteString(" tidx=")
	b.WriteString(strconv.Itoa(int(v.desc.Tidx)))
	b.WriteString(" end=")
	b.WriteString(strconv.Itoa(int(v.desc.End)))
	b.WriteString("  cols=")
	b.WriteString(strconv.Itoa(cols))
	b.WriteString("\n")
	b.WriteString(ansiDim)
	b.WriteString("colors cycle per logical segment; dim = words not on this Value's path (jump gaps or outside its region); \"k=key; container payload = closeidx(count): close in segment color, count always green; ~N = foreign container close (raw stored offset); obj-end iN = dual shared root's inline count (its begin-word count is the reserve count)\n")
	b.WriteString(ansiReset)
}

func writeDiagramGrid(b *strings.Builder, v *Value, tape []uint64, segOf []int, cols int) {
	units := buildUnits(tape, int(v.desc.Base), computeGaps(tape, int(v.desc.Mode&valueabi.ViewShiftMask)))
	rows := chunkUnits(units, segOf, cols)
	for _, row := range rows {
		writeMarkerLine(b, row, v)
		writeIndexLine(b, row)
		writeBorder(b, len(row), "\u250c", "\u252c", "\u2510", "\u2500")
		writeTagRow(b, row, tape)
		writePayloadRow(b, row, tape, v, segOf)
		writeBorder(b, len(row), "\u2514", "\u2534", "\u2518", "\u2500")
	}
}

// writeMarkerLine marks Base with ▼ and Tidx with ↑. A shared position uses ◆.
// Tidx is relative to Base.
func writeMarkerLine(b *strings.Builder, cells []renderCell, v *Value) {
	width := 1 + len(cells)*6
	runes := make([]rune, width)
	for i := range runes {
		runes[i] = ' '
	}
	place := func(wordIdx int, m rune) {
		for i, c := range cells {
			if c.absIdx == wordIdx {
				col := 1 + i*6 + 2 // center of the 5-wide cell
				if 0 <= col && col < width {
					runes[col] = m
				}
				return
			}
		}
	}
	baseAbs := int(v.desc.Base)
	tidxAbs := int(v.desc.Base) + int(v.desc.Tidx)
	if baseAbs == tidxAbs {
		place(baseAbs, '\u25c6') // ◆
	} else {
		place(baseAbs, '\u25bc') // ▼
		place(tidxAbs, '\u2191') // ↑
	}
	for _, r := range runes {
		if r != ' ' {
			b.WriteString(string(runes))
			b.WriteByte('\n')
			return
		}
	}
}

func writeIndexLine(b *strings.Builder, cells []renderCell) {
	b.WriteByte(' ') // aligns with the leading │
	for _, c := range cells {
		var s string
		if c.absIdx < 0 {
			s = cellPad
		} else {
			s = padLeft(strconv.Itoa(c.absIdx), diagramCellW)
		}
		b.WriteString(s)
		b.WriteByte(' ') // aligns with │
	}
	b.WriteByte('\n')
}

func writeBorder(b *strings.Builder, n int, left, mid, right, fill string) {
	b.WriteString(left)
	for i := range n {
		b.WriteString(strings.Repeat(fill, diagramCellW))
		if i < n-1 {
			b.WriteString(mid)
		}
	}
	b.WriteString(right)
	b.WriteByte('\n')
}

func writeTagRow(b *strings.Builder, cells []renderCell, tape []uint64) {
	b.WriteString("\u2502") // │
	for _, c := range cells {
		var content string
		if c.absIdx < 0 {
			content = cellPadMarker // padding cell: visible marker, not blank
		} else {
			content = padCenter(tagCellContent(c, tape), diagramCellW)
		}
		b.WriteString(colorWrap(c.segID, content))
		b.WriteString("\u2502")
	}
	b.WriteByte('\n')
}

func writePayloadRow(b *strings.Builder, cells []renderCell, tape []uint64, v *Value, segOf []int) {
	b.WriteString("\u2502")
	for _, c := range cells {
		if c.absIdx < 0 {
			b.WriteString(colorWrap(c.segID, cellPadMarker))
			b.WriteString("\u2502")
			continue
		}
		// Container payloads carry two semantic fields (close index, element
		// count). Render each in its own color so they read as two numbers, not
		// a single opaque string: close keeps the segment color (it lives on
		// this tape), count takes the next palette color (it describes how
		// many members follow, a separate fact).
		tag := byte(tape[c.absIdx] >> 56)
		if tag == valueabi.TagArrBeg || tag == valueabi.TagObjBeg {
			writeContainerPayload(b, c, tape, v, segOf)
			b.WriteString("\u2502")
			continue
		}
		content := padRight(payloadCellContent(c, tape, v), diagramCellW)
		b.WriteString(colorWrap(c.segID, content))
		b.WriteString("\u2502")
	}
	b.WriteByte('\n')
}

// writeContainerPayload renders a container start word's payload as
// "<close><count>" with each field in its own color. close is the absolute
// word index of the closing tag; count is the member count.
//
// Foreign containers (gap or outside [base,end)) belong to a different
// logical segment whose base we do not know, so their stored close cannot
// be converted to an absolute index. We show the raw stored value prefixed
// with "~" instead: it is still a useful landmark (the relative offset to
// this container's close within its own segment), and the "~" signals that
// it is not directly comparable to the absolute closes shown for in-region
// containers.
func writeContainerPayload(b *strings.Builder, c renderCell, tape []uint64, v *Value, segOf []int) {
	payload := tape[c.absIdx] & valueabi.PayloadMask
	count := int((payload >> 32) & 0xFFFFFF)
	closeStored := int(payload & 0xFFFFFFFF)

	inRegion := c.absIdx < len(segOf) && segOf[c.absIdx] >= 0
	var closeAbs int
	if !inRegion {
		// Foreign: show raw stored value with ~ prefix so it is visually
		// distinct from absolute closes in-region.
		closeAbs = closeStored
	} else {
		closeAbs = int(v.desc.Base) + closeStored
	}
	closeStr := strconv.Itoa(closeAbs)
	if !inRegion {
		closeStr = "~" + closeStr
	}
	countStr := strconv.Itoa(count)
	// Pad to diagramCellW so the cell border stays aligned. close gets the
	// segment color, count gets the next palette color (close+1 mod palette).
	totalRunes := utf8.RuneCountInString(closeStr) + utf8.RuneCountInString(countStr)
	if totalRunes > diagramCellW {
		// Rare: very large indices. Truncate count first (close is more useful).
		overflow := totalRunes - diagramCellW
		if overflow >= utf8.RuneCountInString(countStr) {
			countStr = ""
		} else {
			countStr = truncateRunes(countStr, utf8.RuneCountInString(countStr)-overflow)
		}
	}
	lead := diagramCellW - utf8.RuneCountInString(closeStr) - utf8.RuneCountInString(countStr)
	if lead > 0 {
		b.WriteString(strings.Repeat(" ", lead))
	}
	segID := c.segID
	if segID < 0 {
		segID = -1 // dim
	}
	b.WriteString(colorWrap(segID, closeStr))
	if countStr != "" {
		// Count uses the fixed green, not the cycling palette, so it is
		// always distinguishable from the close index regardless of segment.
		b.WriteString(ansiCount)
		b.WriteString(countStr)
		b.WriteString(ansiReset)
	}
}

func tagCellContent(c renderCell, tape []uint64) string {
	if c.isVal {
		return "" // value word: no tag char (the raw bits of the number)
	}
	// A seam carries no tag byte, only distances, so it is named rather than
	// rendered from its high byte (which is not a character).
	if valueabi.IsSeam(tape[c.absIdx]) {
		return "J"
	}
	tb := byte(tape[c.absIdx] >> 56)
	tag := string(rune(tb))
	// Object keys and values both render as `"` because both are JSON strings.
	// Keys get a `k` suffix so the two can be told apart in a dense tape; a
	// value string stays bare `"` since the tag itself already says "string".
	if c.isKey && valueabi.IsStringTag(tb) {
		return tag + "k"
	}
	return tag
}

func payloadCellContent(c renderCell, tape []uint64, v *Value) string {
	if c.isVal {
		tagIdx := c.absIdx - 1
		if tagIdx < 0 {
			return ""
		}
		switch byte(tape[tagIdx] >> 56) {
		case valueabi.TagInt64:
			return strconv.FormatInt(int64(tape[c.absIdx]), 10)
		case valueabi.TagUint64:
			return strconv.FormatUint(tape[c.absIdx], 10)
		case valueabi.TagDouble:
			return strconv.FormatFloat(math.Float64frombits(tape[c.absIdx]), 'g', -1, 64)
		}
		return ""
	}
	// Both distances, so the diagram shows a seam the rendered view keeps as well
	// as one it leaps over. "→1,1" is a reserved slot nothing has widened. Tested
	// before the tag switch: a seam's high byte is not a tag.
	if w := tape[c.absIdx]; valueabi.IsSeam(w) {
		return "\u2192" + strconv.Itoa(int(w&valueabi.SeamMask)) + "," + strconv.Itoa(int((w>>valueabi.SeamBits)&valueabi.SeamMask))
	}
	tag := byte(tape[c.absIdx] >> 56)
	payload := tape[c.absIdx] & valueabi.PayloadMask
	if valueabi.IsStringTag(tag) || tag == valueabi.TagNumRaw {
		return stringSummary(payload, tag, v)
	}
	switch tag {
	case valueabi.TagArrEnd, valueabi.TagObjEnd:
		// A dual shared root carries the inline projection's member count in
		// the close word's high24; the begin word's count is then the reserve
		// count. Show the inline count so the two are not confused.
		if n := int((payload >> 32) & 0xFFFFFF); n != 0 {
			return "i" + strconv.Itoa(n)
		}
		return ""
	case valueabi.TagTrue, valueabi.TagFalse, valueabi.TagNull:
		return ""
	case valueabi.TagInt64, valueabi.TagUint64, valueabi.TagDouble:
		return "" // value lives in the paired cell
	case valueabi.TagArrBeg, valueabi.TagObjBeg:
		// Container payload is rendered by writeContainerPayload, not here.
		return ""
	}
	return "?"
}

// stringSummary returns the decoded/source text for a string or num-raw word,
// sanitized to printable ASCII and truncated to fit (cellW-1 + …).
func stringSummary(payload uint64, tag byte, v *Value) string {
	off := uint32(payload & 0xFFFFFFFF)
	n := uint32((payload >> 32) & 0xFFFFFF)
	var buf []byte
	switch tag {
	case valueabi.TagString, valueabi.TagStrFree, valueabi.TagNumRaw:
		buf = v.desc.Doc.StrArena
	case valueabi.TagStrRaw:
		buf = v.desc.Doc.Src
	}
	if int(off)+int(n) > len(buf) {
		return "?OOB"
	}
	s := sanitizeASCII(string(buf[off : off+n]))
	maxRunes := diagramCellW - 1 // leave one cell for the ellipsis
	if utf8.RuneCountInString(s) > maxRunes {
		s = truncateRunes(s, maxRunes) + "\u2026" // …
	}
	return s
}

// sanitizeASCII replaces every rune outside [0x20,0x7f) with '?'. This keeps
// cell width equal to byte length: control bytes, ESC, box-drawing runes, and
// multi-byte UTF-8 cannot corrupt the grid. The faithful bytes remain in the
// arena for callers that need them.
func sanitizeASCII(s string) string {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r >= 0x20 && r < 0x7f {
			out = append(out, byte(r))
		} else {
			out = append(out, '?')
		}
	}
	return string(out)
}

func colorWrap(segID int, content string) string {
	if segID < 0 {
		return ansiDim + content + ansiReset
	}
	return diagramPalette[segID%len(diagramPalette)] + content + ansiReset
}

func padLeft(s string, n int) string {
	r := utf8.RuneCountInString(s)
	if r > n {
		return truncateRunes(s, n)
	}
	if r == n {
		return s
	}
	return strings.Repeat(" ", n-r) + s
}

// padRight right-aligns s in n columns, truncating with an ellipsis when s is
// too long (e.g. "=1234567" -> "=123…") so a wide value cannot push the next
// cell's border out of alignment.
func padRight(s string, n int) string {
	r := utf8.RuneCountInString(s)
	if r > n {
		if n <= 1 {
			return truncateRunes(s, n)
		}
		return truncateRunes(s, n-1) + "\u2026"
	}
	if r == n {
		return s
	}
	return s + strings.Repeat(" ", n-r)
}

func padCenter(s string, n int) string {
	r := utf8.RuneCountInString(s)
	if r > n {
		return truncateRunes(s, n)
	}
	if r == n {
		return s
	}
	total := n - r
	left := total / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", total-left)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
