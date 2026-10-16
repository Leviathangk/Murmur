package main

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
)

const mathExtensions = parser.Default | parser.ExtDollarMath

type mathKey struct{}

type mathText struct {
	doc       *parser.Document
	src, out  string
	streaming bool
}

func (a *app) markdown(c *ui.Context, src string, streaming bool) {
	box := ui.Column(c)
	t := *ui.Local(box, mathKey{}, func() *mathText { return &mathText{} })
	src = t.text(src, streaming)
	box.Children(func() {
		if streaming {
			markdown.Stream(c, src, &a.md)
		} else {
			markdown.View(c, src, &a.md)
		}
	})
}

func (a *app) formula(c *ui.Context, tex string) {
	th := markdown.DefaultTheme(c.Theme())
	if a.md.Theme != nil && a.md.Theme.FontSize > 0 {
		th.FontSize = a.md.Theme.FontSize
	}
	size := th.FontSize * 1.1
	b := a.formulas.get(c, tex, size, th.Font)
	if b.w <= 0 {
		return
	}
	col := th.Text
	pad := 0.2 * size
	ui.Row(c).Justify(ui.Center).Children(func() {
		ui.ScrollHorizontal(c).Shrink(1).Children(func() {
			ui.Box(c).Size(b.w+4, b.a+b.d+2*pad).Shrink(0).Label(tex).Draw(func(p *ui.Painter, r ui.Rect) {
				b.paint(p, r.X+2, r.Y+pad+b.a, col)
			})
		})
	})
}

func (t *mathText) text(src string, streaming bool) string {
	if src == t.src && streaming == t.streaming {
		return t.out
	}
	t.src, t.streaming, t.out = src, streaming, src
	if !strings.ContainsAny(src, `\$`) {
		return src
	}
	if t.doc == nil {
		t.doc = parser.New(mathExtensions)
	}
	t.doc.Set(src)
	t.out = typesetInline(t.doc, streaming)
	return t.out
}

type mathSpan struct {
	start, end int
	tex        string
}

func typesetInline(d *parser.Document, streaming bool) string {
	src := d.Source()
	dollars := d.Extensions()&parser.ExtDollarMath != 0
	var b strings.Builder
	last, cut := 0, len(src)
	for i := 1; i < d.Len(); i++ {
		bl := d.Block(i)
		switch bl.Kind {
		case parser.Paragraph, parser.Heading, parser.TableCell:
		default:
			continue
		}
		in := d.Inlines(i)
		spans := inlineSpans(d, i)
		from := bl.Start
		end := min(bl.End, len(src))
		hold := streaming && i == d.Len()-1 && strings.Trim(src[end:], " \t\r\n|") == ""
		if hold {
			end = min(end, openTick(src, bl.Start, end, in))
		}
		for _, m := range spans {
			if m.start < last {
				continue
			}
			if hold && m.start >= end {
				break
			}
			if hold && m.end == len(src) && src[m.end-1] == '$' && (m.end < 2 || src[m.end-2] != '$') {
				cut = m.start
				if cut > 0 && src[cut-1] == '$' {
					cut--
				}
				hold = false
				break
			}
			b.WriteString(src[last:m.start])
			b.WriteString(inlineMath(m.tex))
			last, from = m.end, m.end
		}
		if hold {
			if k := unclosedMath(src, max(from, last), end, in, dollars); k >= 0 {
				cut = k
			}
		}
	}
	if cut >= last {
		b.WriteString(src[last:cut])
	}
	return b.String()
}

