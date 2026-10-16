package main

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type texKind uint8

const (
	texRow texKind = iota
	texAtom
	texFrac
	texSqrt
	texScripts
	texSpace
	texFence
	texAccent
	texStack
	texArrow
	texTable
	texBox
	texColor
	texStyle
	texPhantom
	texCancel
)

type texClass uint8

const (
	texOrd texClass = iota
	texOp
	texBin
	texRel
	texOpen
	texClose
	texPunct
	texInner
)

type texFont uint8

const (
	fontAuto texFont = iota
	fontRoman
	fontItalic
	fontBold
	fontBoldItalic
	fontText
	fontTextItalic
	fontTextBold
	fontMono
)

type texNode struct {
	kind   texKind
	class  texClass
	font   texFont
	text   string
	kids   []*texNode
	body   *texNode
	over   *texNode
	under  *texNode
	rows   [][]*texNode
	left   string
	right  string
	mark   string
	align  string
	color  string
	size   float32
	style  int8
	limits int8
	large  bool
	small  bool
}

const (
	styleDisplay int8 = iota
	styleText
	styleScript
	styleScriptScript
)

const (
	inTop = iota
	inGroup
	inFence
	inCell
	inSub
	inArg
)

const texMaxDepth = 48

type texParser struct {
	s     string
	i     int
	depth int
	tag   string
}

func parseTeX(s string) *texNode {
	p := &texParser{s: s}
	rows := p.cells(false)
	var n *texNode
	if len(rows) == 1 && len(rows[0]) == 1 {
		n = rows[0][0]
	} else {
		n = tableNode(rows, "gathered")
		for _, r := range rows {
			if len(r) > 1 {
				n.mark, n.align = "aligned", "rl"
				break
			}
		}
	}
	if p.tag != "" {
		n = rowNode([]*texNode{n, {kind: texSpace, size: 2}, textNode("("+p.tag+")", fontText)})
	}
	return n
}

func parseTeXIn(s string, depth int) []*texNode {
	p := &texParser{s: s, depth: depth}
	return p.list(inSub)
}

func rowNode(kids []*texNode) *texNode {
	return &texNode{kind: texRow, kids: kids}
}

func atomNode(text string, class texClass) *texNode {
	return &texNode{kind: texAtom, text: text, class: class}
}

func textNode(text string, font texFont) *texNode {
	return &texNode{kind: texAtom, text: text, font: font}
}

func tableNode(rows [][]*texNode, mark string) *texNode {
	return &texNode{kind: texTable, rows: rows, mark: mark, class: texInner}
}

func (p *texParser) eof() bool { return p.i >= len(p.s) }

func (p *texParser) space() {
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func (p *texParser) scanName(from int) (string, int) {
	i := from + 1
	if i >= len(p.s) {
		return "", i
	}
	if !isLetter(p.s[i]) {
		_, w := utf8.DecodeRuneInString(p.s[i:])
		return p.s[i : i+w], i + w
	}
	j := i
	for j < len(p.s) && isLetter(p.s[j]) {
		j++
	}
	return p.s[i:j], j
}

func (p *texParser) peekName() string {
	if p.eof() || p.s[p.i] != '\\' {
		return ""
	}
	name, _ := p.scanName(p.i)
	return name
}

func (p *texParser) readName() string {
	name, j := p.scanName(p.i)
	p.i = j
	if name != "" && isLetter(name[0]) {
		p.space()
	}
	return name
}

func (p *texParser) star() bool {
	if !p.eof() && p.s[p.i] == '*' {
		p.i++
		return true
	}
	return false
}

func (p *texParser) cells(env bool) [][]*texNode {
	mode := inTop
	if env {
		mode = inCell
	}
	var rows [][]*texNode
	var row []*texNode
	for {
		row = append(row, rowNode(p.list(mode)))
		p.space()
		if p.eof() {
			break
		}
		if p.s[p.i] == '&' {
			p.i++
			continue
		}
		name := p.readName()
		if name == "end" {
			p.raw()
			break
		}
		p.optional()
		rows = append(rows, row)
		row = nil
	}
	if len(row) > 1 || len(row[0].kids) > 0 || len(rows) == 0 {
		rows = append(rows, row)
	}
	return rows
}

func (p *texParser) list(mode int) []*texNode {
	var out []*texNode
	for {
		p.space()
		if p.eof() {
			return out
		}
		switch p.s[p.i] {
		case '}':
			if mode == inGroup || mode == inFence {
				return out
			}
			p.i++
			continue
		case '&':
			if mode == inTop || mode == inCell || mode == inFence {
				return out
			}
			p.i++
			continue
		case '\\':
			switch name := p.peekName(); name {
			case "\\", "cr", "newline":
				if mode == inTop || mode == inCell || mode == inFence {
					return out
				}
				p.readName()
				p.optional()
				continue
			case "end":
				if mode == inGroup || mode == inFence || mode == inCell {
					return out
				}
				p.readName()
				p.raw()
				continue
			case "right":
				if mode == inFence {
					return out
				}
				p.readName()
				p.delim()
				continue
			case "over", "atop", "choose", "above":
				p.readName()
				if name == "above" {
					p.length()
				}
				num, den := rowNode(out), rowNode(p.list(mode))
				f := &texNode{kind: texFrac, over: num, under: den, class: texInner}
				switch name {
				case "atop":
					f.mark = "nobar"
				case "choose":
					f.mark, f.left, f.right = "nobar", "(", ")"
				}
				return []*texNode{f}
			}
		}
		if n := p.atom(mode); n != nil {
			out = append(out, n)
		}
	}
}

func (p *texParser) atom(mode int) *texNode {
	return p.scripts(p.base(mode))
}

func (p *texParser) scripts(base *texNode) *texNode {
	var sup, sub *texNode
	var limits int8
	primes := 0
	for {
		p.space()
		if p.eof() {
			break
		}
		c := p.s[p.i]
		if c == '^' || c == '_' {
			p.i++
			arg := p.arg()
			if c == '^' {
				sup = joinNodes(sup, arg)
			} else {
				sub = joinNodes(sub, arg)
			}
			continue
		}
		if c == '\'' && sup == nil {
			p.i++
			primes++
			continue
		}
		if name := p.peekName(); name == "limits" || name == "nolimits" || name == "displaylimits" {
			p.readName()
			switch name {
			case "limits":
				limits = 1
			case "nolimits":
				limits = -1
			}
			continue
		}
		break
	}
	if primes > 0 {
		base = joinNodes(base, atomNode(strings.Repeat("′", primes), texOrd))
	}
	if sup == nil && sub == nil {
		if limits != 0 && base != nil {
			base.limits = limits
		}
		return base
	}
	if base == nil {
		base = rowNode(nil)
	}
	return &texNode{kind: texScripts, body: base, over: sup, under: sub, limits: limits, class: base.class}
}

func joinNodes(a, b *texNode) *texNode {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	}
	return rowNode([]*texNode{a, b})
}

func (p *texParser) arg() *texNode {
	p.space()
	if p.eof() {
		return rowNode(nil)
	}
	switch c := p.s[p.i]; c {
	case '{':
		return p.group()
	case '\\':
		if n := p.command(inArg); n != nil {
			return n
		}
		return rowNode(nil)
	case '}', '&', '^', '_':
		return rowNode(nil)
	}
	r, w := utf8.DecodeRuneInString(p.s[p.i:])
	p.i += w
	return p.char(r)
}

func (p *texParser) group() *texNode {
	p.i++
	p.depth++
	kids := p.list(inGroup)
	p.depth--
	if !p.eof() && p.s[p.i] == '}' {
		p.i++
	}
	return rowNode(kids)
}