func inlineSpans(d *parser.Document, i int) []mathSpan {
	src := d.Source()
	in := d.Inlines(i)
	var spans, singles []mathSpan
	for _, n := range in {
		if n.Kind != parser.Math {
			continue
		}
		m := mathSpan{n.Start, n.End, n.Text}
		if src[n.Start] == '$' && (n.End-n.Start < 2 || src[n.Start+1] != '$') {
			singles = append(singles, m)
		} else {
			spans = append(spans, m)
		}
	}
	if d.Extensions()&parser.ExtDollarMath != 0 {
		bl := d.Block(i)
		found := dollarSpans(src, bl.Start, min(bl.End, len(src)), in, spans)
		for _, m := range singles {
			if !slices.ContainsFunc(found, func(f mathSpan) bool { return f.start < m.end && m.start < f.end }) {
				found = append(found, m)
			}
		}
		spans = append(spans, found...)
	}
	slices.SortFunc(spans, func(a, b mathSpan) int { return a.start - b.start })
	return spans
}

func openTick(src string, from, to int, in []parser.Inline) int {
	for i := from; i < to; i++ {
		if src[i] == '`' && (i == 0 || src[i-1] != '\\') && !covered(in, i) {
			return i
		}
	}
	return to
}

func mathLike(s string) bool {
	if len(s) > 80 {
		return false
	}
	run := 0
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '\\':
			i += w
			for i < len(s) && isLetter(s[i]) {
				i++
			}
			run = 0
			continue
		case r == '{':
			if j := strings.IndexByte(s[i:], '}'); j >= 0 {
				i += j
			} else {
				i = len(s) - 1
			}
			run = 0
		case r < 0x80 && isLetter(byte(r)):
			run++
			if run >= 2 {
				return false
			}
		case isTextRune(r):
			return false
		default:
			run = 0
		}
		i += w
	}
	return true
}