func (p *texParser) base(mode int) *texNode {
	if p.depth > texMaxDepth {
		r, w := utf8.DecodeRuneInString(p.s[p.i:])
		p.i += w
		return textNode(string(r), fontText)
	}
	c := p.s[p.i]
	switch {
	case c == '{':
		return p.group()
	case c == '\\':
		return p.command(mode)
	case c == '^' || c == '_':
		return nil
	case c == '~':
		p.i++
		return &texNode{kind: texSpace, size: 0.25}
	case c == '\'':
		p.i++
		return atomNode("′", texOrd)
	case c >= '0' && c <= '9' || c == '.' && p.i+1 < len(p.s) && p.s[p.i+1] >= '0' && p.s[p.i+1] <= '9':
		j := p.i
		for j < len(p.s) && (p.s[j] >= '0' && p.s[j] <= '9' || p.s[j] == '.' && j+1 < len(p.s) && p.s[j+1] >= '0' && p.s[j+1] <= '9') {
			j++
		}
		n := atomNode(p.s[p.i:j], texOrd)
		p.i = j
		return n
	}
	r, w := utf8.DecodeRuneInString(p.s[p.i:])
	if isTextRune(r) {
		j := p.i
		for j < len(p.s) {
			r, w := utf8.DecodeRuneInString(p.s[j:])
			if !isTextRune(r) {
				break
			}
			j += w
		}
		n := textNode(p.s[p.i:j], fontText)
		p.i = j
		return n
	}
	p.i += w
	return p.char(r)
}

func isTextRune(r rune) bool {
	if r < 0x80 || unicode.Is(unicode.Greek, r) {
		return false
	}
	return unicode.IsLetter(r) || unicode.Is(unicode.Han, r) || unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Hangul) || r >= 0x3000 && r <= 0x303f || r >= 0xff00 && r <= 0xffef
}

func (p *texParser) char(r rune) *texNode {
	switch r {
	case '-':
		return atomNode("−", texBin)
	case '*':
		return atomNode("∗", texBin)
	}
	if r < 0x80 {
		if cl, ok := asciiClass[byte(r)]; ok {
			return atomNode(string(r), cl)
		}
		return atomNode(string(r), texOrd)
	}
	if cl, ok := unicodeClass[r]; ok {
		n := atomNode(string(r), cl)
		if cl == texOp {
			n.large = true
			n.limits = opLimits(string(r))
		}
		return n
	}
	if isTextRune(r) {
		return textNode(string(r), fontText)
	}
	return atomNode(string(r), texOrd)
}

func opLimits(s string) int8 {
	switch s {
	case "∫", "∬", "∭", "∮", "∯", "∰":
		return -1
	}
	return 1
}

var asciiClass = map[byte]texClass{
	'+': texBin, '=': texRel, '<': texRel, '>': texRel, ':': texRel,
	',': texPunct, ';': texPunct, '!': texClose, '?': texClose,
	'(': texOpen, '[': texOpen, ')': texClose, ']': texClose,
}

var unicodeClass = map[rune]texClass{
	'×': texBin, '÷': texBin, '±': texBin, '∓': texBin, '·': texBin, '⋅': texBin, '∘': texBin, '∙': texBin,
	'⊕': texBin, '⊗': texBin, '∩': texBin, '∪': texBin, '∧': texBin, '∨': texBin, '∖': texBin, '−': texBin,
	'≠': texRel, '≤': texRel, '≥': texRel, '≈': texRel, '≡': texRel, '∼': texRel, '≃': texRel, '≅': texRel,
	'∝': texRel, '∈': texRel, '∉': texRel, '∋': texRel, '⊂': texRel, '⊃': texRel, '⊆': texRel, '⊇': texRel,
	'→': texRel, '←': texRel, '↔': texRel, '⇒': texRel, '⇐': texRel, '⇔': texRel, '⟶': texRel, '⟵': texRel,
	'⟹': texRel, '⟸': texRel, '⟺': texRel, '↦': texRel, '≪': texRel, '≫': texRel, '⊥': texRel, '∥': texRel,
	'∣': texRel, '≜': texRel, '≐': texRel, '⇌': texRel, '⩽': texRel, '⩾': texRel, '≦': texRel, '≧': texRel,
	'∵': texRel, '∴': texRel, '≌': texRel, '∽': texRel,
	'⟨': texOpen, '⌊': texOpen, '⌈': texOpen, '⟩': texClose, '⌋': texClose, '⌉': texClose,
	'∑': texOp, '∏': texOp, '∐': texOp, '∫': texOp, '∬': texOp, '∭': texOp, '∮': texOp, '⋃': texOp, '⋂': texOp,
}