func covered(in []parser.Inline, i int) bool {
	for _, n := range in {
		if (n.Kind == parser.Code || n.Kind == parser.HTML) && i >= n.Start && i < n.End {
			return true
		}
	}
	return false
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func dollarSpans(src string, from, to int, in []parser.Inline, kept []mathSpan) []mathSpan {
	inKept := func(i int) bool {
		return slices.ContainsFunc(kept, func(m mathSpan) bool { return i >= m.start && i < m.end })
	}
	var out []mathSpan
	for i := from; i < to; i++ {
		if src[i] != '$' {
			continue
		}
		if i+1 < to && src[i+1] == '$' {
			i++
			continue
		}
		if i > 0 && src[i-1] == '\\' || i+1 >= to || src[i+1] == ' ' || src[i+1] == '\n' || covered(in, i) || inKept(i) {
			continue
		}
		end := -1
		for j := i + 1; j < to; j++ {
			if src[j] == '$' && src[j-1] != '\\' {
				end = j
				break
			}
		}
		if end < 0 {
			break
		}
		tex := src[i+1 : end]
		if src[end-1] == ' ' || src[end-1] == '\n' || end+1 < len(src) && isDigit(src[end+1]) || isDigit(src[i+1]) && !strings.ContainsAny(tex, `\^_{`) {
			continue
		}
		if slices.ContainsFunc(in, func(n parser.Inline) bool {
			return (n.Kind == parser.Code || n.Kind == parser.HTML) && n.Start < end && i < n.End
		}) || inKept(end) {
			continue
		}
		out = append(out, mathSpan{i, end + 1, tex})
		i = end
	}
	return out
}

func unclosedMath(src string, from, to int, in []parser.Inline, dollars bool) int {
	to = min(to, len(src))
	for i := from; i < to; i++ {
		c := src[i]
		opens := false
		switch {
		case c == '\\' && i+1 < to && (src[i+1] == '(' || src[i+1] == '['):
			opens = i == 0 || src[i-1] != '\\'
		case c == '\\' && i+1 == to && to == len(src):
			opens = true
		case c == '$' && i+1 < to && src[i+1] == '$':
			opens = !strings.Contains(src[i+2:to], "$$")
		case c == '$' && dollars && (i == 0 || src[i-1] != '\\'):
			rest := src[i+1 : to]
			switch {
			case strings.IndexByte(rest, '$') >= 0:
			case rest == "":
				opens = true
			case isDigit(rest[0]):
				opens = mathLike(rest)
			default:
				opens = rest[0] != ' '
			}
		}
		if opens && !covered(in, i) {
			return i
		}
	}
	return -1
}

func plainFormulas(text string) string {
	if !strings.ContainsAny(text, `\$`) {
		return text
	}
	d := parser.New(mathExtensions)
	d.Set(text)
	var b strings.Builder
	last := 0
	for i := 1; i < d.Len(); i++ {
		bl := d.Block(i)
		switch bl.Kind {
		case parser.MathBlock:
			if bl.Start >= last {
				b.WriteString(text[last:bl.Start])
				b.WriteString(plainMath(d.Lines(i)))
				last = min(bl.End, len(text))
				rest := text[last:]
				if trimmed := strings.TrimLeft(rest, " \t\r\n"); strings.HasPrefix(trimmed, `\]`) || strings.HasPrefix(trimmed, "$$") {
					last += len(rest) - len(trimmed) + 2
				}
			}
		case parser.Paragraph, parser.Heading, parser.TableCell:
			for _, m := range inlineSpans(d, i) {
				if m.start >= last {
					b.WriteString(text[last:m.start])
					b.WriteString(plainMath(m.tex))
					last = m.end
				}
			}
		}
	}
	b.WriteString(text[last:])
	return b.String()
}

type piece struct {
	s      string
	italic bool
}

type linear struct {
	out []piece
}

func inlineMath(tex string) string {
	var l linear
	l.node(parseTeX(tex), false)
	return l.markdown()
}

func plainMath(tex string) string {
	var l linear
	l.node(parseTeX(tex), false)
	return l.plain()
}

func (l *linear) put(s string, italic bool) {
	if s != "" {
		l.out = append(l.out, piece{s, italic})
	}
}

func (l *linear) space() {
	if n := len(l.out); n > 0 && !strings.HasSuffix(l.out[n-1].s, " ") && !strings.HasSuffix(l.out[n-1].s, "\u2003") {
		l.out = append(l.out, piece{s: " "})
	}
}

func (l *linear) plain() string {
	var b strings.Builder
	for _, p := range l.out {
		b.WriteString(p.s)
	}
	return collapseSpaces(b.String())
}

func collapseSpaces(s string) string {
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}

func (l *linear) markdown() string {
	var b strings.Builder
	italic, first := false, true
	text := collapsePieces(l.out)
	for _, p := range text {
		if strings.TrimSpace(p.s) == "" {
			p.italic = false
		}
		if p.italic != italic {
			if italic {
				b.WriteString("</i>")
			} else {
				b.WriteString("<i>")
			}
			italic = p.italic
		}
		for _, r := range p.s {
			switch {
			case first && r >= '0' && r <= '9':
				fmt.Fprintf(&b, "&#%d;", r)
			case r == '@':
				b.WriteString("&#64;")
			case r == '[':
				b.WriteString("&#91;")
			case r == ']':
				b.WriteString("&#93;")
			case r == '(' && first:
				b.WriteString("&#40;")
			case r == '(' || r == ')':
				b.WriteRune(r)
			case r < 0x80 && strings.ContainsRune("!\"#$%&'*+,-./:;<=>?@\\^_`{|}~", r):
				b.WriteByte('\\')
				b.WriteRune(r)
			default:
				b.WriteRune(r)
			}
			first = false
		}
	}
	if italic {
		b.WriteString("</i>")
	}
	return b.String()
}

func collapsePieces(in []piece) []piece {
	var out []piece
	space := true
	for _, p := range in {
		s := p.s
		if space {
			s = strings.TrimLeft(s, " ")
		}
		for strings.Contains(s, "  ") {
			s = strings.ReplaceAll(s, "  ", " ")
		}
		if s == "" {
			continue
		}
		space = strings.HasSuffix(s, " ")
		out = append(out, piece{s, p.italic})
	}
	for len(out) > 0 {
		last := &out[len(out)-1]
		last.s = strings.TrimRight(last.s, " ")
		if last.s != "" {
			break
		}
		out = out[:len(out)-1]
	}
	return out
}

func (l *linear) sub(n *texNode, script bool) []piece {
	var s linear
	s.node(n, script)
	return collapsePieces(s.out)
}

func piecesText(ps []piece) string {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString(p.s)
	}
	return b.String()
}