func (p *texParser) command(mode int) *texNode {
	name := p.readName()
	if name == "" {
		return nil
	}
	if s, ok := texSymbols[name]; ok {
		return atomNode(s.text, s.class)
	}
	if s, ok := texLargeOps[name]; ok {
		return &texNode{kind: texAtom, text: s, class: texOp, large: true, limits: opLimits(s)}
	}
	if lim, ok := texFunctions[name]; ok {
		return &texNode{kind: texAtom, text: name, class: texOp, font: fontRoman, limits: lim}
	}
	if w, ok := texSpaces[name]; ok {
		return &texNode{kind: texSpace, size: w}
	}
	if _, ok := texAccents[name]; ok {
		return &texNode{kind: texAccent, mark: name, body: p.arg()}
	}
	if f, ok := texFonts[name]; ok {
		return p.font(name, f)
	}
	if _, ok := texArrows[name]; ok {
		under := p.optional()
		n := &texNode{kind: texArrow, mark: name, over: p.arg(), class: texRel}
		if under != "" {
			n.under = rowNode(parseTeXIn(under, p.depth+1))
		}
		return n
	}
	switch name {
	case "frac", "dfrac", "tfrac", "cfrac", "binom", "dbinom", "tbinom":
		n := &texNode{kind: texFrac, class: texInner}
		n.over = p.arg()
		n.under = p.arg()
		if strings.HasPrefix(name, "d") || name == "cfrac" {
			n.style = 1
		} else if strings.HasPrefix(name, "t") {
			n.style = 2
		}
		if strings.HasSuffix(name, "binom") {
			n.mark, n.left, n.right = "nobar", "(", ")"
		}
		return n
	case "sqrt":
		n := &texNode{kind: texSqrt}
		if idx := p.optional(); idx != "" {
			n.over = rowNode(parseTeXIn(idx, p.depth+1))
		}
		n.body = p.arg()
		return n
	case "left":
		l := p.delim()
		p.depth++
		body := p.list(inFence)
		p.depth--
		r := ""
		if p.peekName() == "right" {
			p.readName()
			r = p.delim()
		}
		return &texNode{kind: texFence, left: l, right: r, body: rowNode(body), class: texInner}
	case "middle":
		return atomNode(p.delim(), texRel)
	case "big", "Big", "bigg", "Bigg", "bigl", "Bigl", "biggl", "Biggl", "bigr", "Bigr", "biggr", "Biggr", "bigm", "Bigm", "biggm", "Biggm":
		n := atomNode(p.delim(), texOrd)
		switch {
		case strings.HasPrefix(name, "Bigg"):
			n.size = 3
		case strings.HasPrefix(name, "bigg"):
			n.size = 2.4
		case strings.HasPrefix(name, "Big"):
			n.size = 1.8
		default:
			n.size = 1.2
		}
		switch name[len(name)-1] {
		case 'l':
			n.class = texOpen
		case 'r':
			n.class = texClose
		case 'm':
			n.class = texRel
		}
		return n
	case "text", "textrm", "textnormal", "textup", "textmd", "mbox", "hbox", "textsf", "textit", "emph", "textsl", "textbf", "texttt":
		font := fontText
		switch name {
		case "textit", "emph", "textsl":
			font = fontTextItalic
		case "textbf":
			font = fontTextBold
		case "texttt":
			font = fontMono
		}
		return p.text(p.raw(), font)
	case "operatorname", "operatornamewithlimits":
		lim := int8(-1)
		if p.star() || name == "operatornamewithlimits" {
			lim = 1
		}
		body := p.arg()
		return &texNode{kind: texAtom, text: plainTeX(body), class: texOp, font: fontRoman, limits: lim}
	case "mathop", "mathrel", "mathbin", "mathord", "mathopen", "mathclose", "mathpunct", "mathinner":
		body := p.arg()
		body.class = map[string]texClass{"mathop": texOp, "mathrel": texRel, "mathbin": texBin, "mathord": texOrd, "mathopen": texOpen, "mathclose": texClose, "mathpunct": texPunct, "mathinner": texInner}[name]
		if body.class == texOp {
			body.limits = 1
		}
		return body
	case "overset", "underset", "stackrel":
		label := p.arg()
		body := p.arg()
		n := &texNode{kind: texStack, body: body, class: body.class}
		if name == "underset" {
			n.under = label
		} else {
			n.over = label
		}
		if name == "stackrel" {
			n.class = texRel
		}
		if n.class == texOrd && len(body.kids) == 1 {
			n.class = body.kids[0].class
		}
		return n
	case "overbrace", "underbrace", "overbracket", "underbracket":
		return &texNode{kind: texStack, mark: name, body: p.arg(), limits: 1}
	case "boxed", "fbox", "framebox":
		if name == "boxed" {
			return &texNode{kind: texBox, body: p.arg()}
		}
		return &texNode{kind: texBox, body: p.text(p.raw(), fontText)}
	case "colorbox":
		c := p.raw()
		return &texNode{kind: texBox, color: c, body: p.text(p.raw(), fontText)}
	case "fcolorbox":
		p.raw()
		c := p.raw()
		return &texNode{kind: texBox, color: c, body: p.text(p.raw(), fontText)}
	case "color":
		c := p.raw()
		if mode == inArg {
			return &texNode{kind: texColor, color: c, body: rowNode(nil)}
		}
		return &texNode{kind: texColor, color: c, body: rowNode(p.list(mode))}
	case "textcolor":
		c := p.raw()
		return &texNode{kind: texColor, color: c, body: p.arg()}
	case "displaystyle", "textstyle", "scriptstyle", "scriptscriptstyle":
		if mode == inArg {
			return nil
		}
		level := map[string]int8{"displaystyle": styleDisplay, "textstyle": styleText, "scriptstyle": styleScript, "scriptscriptstyle": styleScriptScript}[name]
		return &texNode{kind: texStyle, style: level, body: rowNode(p.list(mode))}
	case "bold", "frak":
		f := map[string]string{"bold": "mathbf", "frak": "mathfrak"}[name]
		return p.font(f, texFonts[f])
	case "rm", "bf", "it", "sf", "tt", "cal", "mit":
		if mode == inArg {
			return nil
		}
		f := map[string]string{"rm": "mathrm", "bf": "mathbf", "it": "mathit", "sf": "mathsf", "tt": "mathtt", "cal": "mathcal", "mit": "mathit"}[name]
		body := rowNode(p.list(mode))
		setFont(body, texFonts[f])
		return body
	case "tiny", "scriptsize", "footnotesize", "small", "normalsize", "large", "Large", "LARGE", "huge", "Huge":
		return nil
	case "phantom", "hphantom", "vphantom":
		return &texNode{kind: texPhantom, mark: name, body: p.arg()}
	case "smash", "mathstrut", "strut":
		if name == "smash" {
			p.optional()
			return p.arg()
		}
		return nil
	case "cancel", "bcancel", "xcancel", "sout":
		return &texNode{kind: texCancel, mark: name, body: p.arg()}
	case "cancelto":
		p.arg()
		return &texNode{kind: texCancel, mark: "cancel", body: p.arg()}
	case "not":
		p.space()
		if p.eof() {
			return atomNode("/", texRel)
		}
		n := p.base(inArg)
		if n == nil || n.kind != texAtom {
			return atomNode("/", texRel)
		}
		if neg, ok := texNegations[n.text]; ok {
			n.text = neg
		} else {
			n.text += "\u0338"
		}
		n.class = texRel
		return n
	case "pmod", "pod":
		body := p.arg()
		kids := []*texNode{{kind: texSpace, size: 0.5}, atomNode("(", texOpen)}
		if name == "pmod" {
			kids = append(kids, &texNode{kind: texAtom, text: "mod", font: fontRoman}, &texNode{kind: texSpace, size: 0.333})
		}
		kids = append(kids, body, atomNode(")", texClose))
		return rowNode(kids)
	case "bmod":
		return &texNode{kind: texAtom, text: "mod", font: fontRoman, class: texBin}
	case "mod":
		return rowNode([]*texNode{{kind: texSpace, size: 0.5}, {kind: texAtom, text: "mod", font: fontRoman}, {kind: texSpace, size: 0.333}})
	case "begin":
		return p.environment()
	case "substack":
		raw := p.raw()
		q := &texParser{s: raw, depth: p.depth + 1}
		n := tableNode(q.cells(false), "substack")
		n.small = true
		return n
	case "displaylines":
		raw := p.raw()
		q := &texParser{s: raw, depth: p.depth + 1}
		return tableNode(q.cells(false), "gathered")
	case "tag":
		p.star()
		p.tag = plainText(p.raw())
		return nil
	case "label", "nonumber", "notag", "hline", "hdashline", "cline", "allowbreak", "relax", "limits", "nolimits", "displaylimits", "noindent", "centering", "hfill", "hfil", "vfill", "protect", "nolinebreak":
		if name == "label" || name == "cline" {
			p.raw()
		}
		return nil
	case "hspace", "hskip", "kern", "mkern", "mskip":
		if name == "hspace" {
			p.star()
			return &texNode{kind: texSpace, size: parseLength(p.raw())}
		}
		return &texNode{kind: texSpace, size: p.length()}
	case "vspace", "vskip":
		p.star()
		p.raw()
		return nil
	case "multicolumn":
		p.raw()
		p.raw()
		return p.arg()
	case "href":
		p.raw()
		return p.arg()
	case "url":
		return textNode(p.raw(), fontMono)
	case "ce":
		return ceNode(p.raw(), false, p.depth+1)
	case "pu":
		return ceNode(p.raw(), true, p.depth+1)
	case "newcommand", "renewcommand", "providecommand", "def", "DeclareMathOperator":
		p.star()
		p.raw()
		p.optional()
		p.optional()
		p.raw()
		return nil
	case "unicode":
		code := strings.TrimSpace(p.raw())
		if strings.HasPrefix(code, "x") || strings.HasPrefix(code, "X") {
			code = "0" + code
		}
		if v, err := strconv.ParseInt(code, 0, 32); err == nil && v > 0 && v <= unicode.MaxRune {
			return atomNode(string(rune(v)), texOrd)
		}
		return nil
	}
	if p.eof() && isLetter(name[0]) {
		return nil
	}
	return &texNode{kind: texAtom, text: "\\" + name, font: fontText, class: texOp, limits: -1}
}

func (p *texParser) font(name string, f texFont) *texNode {
	body := p.arg()
	switch name {
	case "mathbb", "Bbb":
		mapRunes(body, doubleStruck)
	case "mathcal", "mathscr":
		mapRunes(body, scriptLetter)
	case "mathfrak":
		mapRunes(body, frakturLetter)
	}
	setFont(body, f)
	return body
}

func setFont(n *texNode, f texFont) {
	if n == nil {
		return
	}
	if n.kind == texAtom {
		if n.font == fontAuto {
			n.font = f
		}
		return
	}
	for _, k := range n.kids {
		setFont(k, f)
	}
	for _, r := range n.rows {
		for _, k := range r {
			setFont(k, f)
		}
	}
	setFont(n.body, f)
	setFont(n.over, f)
	setFont(n.under, f)
}

func mapRunes(n *texNode, m func(rune) rune) {
	if n == nil {
		return
	}
	if n.kind == texAtom && n.font != fontText {
		n.text = strings.Map(m, n.text)
		return
	}
	for _, k := range n.kids {
		mapRunes(k, m)
	}
	mapRunes(n.body, m)
	mapRunes(n.over, m)
	mapRunes(n.under, m)
}

func doubleStruck(r rune) rune {
	if s, ok := map[rune]rune{'C': 'ℂ', 'H': 'ℍ', 'N': 'ℕ', 'P': 'ℙ', 'Q': 'ℚ', 'R': 'ℝ', 'Z': 'ℤ'}[r]; ok {
		return s
	}
	switch {
	case r >= 'A' && r <= 'Z':
		return 0x1D538 + r - 'A'
	case r >= 'a' && r <= 'z':
		return 0x1D552 + r - 'a'
	case r >= '0' && r <= '9':
		return 0x1D7D8 + r - '0'
	}
	return r
}

func scriptLetter(r rune) rune {
	if s, ok := map[rune]rune{'B': 'ℬ', 'E': 'ℰ', 'F': 'ℱ', 'H': 'ℋ', 'I': 'ℐ', 'L': 'ℒ', 'M': 'ℳ', 'R': 'ℛ', 'e': 'ℯ', 'g': 'ℊ', 'o': 'ℴ'}[r]; ok {
		return s
	}
	switch {
	case r >= 'A' && r <= 'Z':
		return 0x1D49C + r - 'A'
	case r >= 'a' && r <= 'z':
		return 0x1D4B6 + r - 'a'
	}
	return r
}

func frakturLetter(r rune) rune {
	if s, ok := map[rune]rune{'C': 'ℭ', 'H': 'ℌ', 'I': 'ℑ', 'R': 'ℜ', 'Z': 'ℨ'}[r]; ok {
		return s
	}
	switch {
	case r >= 'A' && r <= 'Z':
		return 0x1D504 + r - 'A'
	case r >= 'a' && r <= 'z':
		return 0x1D51E + r - 'a'
	}
	return r
}

func (p *texParser) raw() string {
	p.space()
	if p.eof() {
		return ""
	}
	if p.s[p.i] != '{' {
		if p.s[p.i] == '\\' {
			start := p.i
			_, j := p.scanName(p.i)
			p.i = j
			return p.s[start:j]
		}
		_, w := utf8.DecodeRuneInString(p.s[p.i:])
		p.i += w
		return p.s[p.i-w : p.i]
	}
	depth := 0
	start := p.i + 1
	for j := p.i; j < len(p.s); j++ {
		switch p.s[j] {
		case '\\':
			j++
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				p.i = j + 1
				return p.s[start:j]
			}
		}
	}
	p.i = len(p.s)
	return p.s[start:]
}

func (p *texParser) optional() string {
	save := p.i
	p.space()
	if p.eof() || p.s[p.i] != '[' {
		p.i = save
		return ""
	}
	depth := 0
	for j := p.i + 1; j < len(p.s); j++ {
		switch p.s[j] {
		case '\\':
			j++
		case '{':
			depth++
		case '}':
			depth--
		case ']':
			if depth <= 0 {
				s := p.s[p.i+1 : j]
				p.i = j + 1
				return s
			}
		}
	}
	p.i = save
	return ""
}

func (p *texParser) length() float32 {
	p.space()
	j := p.i
	for j < len(p.s) && (p.s[j] == '-' || p.s[j] == '+' || p.s[j] == '.' || p.s[j] >= '0' && p.s[j] <= '9' || p.s[j] == ' ') {
		j++
	}
	if j+2 <= len(p.s) && lengthUnit(p.s[j:j+2]) > 0 {
		j += 2
	}
	s := p.s[p.i:j]
	p.i = j
	return parseLength(s)
}

func lengthUnit(u string) float32 {
	switch u {
	case "em":
		return 1
	case "ex":
		return 0.43
	case "pt", "bp", "dd":
		return 0.1
	case "pc":
		return 1.2
	case "mm":
		return 0.2845
	case "cm":
		return 2.845
	case "in":
		return 7.227
	case "px":
		return 1.0 / 16
	case "mu":
		return 1.0 / 18
	}
	return 0
}

func parseLength(s string) float32 {
	s = strings.ReplaceAll(s, " ", "")
	if len(s) < 2 {
		return 0
	}
	u := lengthUnit(s[len(s)-2:])
	if u == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(s[:len(s)-2], 32)
	if err != nil || v != v {
		return 0
	}
	return max(min(float32(v)*u, 20), -20)
}

func (p *texParser) delim() string {
	p.space()
	if p.eof() {
		return ""
	}
	if p.s[p.i] == '\\' {
		name := p.readName()
		if d, ok := texDelims[name]; ok {
			return d
		}
		if s, ok := texSymbols[name]; ok {
			return s.text
		}
		return ""
	}
	r, w := utf8.DecodeRuneInString(p.s[p.i:])
	p.i += w
	switch r {
	case '.':
		return ""
	case '<':
		return "⟨"
	case '>':
		return "⟩"
	}
	return string(r)
}

var texDelims = map[string]string{
	"{": "{", "}": "}", "lbrace": "{", "rbrace": "}", "|": "‖", "Vert": "‖", "lVert": "‖", "rVert": "‖",
	"vert": "|", "lvert": "|", "rvert": "|", "langle": "⟨", "rangle": "⟩", "lfloor": "⌊", "rfloor": "⌋",
	"lceil": "⌈", "rceil": "⌉", "backslash": "\\", "lgroup": "(", "rgroup": ")", "lbrack": "[", "rbrack": "]",
	"lparen": "(", "rparen": ")",
}

func (p *texParser) text(s string, font texFont) *texNode {
	var kids []*texNode
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			kids = append(kids, textNode(b.String(), font))
			b.Reset()
		}
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '$':
			j := strings.IndexByte(s[i+1:], '$')
			if j < 0 {
				b.WriteByte(c)
				i++
				continue
			}
			flush()
			kids = append(kids, rowNode(parseTeXIn(s[i+1:i+1+j], p.depth+1)))
			i += j + 2
		case c == '\\':
			q := &texParser{s: s, i: i, depth: p.depth + 1}
			name, j := q.scanName(i)
			i = j
			switch {
			case name == "" || name == " " || name == "\\" || name == "," || name == ";" || name == "quad":
				b.WriteByte(' ')
				if name == "quad" {
					b.WriteString("  ")
				}
			case len(name) == 1 && !isLetter(name[0]):
				b.WriteString(name)
			default:
				if sym, ok := texSymbols[name]; ok {
					b.WriteString(sym.text)
					continue
				}
				q.i = j
				for q.i < len(s) && s[q.i] == ' ' {
					q.i++
				}
				if q.i < len(s) && s[q.i] == '{' {
					inner := q.raw()
					i = q.i
					flush()
					f := font
					switch name {
					case "textbf", "mathbf":
						f = fontTextBold
					case "textit", "emph", "mathit":
						f = fontTextItalic
					}
					kids = append(kids, q.text(inner, f))
				}
			}
		case c == '~':
			b.WriteString("\u00a0")
			i++
		case c == '{' || c == '}':
			i++
		case c == '-' && strings.HasPrefix(s[i:], "---"):
			b.WriteString("—")
			i += 3
		case c == '-' && strings.HasPrefix(s[i:], "--"):
			b.WriteString("–")
			i += 2
		default:
			b.WriteByte(c)
			i++
		}
	}
	flush()
	if len(kids) == 1 {
		return kids[0]
	}
	return rowNode(kids)
}

func plainText(s string) string {
	p := &texParser{}
	return plainTeX(p.text(s, fontText))
}

func plainTeX(n *texNode) string {
	if n == nil {
		return ""
	}
	if n.kind == texAtom {
		return n.text
	}
	var b strings.Builder
	for _, k := range n.kids {
		b.WriteString(plainTeX(k))
	}
	if n.body != nil {
		b.WriteString(plainTeX(n.body))
	}
	return b.String()
}