func (l *linear) node(n *texNode, script bool) {
	if n == nil {
		return
	}
	if n.kind == texRow {
		l.row(n.kids, script, false)
		return
	}
	l.row([]*texNode{n}, script, false)
}

func (l *linear) row(kids []*texNode, script, leading bool) {
	parts := flatten(kids, mstate{}, nil)
	cls := classes(parts, leading)
	prev := texClass(255)
	if leading {
		prev = texOrd
	}
	for i, p := range parts {
		if p.n.kind == texSpace {
			l.spaceOf(p.n.size)
			continue
		}
		if prev != 255 && !script && spacing(prev, cls[i], styleText) > 0 && !l.signed(prev) {
			l.space()
		}
		l.item(p.n, script)
		prev = cls[i]
	}
}

func (l *linear) signed(prev texClass) bool {
	if prev != texOrd || len(l.out) == 0 {
		return false
	}
	s := l.out[len(l.out)-1].s
	return strings.HasSuffix(s, "−") || strings.HasSuffix(s, "+") || strings.HasSuffix(s, "±")
}

func (l *linear) spaceOf(em float32) {
	switch {
	case em >= 0.9:
		l.out = append(l.out, piece{s: strings.Repeat("\u2003", min(int(em+0.1), 4))})
	case em > 0.1:
		l.space()
	}
}

func (l *linear) item(n *texNode, script bool) {
	switch n.kind {
	case texRow:
		l.row(n.kids, script, false)
	case texAtom:
		if n.size > 0 && n.text == "" {
			return
		}
		l.put(n.text, atomItalic(n))
	case texSpace:
		l.spaceOf(n.size)
	case texFrac:
		num, den := l.sub(n.over, script), l.sub(n.under, script)
		if n.mark == "nobar" {
			if n.left != "" {
				l.put("C(", false)
				l.out = append(l.out, num...)
				l.put(", ", false)
				l.out = append(l.out, den...)
				l.put(")", false)
				return
			}
			l.out = append(l.out, num...)
			l.put(" ", false)
			l.out = append(l.out, den...)
			return
		}
		l.group(num, !simpleNode(n.over, true))
		l.put("/", false)
		l.group(den, !simpleNode(n.under, false))
	case texSqrt:
		body := l.sub(n.body, script)
		idx := l.sub(n.over, true)
		switch piecesText(idx) {
		case "":
			l.put("√", false)
		case "3":
			l.put("∛", false)
		case "4":
			l.put("∜", false)
		default:
			l.script(idx, true)
			l.put("√", false)
		}
		l.group(body, !simpleNode(n.body, false))
	case texScripts:
		l.node(n.body, script)
		if n.under != nil {
			if limitsOf(n, mstate{level: styleDisplay}) && n.body.kind == texAtom && !n.body.large {
				l.limit(l.sub(n.under, true))
			} else {
				l.script(l.sub(n.under, true), false)
			}
		}
		if n.over != nil {
			l.script(l.sub(n.over, true), true)
		}
	case texFence:
		l.put(n.left, false)
		l.node(n.body, script)
		l.put(n.right, false)
	case texAccent:
		l.accent(n, script)
	case texStack:
		l.node(n.body, script)
		if n.under != nil {
			l.script(l.sub(n.under, true), false)
		}
		if n.over != nil {
			l.script(l.sub(n.over, true), true)
		}
	case texArrow:
		l.arrow(n)
	case texTable:
		l.table(n, script)
	case texBox, texColor, texStyle, texCancel:
		l.node(n.body, script)
	}
}