func (p *texParser) environment() *texNode {
	name := strings.TrimSpace(p.raw())
	n := tableNode(nil, "matrix")
	n.align = "c"
	switch strings.TrimSuffix(name, "*") {
	case "pmatrix":
		n.left, n.right = "(", ")"
	case "bmatrix":
		n.left, n.right = "[", "]"
	case "Bmatrix":
		n.left, n.right = "{", "}"
	case "vmatrix":
		n.left, n.right = "|", "|"
	case "Vmatrix":
		n.left, n.right = "‖", "‖"
	case "smallmatrix":
		n.small = true
	case "cases", "dcases":
		n.mark, n.align, n.left = "cases", "ll", "{"
		if name == "dcases" {
			n.style = 1
		}
	case "rcases", "drcases":
		n.mark, n.align, n.right = "cases", "ll", "}"
	case "aligned", "align", "split", "alignat", "alignedat", "flalign", "eqnarray", "multlined":
		n.mark, n.align = "aligned", "rl"
		if strings.HasPrefix(name, "alignat") || strings.HasPrefix(name, "alignedat") {
			p.raw()
		}
		if strings.HasPrefix(name, "eqnarray") {
			n.align = "rcl"
		}
	case "gathered", "gather", "multline", "center":
		n.mark = "gathered"
	case "array", "darray", "subarray", "tabular":
		n.mark = "array"
		n.align = columnSpec(p.raw())
		if name == "subarray" {
			n.small = true
		}
	case "equation", "displaymath", "math":
		rows := p.cells(true)
		if len(rows) == 1 && len(rows[0]) == 1 {
			return rows[0][0]
		}
		n.mark = "gathered"
		n.rows = rows
		return n
	}
	if strings.HasSuffix(name, "matrix*") {
		if a := p.optional(); a != "" {
			n.align = columnSpec(a)
		}
	}
	n.rows = p.cells(true)
	return n
}

func columnSpec(s string) string {
	var b strings.Builder
	depth := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '{':
			depth++
		case c == '}':
			depth--
		case depth > 0:
		case c == 'l' || c == 'c' || c == 'r':
			b.WriteByte(c)
		case c == 'p' || c == 'm' || c == 'b' || c == 'X':
			b.WriteByte('l')
		}
	}
	if b.Len() == 0 {
		return "c"
	}
	return b.String()
}

type texSym struct {
	text  string
	class texClass
}

var texSymbols = map[string]texSym{
	"alpha": {"α", texOrd}, "beta": {"β", texOrd}, "gamma": {"γ", texOrd}, "delta": {"δ", texOrd},
	"epsilon": {"ϵ", texOrd}, "varepsilon": {"ε", texOrd}, "zeta": {"ζ", texOrd}, "eta": {"η", texOrd},
	"theta": {"θ", texOrd}, "vartheta": {"ϑ", texOrd}, "iota": {"ι", texOrd}, "kappa": {"κ", texOrd},
	"varkappa": {"ϰ", texOrd}, "lambda": {"λ", texOrd}, "mu": {"μ", texOrd}, "nu": {"ν", texOrd},
	"xi": {"ξ", texOrd}, "omicron": {"ο", texOrd}, "pi": {"π", texOrd}, "varpi": {"ϖ", texOrd},
	"rho": {"ρ", texOrd}, "varrho": {"ϱ", texOrd}, "sigma": {"σ", texOrd}, "varsigma": {"ς", texOrd},
	"tau": {"τ", texOrd}, "upsilon": {"υ", texOrd}, "phi": {"ϕ", texOrd}, "varphi": {"φ", texOrd},
	"chi": {"χ", texOrd}, "psi": {"ψ", texOrd}, "omega": {"ω", texOrd},
	"Gamma": {"Γ", texOrd}, "Delta": {"Δ", texOrd}, "Theta": {"Θ", texOrd}, "Lambda": {"Λ", texOrd},
	"Xi": {"Ξ", texOrd}, "Pi": {"Π", texOrd}, "Sigma": {"Σ", texOrd}, "Upsilon": {"Υ", texOrd},
	"Phi": {"Φ", texOrd}, "Psi": {"Ψ", texOrd}, "Omega": {"Ω", texOrd},
	"varGamma": {"Γ", texOrd}, "varDelta": {"Δ", texOrd}, "varTheta": {"Θ", texOrd}, "varLambda": {"Λ", texOrd},
	"varPi": {"Π", texOrd}, "varSigma": {"Σ", texOrd}, "varPhi": {"Φ", texOrd}, "varPsi": {"Ψ", texOrd}, "varOmega": {"Ω", texOrd},
	"Alpha": {"Α", texOrd}, "Beta": {"Β", texOrd}, "Epsilon": {"Ε", texOrd}, "Zeta": {"Ζ", texOrd},
	"Eta": {"Η", texOrd}, "Iota": {"Ι", texOrd}, "Kappa": {"Κ", texOrd}, "Mu": {"Μ", texOrd},
	"Nu": {"Ν", texOrd}, "Omicron": {"Ο", texOrd}, "Rho": {"Ρ", texOrd}, "Tau": {"Τ", texOrd}, "Chi": {"Χ", texOrd},
	"digamma": {"ϝ", texOrd},

	"pm": {"±", texBin}, "mp": {"∓", texBin}, "times": {"×", texBin}, "div": {"÷", texBin},
	"cdot": {"⋅", texBin}, "ast": {"∗", texBin}, "star": {"⋆", texBin}, "circ": {"∘", texBin},
	"bullet": {"∙", texBin}, "cap": {"∩", texBin}, "cup": {"∪", texBin}, "wedge": {"∧", texBin},
	"land": {"∧", texBin}, "vee": {"∨", texBin}, "lor": {"∨", texBin}, "setminus": {"∖", texBin},
	"smallsetminus": {"∖", texBin}, "oplus": {"⊕", texBin}, "ominus": {"⊖", texBin}, "otimes": {"⊗", texBin},
	"oslash": {"⊘", texBin}, "odot": {"⊙", texBin}, "sqcap": {"⊓", texBin}, "sqcup": {"⊔", texBin},
	"uplus": {"⊎", texBin}, "amalg": {"⨿", texBin}, "wr": {"≀", texBin}, "bigcirc": {"◯", texBin},
	"triangleleft": {"◃", texBin}, "triangleright": {"▹", texBin}, "lhd": {"⊲", texBin}, "rhd": {"⊳", texBin},
	"unlhd": {"⊴", texBin}, "unrhd": {"⊵", texBin}, "dagger": {"†", texBin}, "ddagger": {"‡", texBin},
	"dotplus": {"∔", texBin}, "centerdot": {"⋅", texBin}, "intercal": {"⊺", texBin}, "divideontimes": {"⋇", texBin},
	"ltimes": {"⋉", texBin}, "rtimes": {"⋊", texBin}, "boxplus": {"⊞", texBin}, "boxtimes": {"⊠", texBin},
	"boxminus": {"⊟", texBin}, "boxdot": {"⊡", texBin}, "barwedge": {"⊼", texBin}, "veebar": {"⊻", texBin},

	"eq": {"=", texRel}, "le": {"≤", texRel}, "leq": {"≤", texRel}, "ge": {"≥", texRel}, "geq": {"≥", texRel},
	"leqslant": {"⩽", texRel}, "geqslant": {"⩾", texRel}, "leqq": {"≦", texRel}, "geqq": {"≧", texRel},
	"ne": {"≠", texRel}, "neq": {"≠", texRel}, "approx": {"≈", texRel}, "approxeq": {"≊", texRel},
	"equiv": {"≡", texRel}, "sim": {"∼", texRel}, "simeq": {"≃", texRel}, "cong": {"≅", texRel},
	"propto": {"∝", texRel}, "varpropto": {"∝", texRel}, "in": {"∈", texRel}, "notin": {"∉", texRel}, "ni": {"∋", texRel},
	"owns": {"∋", texRel}, "subset": {"⊂", texRel}, "supset": {"⊃", texRel}, "subseteq": {"⊆", texRel},
	"supseteq": {"⊇", texRel}, "subsetneq": {"⊊", texRel}, "supsetneq": {"⊋", texRel}, "nsubseteq": {"⊈", texRel},
	"nsupseteq": {"⊉", texRel}, "subseteqq": {"⫅", texRel}, "supseteqq": {"⫆", texRel}, "sqsubset": {"⊏", texRel},
	"sqsupset": {"⊐", texRel}, "sqsubseteq": {"⊑", texRel}, "sqsupseteq": {"⊒", texRel},
	"to": {"→", texRel}, "gets": {"←", texRel}, "rightarrow": {"→", texRel}, "leftarrow": {"←", texRel},
	"Rightarrow": {"⇒", texRel}, "Leftarrow": {"⇐", texRel}, "leftrightarrow": {"↔", texRel},
	"Leftrightarrow": {"⇔", texRel}, "iff": {"⟺", texRel}, "implies": {"⟹", texRel}, "impliedby": {"⟸", texRel},
	"mapsto": {"↦", texRel}, "longmapsto": {"⟼", texRel}, "longrightarrow": {"⟶", texRel},
	"longleftarrow": {"⟵", texRel}, "Longrightarrow": {"⟹", texRel}, "Longleftarrow": {"⟸", texRel},
	"longleftrightarrow": {"⟷", texRel}, "Longleftrightarrow": {"⟺", texRel}, "uparrow": {"↑", texOrd},
	"downarrow": {"↓", texOrd}, "Uparrow": {"⇑", texRel}, "Downarrow": {"⇓", texRel}, "updownarrow": {"↕", texRel},
	"Updownarrow": {"⇕", texRel}, "nearrow": {"↗", texRel}, "searrow": {"↘", texRel}, "swarrow": {"↙", texRel},
	"nwarrow": {"↖", texRel}, "rightleftharpoons": {"⇌", texRel}, "leftrightharpoons": {"⇋", texRel},
	"rightharpoonup": {"⇀", texRel}, "rightharpoondown": {"⇁", texRel}, "leftharpoonup": {"↼", texRel},
	"leftharpoondown": {"↽", texRel}, "hookrightarrow": {"↪", texRel}, "hookleftarrow": {"↩", texRel},
	"rightrightarrows": {"⇉", texRel}, "leftleftarrows": {"⇇", texRel}, "rightleftarrows": {"⇄", texRel},
	"leftrightarrows": {"⇆", texRel}, "Lleftarrow": {"⇚", texRel}, "Rrightarrow": {"⇛", texRel},
	"twoheadrightarrow": {"↠", texRel}, "twoheadleftarrow": {"↞", texRel}, "rightsquigarrow": {"⇝", texRel},
	"leadsto": {"⇝", texRel}, "longleftrightarrows": {"⇄", texRel}, "circlearrowleft": {"↺", texRel}, "circlearrowright": {"↻", texRel},
	"ll": {"≪", texRel}, "gg": {"≫", texRel}, "lll": {"⋘", texRel}, "ggg": {"⋙", texRel}, "prec": {"≺", texRel},
	"succ": {"≻", texRel}, "preceq": {"⪯", texRel}, "succeq": {"⪰", texRel}, "models": {"⊨", texRel},
	"vdash": {"⊢", texRel}, "dashv": {"⊣", texRel}, "vDash": {"⊨", texRel}, "Vdash": {"⊩", texRel},
	"asymp": {"≍", texRel}, "doteq": {"≐", texRel}, "triangleq": {"≜", texRel}, "coloneqq": {"≔", texRel},
	"eqqcolon": {"≕", texRel}, "coloneq": {"≔", texRel}, "Coloneqq": {"⩴", texRel}, "lesssim": {"≲", texRel},
	"gtrsim": {"≳", texRel}, "lessgtr": {"≶", texRel}, "gtrless": {"≷", texRel}, "lneq": {"⪇", texRel},
	"gneq": {"⪈", texRel}, "lneqq": {"≨", texRel}, "gneqq": {"≩", texRel}, "nless": {"≮", texRel}, "ngtr": {"≯", texRel},
	"nleq": {"≰", texRel}, "ngeq": {"≱", texRel}, "nleqslant": {"⪇", texRel}, "ngeqslant": {"⪈", texRel},
	"nsim": {"≁", texRel}, "ncong": {"≇", texRel}, "parallel": {"∥", texRel}, "nparallel": {"∦", texRel},
	"perp": {"⊥", texRel}, "mid": {"|", texRel}, "nmid": {"∤", texRel}, "bowtie": {"⋈", texRel},
	"Join": {"⋈", texRel}, "smile": {"⌣", texRel}, "frown": {"⌢", texRel}, "because": {"∵", texRel},
	"therefore": {"∴", texRel}, "backsim": {"∽", texRel}, "backsimeq": {"⋍", texRel}, "eqsim": {"≂", texRel},
	"risingdotseq": {"≓", texRel}, "fallingdotseq": {"≒", texRel}, "bumpeq": {"≏", texRel}, "Bumpeq": {"≎", texRel},
	"between": {"≬", texRel}, "pitchfork": {"⋔", texRel}, "trianglelefteq": {"⊴", texRel}, "trianglerighteq": {"⊵", texRel},
	"vartriangleleft": {"⊲", texRel}, "vartriangleright": {"⊳", texRel}, "neg": {"¬", texOrd}, "lnot": {"¬", texOrd},
	"colon": {":", texPunct}, "ratio": {"∶", texRel}, "measeq": {"≝", texRel}, "approxident": {"≋", texRel},

	"infty": {"∞", texOrd}, "partial": {"∂", texOrd}, "nabla": {"∇", texOrd}, "forall": {"∀", texOrd},
	"exists": {"∃", texOrd}, "nexists": {"∄", texOrd}, "emptyset": {"∅", texOrd}, "varnothing": {"∅", texOrd},
	"hbar": {"ℏ", texOrd}, "hslash": {"ℏ", texOrd}, "ell": {"ℓ", texOrd}, "Re": {"ℜ", texOrd}, "Im": {"ℑ", texOrd},
	"aleph": {"ℵ", texOrd}, "beth": {"ℶ", texOrd}, "gimel": {"ℷ", texOrd}, "wp": {"℘", texOrd},
	"angle": {"∠", texOrd}, "measuredangle": {"∡", texOrd}, "sphericalangle": {"∢", texOrd},
	"triangle": {"△", texOrd}, "vartriangle": {"△", texOrd}, "triangledown": {"▽", texOrd}, "bigtriangleup": {"△", texBin},
	"bigtriangledown": {"▽", texBin}, "blacktriangle": {"▲", texOrd}, "blacktriangledown": {"▼", texOrd},
	"square": {"□", texOrd}, "Box": {"□", texOrd}, "blacksquare": {"■", texOrd}, "diamond": {"⋄", texBin},
	"Diamond": {"◊", texOrd}, "lozenge": {"◊", texOrd}, "blacklozenge": {"⧫", texOrd}, "bigstar": {"★", texOrd},
	"prime": {"′", texOrd}, "backprime": {"‵", texOrd}, "degree": {"°", texOrd}, "top": {"⊤", texOrd},
	"bot": {"⊥", texOrd}, "S": {"§", texOrd}, "P": {"¶", texOrd}, "checkmark": {"✓", texOrd},
	"dag": {"†", texOrd}, "ddag": {"‡", texOrd}, "copyright": {"©", texOrd}, "pounds": {"£", texOrd},
	"yen": {"¥", texOrd}, "euro": {"€", texOrd}, "complement": {"∁", texOrd}, "flat": {"♭", texOrd},
	"natural": {"♮", texOrd}, "sharp": {"♯", texOrd}, "clubsuit": {"♣", texOrd}, "diamondsuit": {"♢", texOrd},
	"heartsuit": {"♡", texOrd}, "spadesuit": {"♠", texOrd}, "imath": {"ı", texOrd}, "jmath": {"ȷ", texOrd},
	"mho": {"℧", texOrd}, "Finv": {"Ⅎ", texOrd}, "eth": {"ð", texOrd}, "circledR": {"®", texOrd}, "circledS": {"Ⓢ", texOrd},
	"N": {"ℕ", texOrd}, "Z": {"ℤ", texOrd}, "Q": {"ℚ", texOrd}, "R": {"ℝ", texOrd}, "C": {"ℂ", texOrd},
	"natnums": {"ℕ", texOrd}, "reals": {"ℝ", texOrd}, "Reals": {"ℝ", texOrd}, "cnums": {"ℂ", texOrd}, "Complex": {"ℂ", texOrd},
	"ldots": {"…", texInner}, "dots": {"…", texInner}, "cdots": {"⋯", texInner}, "dotsb": {"⋯", texInner},
	"dotsc": {"…", texInner}, "dotsm": {"⋯", texInner}, "dotsi": {"⋯", texInner}, "dotso": {"…", texInner},
	"vdots": {"⋮", texOrd}, "ddots": {"⋱", texInner}, "iddots": {"⋰", texInner}, "mathellipsis": {"…", texInner},

	"{": {"{", texOpen}, "}": {"}", texClose}, "lbrace": {"{", texOpen}, "rbrace": {"}", texClose},
	"lbrack": {"[", texOpen}, "rbrack": {"]", texClose}, "lparen": {"(", texOpen}, "rparen": {")", texClose},
	"langle": {"⟨", texOpen}, "rangle": {"⟩", texClose}, "lfloor": {"⌊", texOpen}, "rfloor": {"⌋", texClose},
	"lceil": {"⌈", texOpen}, "rceil": {"⌉", texClose}, "lvert": {"|", texOpen}, "rvert": {"|", texClose},
	"lVert": {"‖", texOpen}, "rVert": {"‖", texClose}, "vert": {"|", texOrd}, "Vert": {"‖", texOrd},
	"|": {"‖", texOrd}, "backslash": {"\\", texOrd}, "ulcorner": {"⌜", texOpen}, "urcorner": {"⌝", texClose},
	"llcorner": {"⌞", texOpen}, "lrcorner": {"⌟", texClose},
	"#": {"#", texOrd}, "$": {"$", texOrd}, "%": {"%", texOrd}, "&": {"&", texOrd}, "_": {"_", texOrd},
	"textbackslash": {"\\", texOrd}, "textasciitilde": {"~", texOrd}, "textdegree": {"°", texOrd},
	"textunderscore": {"_", texOrd}, "textbar": {"|", texOrd}, "lq": {"‘", texOrd}, "rq": {"’", texOrd},
	"slash": {"/", texOrd}, "surd": {"√", texOrd}, "sslash": {"⫽", texBin}, "infin": {"∞", texOrd},
	"celsius": {"℃", texOrd}, "permil": {"‰", texOrd}, "micro": {"µ", texOrd}, "ohm": {"Ω", texOrd},
	"origof": {"⊶", texRel}, "imageof": {"⊷", texRel}, "multimap": {"⊸", texRel}, "And": {"&", texBin},
}