func (l *linear) group(ps []piece, parens bool) {
	if parens {
		l.put("(", false)
	}
	l.out = append(l.out, ps...)
	if parens {
		l.put(")", false)
	}
}

func simpleNode(n *texNode, loose bool) bool {
	if n == nil {
		return true
	}
	switch n.kind {
	case texAtom:
		return n.class != texBin && n.class != texRel && n.class != texPunct && !strings.Contains(n.text, " ")
	case texRow:
		if len(n.kids) <= 1 {
			return len(n.kids) == 0 || simpleNode(n.kids[0], loose)
		}
		if !loose {
			return false
		}
		for _, k := range n.kids {
			if k.kind != texAtom && k.kind != texScripts || k.class != texOrd || !simpleNode(k, false) {
				return false
			}
		}
		return true
	case texScripts:
		return simpleNode(n.body, false)
	case texFence, texSqrt, texAccent:
		return true
	case texColor, texStyle, texBox:
		return simpleNode(n.body, loose)
	}
	return false
}

var superscripts = map[rune]rune{
	'0': '⁰', '1': '¹', '2': '²', '3': '³', '4': '⁴', '5': '⁵', '6': '⁶', '7': '⁷', '8': '⁸', '9': '⁹',
	'+': '⁺', '−': '⁻', '-': '⁻', '=': '⁼', '(': '⁽', ')': '⁾', 'n': 'ⁿ', 'i': 'ⁱ', 'T': 'ᵀ',
	'∘': '°', '°': '°', ',': ',', 'x': 'ˣ', 'k': 'ᵏ', 'm': 'ᵐ', 'a': 'ᵃ', 'b': 'ᵇ', 'c': 'ᶜ', 'd': 'ᵈ', 'e': 'ᵉ',
	'j': 'ʲ', 'r': 'ʳ', 't': 'ᵗ', 'y': 'ʸ',
}

var subscripts = map[rune]rune{
	'0': '₀', '1': '₁', '2': '₂', '3': '₃', '4': '₄', '5': '₅', '6': '₆', '7': '₇', '8': '₈', '9': '₉',
	'+': '₊', '−': '₋', '-': '₋', '=': '₌', '(': '₍', ')': '₎', ',': ',', 'a': 'ₐ', 'e': 'ₑ', 'i': 'ᵢ', 'j': 'ⱼ',
	'k': 'ₖ', 'n': 'ₙ', 'x': 'ₓ', 'm': 'ₘ', 'o': 'ₒ', 'r': 'ᵣ', 't': 'ₜ', 'p': 'ₚ', 's': 'ₛ', 'l': 'ₗ',
	'h': 'ₕ', 'u': 'ᵤ', 'v': 'ᵥ',
}

func (l *linear) script(ps []piece, up bool) {
	s := piecesText(ps)
	if s == "" {
		return
	}
	if up {
		primes := strings.TrimLeft(s, "′″‴")
		if len(primes) < len(s) {
			l.put(s[:len(s)-len(primes)], false)
			s = primes
			ps = []piece{{s: s}}
			if s == "" {
				return
			}
		}
	}
	table := subscripts
	if up {
		table = superscripts
	}
	mapped := make([]rune, 0, len(s))
	ok := true
	for _, r := range strings.ReplaceAll(s, " ", "") {
		m, found := table[r]
		if !found {
			ok = false
			break
		}
		mapped = append(mapped, m)
	}
	if ok {
		l.put(string(mapped), false)
		return
	}
	if up {
		l.put("^", false)
	} else {
		l.put("_", false)
	}
	l.group(ps, utf8.RuneCountInString(s) > 1)
}

func (l *linear) limit(ps []piece) {
	s := piecesText(ps)
	mapped := make([]rune, 0, len(s))
	for _, r := range strings.ReplaceAll(s, " ", "") {
		m, ok := subscripts[r]
		if !ok {
			l.group(ps, true)
			return
		}
		mapped = append(mapped, m)
	}
	l.put(string(mapped), false)
}