var texLargeOps = map[string]string{
	"sum": "∑", "prod": "∏", "coprod": "∐", "int": "∫", "iint": "∬", "iiint": "∭", "oint": "∮",
	"oiint": "∯", "oiiint": "∰", "intop": "∫", "smallint": "∫", "bigcup": "⋃", "bigcap": "⋂",
	"bigoplus": "⨁", "bigotimes": "⨂", "bigodot": "⨀", "bigvee": "⋁", "bigwedge": "⋀",
	"bigsqcup": "⨆", "biguplus": "⨄",
}

var texFunctions = map[string]int8{
	"sin": -1, "cos": -1, "tan": -1, "cot": -1, "sec": -1, "csc": -1, "arcsin": -1, "arccos": -1,
	"arctan": -1, "arccot": -1, "sinh": -1, "cosh": -1, "tanh": -1, "coth": -1, "sech": -1, "csch": -1,
	"log": -1, "ln": -1, "lg": -1, "exp": -1, "arg": -1, "deg": -1, "dim": -1, "hom": -1, "ker": -1,
	"tg": -1, "ctg": -1, "cotg": -1, "arctg": -1, "arcctg": -1, "sh": -1, "ch": -1, "th": -1, "cth": -1,
	"lim": 1, "limsup": 1, "liminf": 1, "max": 1, "min": 1, "sup": 1, "inf": 1, "det": 1, "gcd": 1,
	"Pr": 1, "injlim": 1, "projlim": 1, "argmax": 1, "argmin": 1, "lcm": -1, "sgn": -1, "Var": -1, "Cov": -1, "E": -1,
}

var texSpaces = map[string]float32{
	",": 3.0 / 18, "thinspace": 3.0 / 18, ":": 4.0 / 18, ">": 4.0 / 18, "medspace": 4.0 / 18,
	";": 5.0 / 18, "thickspace": 5.0 / 18, "!": -3.0 / 18, "negthinspace": -3.0 / 18,
	"negmedspace": -4.0 / 18, "negthickspace": -5.0 / 18, " ": 0.25, "space": 0.25, "nobreakspace": 0.25,
	"quad": 1, "qquad": 2, "enspace": 0.5, "enskip": 0.5, "nobreak": 0,
}

var texAccents = map[string]bool{
	"hat": true, "check": true, "breve": true, "acute": true, "grave": true, "tilde": true, "bar": true,
	"vec": true, "dot": true, "ddot": true, "dddot": true, "mathring": true, "widehat": true, "widetilde": true,
	"widecheck": true, "overline": true, "underline": true, "overrightarrow": true, "overleftarrow": true,
	"overleftrightarrow": true, "underrightarrow": true, "underleftarrow": true, "underleftrightarrow": true,
	"overgroup": true, "undergroup": true, "overparen": true, "underparen": true, "overarc": true,
	"utilde": true, "Overrightarrow": true, "Vec": true, "Hat": true, "Bar": true, "Tilde": true, "Dot": true, "Ddot": true,
}

var texFonts = map[string]texFont{
	"mathrm": fontRoman, "mathup": fontRoman, "mathnormal": fontAuto, "mathit": fontItalic, "mathbf": fontBold,
	"mathbfit": fontBoldItalic, "boldsymbol": fontBoldItalic, "bm": fontBoldItalic, "pmb": fontBoldItalic,
	"mathsf": fontRoman, "mathsfit": fontItalic, "mathtt": fontMono, "mathbb": fontRoman, "Bbb": fontRoman,
	"mathcal": fontRoman, "mathscr": fontRoman, "mathfrak": fontRoman,
}

var texArrows = map[string]bool{
	"xrightarrow": true, "xleftarrow": true, "xRightarrow": true, "xLeftarrow": true, "xleftrightarrow": true,
	"xLeftrightarrow": true, "xlongequal": true, "xrightleftharpoons": true, "xleftrightharpoons": true,
	"xmapsto": true, "xhookrightarrow": true, "xhookleftarrow": true, "xtofrom": true, "xtwoheadrightarrow": true,
	"xrightharpoonup": true, "xrightharpoondown": true, "xleftharpoonup": true, "xleftharpoondown": true,
}

var texNegations = map[string]string{
	"=": "≠", "<": "≮", ">": "≯", "≤": "≰", "≥": "≱", "∈": "∉", "∋": "∌", "≡": "≢", "∼": "≁",
	"≃": "≄", "≈": "≉", "≅": "≇", "⊂": "⊄", "⊃": "⊅", "⊆": "⊈", "⊇": "⊉", "∣": "∤", "∥": "∦",
	"∃": "∄", "→": "↛", "←": "↚", "↔": "↮", "⇒": "⇏", "⇐": "⇍", "⇔": "⇎", "⪯": "⋠", "⪰": "⋡",
	"≺": "⊀", "≻": "⊁", "⊢": "⊬", "⊨": "⊭",
}

type ceParser struct {
	s     string
	i     int
	units bool
	depth int
	out   []*texNode
	start bool
}

func ceNode(s string, units bool, depth int) *texNode {
	c := &ceParser{s: s, units: units, depth: depth, start: true}
	c.parse()
	return rowNode(c.out)
}

func (c *ceParser) add(n *texNode) {
	c.out = append(c.out, n)
	c.start = false
}

func (c *ceParser) attach(sup, sub *texNode) {
	if len(c.out) == 0 {
		c.out = append(c.out, &texNode{kind: texScripts, body: rowNode(nil), over: sup, under: sub})
		return
	}
	last := c.out[len(c.out)-1]
	if last.kind == texScripts && (sup == nil || last.over == nil) && (sub == nil || last.under == nil) {
		if sup != nil {
			last.over = sup
		}
		if sub != nil {
			last.under = sub
		}
		return
	}
	c.out[len(c.out)-1] = &texNode{kind: texScripts, body: last, over: sup, under: sub, class: last.class}
}

func (c *ceParser) spaced(i int) bool {
	return i < 0 || i >= len(c.s) || c.s[i] == ' '
}

func (c *ceParser) parse() {
	arrows := []struct{ src, text, mark string }{
		{"<=>>", "⇌", "xrightleftharpoons"}, {"<<=>", "⇌", "xrightleftharpoons"}, {"<=>", "⇌", "xrightleftharpoons"},
		{"<-->", "⟷", "xleftrightarrow"}, {"<->", "↔", "xleftrightarrow"}, {"->", "→", "xrightarrow"},
		{"<-", "←", "xleftarrow"}, {"→", "→", "xrightarrow"}, {"⇌", "⇌", "xrightleftharpoons"},
	}
	for c.i < len(c.s) {
		ch := c.s[c.i]
		if ch == ' ' || ch == '\t' || ch == '\n' {
			c.i++
			if c.units && len(c.out) > 0 && c.out[len(c.out)-1].kind != texSpace {
				c.out = append(c.out, &texNode{kind: texSpace, size: 3.0 / 18})
			}
			c.start = true
			continue
		}
		matched := false
		for _, a := range arrows {
			if strings.HasPrefix(c.s[c.i:], a.src) {
				c.i += len(a.src)
				p := &texParser{s: c.s, i: c.i, depth: c.depth}
				over, under := p.optional(), p.optional()
				c.i = p.i
				if over == "" && under == "" {
					c.add(atomNode(a.text, texRel))
				} else {
					n := &texNode{kind: texArrow, mark: a.mark, class: texRel, over: ceNode(over, false, c.depth+1)}
					if under != "" {
						n.under = ceNode(under, false, c.depth+1)
					}
					c.add(n)
				}
				c.start = true
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		switch {
		case ch == '+' && c.spaced(c.i-1):
			c.i++
			c.add(atomNode("+", texBin))
			c.start = true
		case (ch == '+' || ch == '-') && !c.start && !c.units:
			j := c.i
			for j < len(c.s) && (c.s[j] == '+' || c.s[j] == '-') {
				j++
			}
			c.attach(atomNode(strings.ReplaceAll(c.s[c.i:j], "-", "−"), texOrd), nil)
			c.i = j
		case ch == '=' && c.spaced(c.i-1):
			c.i++
			c.add(atomNode("=", texRel))
			c.start = true
		case ch == '^':
			c.i++
			if c.spaced(c.i-2) && c.spaced(c.i) {
				c.add(atomNode("↑", texOrd))
				continue
			}
			c.attach(c.script(), nil)
		case ch == '_':
			c.i++
			c.attach(nil, c.script())
		case ch == 'v' && c.spaced(c.i-1) && c.spaced(c.i+1):
			c.i++
			c.add(atomNode("↓", texOrd))
		case ch >= '0' && ch <= '9':
			j := c.i
			coef := c.start || c.units
			for j < len(c.s) && (c.s[j] >= '0' && c.s[j] <= '9' || coef && (c.s[j] == '.' || c.s[j] == '/') && j+1 < len(c.s) && c.s[j+1] >= '0' && c.s[j+1] <= '9') {
				j++
			}
			num := c.s[c.i:j]
			c.i = j
			if coef {
				c.add(&texNode{kind: texAtom, text: num, font: fontRoman})
				c.start = c.units
			} else {
				c.attach(nil, &texNode{kind: texAtom, text: num, font: fontRoman})
			}
		case isLetter(ch):
			j := c.i + 1
			if ch >= 'A' && ch <= 'Z' {
				for j < len(c.s) && c.s[j] >= 'a' && c.s[j] <= 'z' {
					j++
				}
			} else {
				for j < len(c.s) && isLetter(c.s[j]) {
					j++
				}
			}
			c.add(&texNode{kind: texAtom, text: c.s[c.i:j], font: fontRoman})
			c.i = j
		case ch == '(' || ch == '[':
			c.i++
			c.add(atomNode(string(ch), texOpen))
			c.start = c.units
		case ch == ')' || ch == ']':
			c.i++
			c.add(atomNode(string(ch), texClose))
		case ch == '.' || ch == '*':
			c.i++
			c.add(atomNode("·", texOrd))
			c.start = !c.units
		case ch == '$':
			j := strings.IndexByte(c.s[c.i+1:], '$')
			if j < 0 {
				j = len(c.s) - c.i - 1
			}
			c.add(rowNode(parseTeXIn(c.s[c.i+1:c.i+1+j], c.depth+1)))
			c.i = min(len(c.s), c.i+j+2)
		case ch == '{':
			p := &texParser{s: c.s, i: c.i, depth: c.depth}
			inner := p.raw()
			c.i = p.i
			c.add(ceNode(inner, c.units, c.depth+1))
		case ch == '\\':
			p := &texParser{s: c.s, i: c.i, depth: c.depth + 1}
			n := p.command(inArg)
			c.i = max(p.i, c.i+1)
			if n != nil {
				setFont(n, fontRoman)
				c.add(n)
			}
		case ch == '"' || ch == '}':
			c.i++
		default:
			r, w := utf8.DecodeRuneInString(c.s[c.i:])
			if isTextRune(r) {
				j := c.i
				for j < len(c.s) {
					r, w := utf8.DecodeRuneInString(c.s[j:])
					if !isTextRune(r) {
						break
					}
					j += w
				}
				c.add(textNode(c.s[c.i:j], fontText))
				c.i = j
				continue
			}
			c.i += w
			p := &texParser{}
			n := p.char(r)
			setFont(n, fontRoman)
			c.add(n)
		}
	}
}

func (c *ceParser) script() *texNode {
	if c.i < len(c.s) && c.s[c.i] == '{' {
		p := &texParser{s: c.s, i: c.i, depth: c.depth}
		inner := p.raw()
		c.i = p.i
		return ceCharge(inner)
	}
	j := c.i
	for j < len(c.s) && (c.s[j] >= '0' && c.s[j] <= '9' || c.s[j] == '+' || c.s[j] == '-') {
		j++
	}
	if j == c.i && j < len(c.s) {
		_, w := utf8.DecodeRuneInString(c.s[j:])
		j += w
	}
	s := c.s[c.i:j]
	c.i = j
	return ceCharge(s)
}

func ceCharge(s string) *texNode {
	return &texNode{kind: texAtom, text: strings.ReplaceAll(s, "-", "−"), font: fontRoman}
}