var combining = map[string]string{
	"hat": "\u0302", "widehat": "\u0302", "check": "\u030c", "widecheck": "\u030c", "breve": "\u0306",
	"acute": "\u0301", "grave": "\u0300", "tilde": "\u0303", "widetilde": "\u0303", "bar": "\u0304",
	"overline": "\u0305", "underline": "\u0332", "vec": "\u20d7", "overrightarrow": "\u20d7",
	"Overrightarrow": "\u20d7", "overleftarrow": "\u20d6", "overleftrightarrow": "\u20e1", "dot": "\u0307",
	"ddot": "\u0308", "dddot": "\u20db", "mathring": "\u030a", "utilde": "\u0330", "underrightarrow": "\u20ef",
	"overparen": "\u0311", "overarc": "\u0311", "overgroup": "\u0311", "underparen": "\u032e",
}

func (l *linear) accent(n *texNode, script bool) {
	ps := l.sub(n.body, script)
	mark, ok := combining[n.mark]
	if !ok || len(ps) == 0 {
		l.out = append(l.out, ps...)
		return
	}
	every := n.mark == "overline" || n.mark == "underline"
	for i, p := range ps {
		var b strings.Builder
		for j, r := range p.s {
			b.WriteRune(r)
			lastRune := i == len(ps)-1 && j+utf8.RuneLen(r) == len(p.s)
			if every && r != ' ' || !every && lastRune {
				b.WriteString(mark)
			}
		}
		l.put(b.String(), p.italic)
	}
}

func (l *linear) arrow(n *texNode) {
	glyph := map[string]string{
		"xrightarrow": "→", "xleftarrow": "←", "xRightarrow": "⇒", "xLeftarrow": "⇐", "xleftrightarrow": "↔",
		"xLeftrightarrow": "⇔", "xlongequal": "=", "xrightleftharpoons": "⇌", "xleftrightharpoons": "⇋",
		"xmapsto": "↦", "xhookrightarrow": "↪", "xhookleftarrow": "↩", "xtofrom": "⇄", "xtwoheadrightarrow": "↠",
		"xrightharpoonup": "⇀", "xrightharpoondown": "⇁", "xleftharpoonup": "↼", "xleftharpoondown": "↽",
	}[n.mark]
	if glyph == "" {
		glyph = "→"
	}
	label := l.sub(n.over, false)
	if under := l.sub(n.under, false); len(under) > 0 {
		if len(label) > 0 {
			label = append(label, piece{s: ", "})
		}
		label = append(label, under...)
	}
	if len(label) == 0 {
		l.put(glyph, false)
		return
	}
	switch glyph {
	case "→", "⇒", "↦", "↪", "↠", "⇀", "⇁":
		l.put("—", false)
		l.out = append(l.out, label...)
		l.put(glyph, false)
	case "←", "⇐", "↩", "↼", "↽":
		l.put(glyph, false)
		l.out = append(l.out, label...)
		l.put("—", false)
	case "=":
		l.put("=", false)
		l.out = append(l.out, label...)
		l.put("=", false)
	default:
		l.put(glyph, false)
		l.put("(", false)
		l.out = append(l.out, label...)
		l.put(")", false)
	}
}

func (l *linear) table(n *texNode, script bool) {
	l.put(n.left, false)
	if n.left != "" && n.mark == "cases" {
		l.put(" ", false)
	}
	for i, r := range n.rows {
		if i > 0 {
			l.put("; ", false)
		}
		for j, cell := range r {
			if j > 0 && (n.mark != "aligned" || j%2 == 0) {
				l.put(" ", false)
			}
			if cell == nil {
				continue
			}
			kids := cell.kids
			if cell.kind != texRow {
				kids = []*texNode{cell}
			}
			l.row(kids, script, n.mark == "aligned" && j%2 == 1)
		}
	}
	l.put(n.right, false)
}
