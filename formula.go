package main

import (
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

type mbox struct {
	w, a, d float32
	items   []mitem
}

type mitem struct {
	x, y float32
	draw func(p *ui.Painter, x, y float32, col ui.Color)
}

func (b *mbox) add(o mbox, x, y float32) {
	for _, it := range o.items {
		it.x += x
		it.y += y
		b.items = append(b.items, it)
	}
}

func (b *mbox) paint(p *ui.Painter, x, y float32, col ui.Color) {
	for _, it := range b.items {
		it.draw(p, x+it.x, y+it.y, col)
	}
}

func (b *mbox) put(x, y float32, draw func(p *ui.Painter, x, y float32, col ui.Color)) {
	b.items = append(b.items, mitem{x, y, draw})
}

type mlayout struct {
	c    *ui.Context
	size float32
	font string
}

type mstate struct {
	level int8
	color ui.Color
}

const (
	mathAxis = 0.255
	mathXH   = 0.45
)

func (l *mlayout) fs(st mstate) float32 {
	switch st.level {
	case styleScript:
		return l.size * 0.7
	case styleScriptScript:
		return l.size * 0.55
	}
	return l.size
}

func (l *mlayout) rule(fs float32) float32 {
	return max(fs*0.055, 1)
}

func pick(c, def ui.Color) ui.Color {
	if c.A > 0 {
		return c
	}
	return def
}

func scriptLevel(level int8) int8 {
	if level <= styleText {
		return styleScript
	}
	return styleScriptScript
}

func fracLevel(level int8) int8 {
	if level == styleDisplay {
		return styleText
	}
	return scriptLevel(level)
}

func (l *mlayout) node(n *texNode, st mstate) mbox {
	if n == nil {
		return mbox{}
	}
	switch n.kind {
	case texRow:
		return l.row(n.kids, st, false)
	case texAtom:
		return l.atom(n, st)
	case texSpace:
		return mbox{w: n.size * l.fs(st)}
	case texFrac:
		return l.frac(n, st)
	case texSqrt:
		return l.sqrt(n, st)
	case texScripts:
		return l.scripts(n, st)
	case texFence:
		body := l.node(n.body, st)
		return l.fenced(n.left, n.right, body, st)
	case texAccent:
		return l.accent(n, st)
	case texStack:
		return l.stack(n, st)
	case texArrow:
		return l.arrow(n, st)
	case texTable:
		return l.table(n, st)
	case texBox:
		return l.boxed(n, st)
	case texColor:
		s := st
		s.color = parseColor(n.color, st.color)
		return l.node(n.body, s)
	case texStyle:
		s := st
		s.level = n.style
		return l.node(n.body, s)
	case texPhantom:
		b := l.node(n.body, st)
		b.items = nil
		switch n.mark {
		case "hphantom":
			b.a, b.d = 0, 0
		case "vphantom":
			b.w = 0
		}
		return b
	case texCancel:
		return l.cancel(n, st)
	}
	return mbox{}
}

type mpart struct {
	n  *texNode
	st mstate
}

func flatten(kids []*texNode, st mstate, out []mpart) []mpart {
	for _, k := range kids {
		switch {
		case k == nil:
		case k.kind == texColor && k.body != nil && k.body.kind == texRow:
			s := st
			s.color = parseColor(k.color, st.color)
			out = flatten(k.body.kids, s, out)
		case k.kind == texStyle && k.body != nil && k.body.kind == texRow:
			s := st
			s.level = k.style
			out = flatten(k.body.kids, s, out)
		default:
			out = append(out, mpart{k, st})
		}
	}
	return out
}

func classes(parts []mpart, leading bool) []texClass {
	cls := make([]texClass, len(parts))
	prev := -1
	if leading {
		prev = int(texOrd)
	}
	for i, p := range parts {
		if p.n.kind == texSpace {
			continue
		}
		c := p.n.class
		if c == texBin && (prev < 0 || prev == int(texBin) || prev == int(texOp) || prev == int(texRel) || prev == int(texOpen) || prev == int(texPunct)) {
			c = texOrd
		}
		if (c == texRel || c == texClose || c == texPunct) && prev == int(texBin) {
			for j := i - 1; j >= 0; j-- {
				if parts[j].n.kind != texSpace {
					cls[j] = texOrd
					break
				}
			}
		}
		cls[i] = c
		prev = int(c)
	}
	for j := len(parts) - 1; j >= 0; j-- {
		if parts[j].n.kind != texSpace {
			if cls[j] == texBin {
				cls[j] = texOrd
			}
			break
		}
	}
	return cls
}

var texSpacing = [8][8]int8{
	{0, 1, -2, -3, 0, 0, 0, -1},
	{1, 1, 0, -3, 0, 0, 0, -1},
	{-2, -2, 0, 0, -2, 0, 0, -2},
	{-3, -3, 0, 0, -3, 0, 0, -3},
	{0, 0, 0, 0, 0, 0, 0, 0},
	{0, 1, -2, -3, 0, 0, 0, -1},
	{-1, -1, 0, -1, -1, -1, -1, -1},
	{-1, 1, -2, -3, -1, 0, -1, -1},
}

func spacing(a, b texClass, level int8) float32 {
	s := texSpacing[a][b]
	if s < 0 {
		if level >= styleScript {
			return 0
		}
		s = -s
	}
	switch s {
	case 1:
		return 3.0 / 18
	case 2:
		return 4.0 / 18
	case 3:
		return 5.0 / 18
	}
	return 0
}

func (l *mlayout) row(kids []*texNode, st mstate, leading bool) mbox {
	parts := flatten(kids, st, nil)
	cls := classes(parts, leading)
	var b mbox
	x := float32(0)
	prev := texClass(255)
	if leading {
		prev = texOrd
	}
	for i, p := range parts {
		fs := l.fs(p.st)
		if p.n.kind == texSpace {
			x += p.n.size * fs
			continue
		}
		if prev != 255 {
			x += spacing(prev, cls[i], p.st.level) * fs
		}
		kb := l.node(p.n, p.st)
		b.add(kb, x, 0)
		x += kb.w
		b.a = max(b.a, kb.a)
		b.d = max(b.d, kb.d)
		prev = cls[i]
	}
	b.w = max(x, 0)
	return b
}

func italicRune(r rune) bool {
	return r < 0x80 && isLetter(byte(r)) || r >= 'α' && r <= 'ω' || strings.ContainsRune("ϵϑϰϕϱϖ", r)
}

func atomItalic(n *texNode) bool {
	switch n.font {
	case fontItalic, fontTextItalic:
		return true
	case fontAuto, fontBoldItalic:
		r, _ := utf8.DecodeRuneInString(n.text)
		return utf8.RuneCountInString(n.text) == 1 && italicRune(r)
	}
	return false
}

func atomWeight(n *texNode) int {
	switch n.font {
	case fontBold, fontBoldItalic, fontTextBold:
		return 700
	}
	return 0
}

func runeHeight(r rune) float32 {
	switch {
	case strings.ContainsRune("acemnorsuvwxzıȷαγεϵηικμνοπρστυφϕχωςϖ∞∝", r):
		return mathXH
	case r == 't':
		return 0.62
	case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9':
		return 0.7
	case strings.ContainsRune("()[]{}|‖⟨⟩⌊⌋⌈⌉/\\′″‴", r):
		return 0.76
	case strings.ContainsRune("+−=×÷±∓<>≤≥≠≈≡∼≅∈∉⊂⊃⊆⊇∪∩∧∨→←↔⇒⇐⇔⟶⟵⟹⟸⟺↦⇌", r):
		return 0.58
	case strings.ContainsRune("⋅·∘∙∗*⋯", r):
		return 0.4
	case strings.ContainsRune(".,;…_", r):
		return 0.12
	case r == ' ' || r == '\u00a0':
		return 0
	case unicode.Is(unicode.Greek, r):
		return 0.7
	case r >= 0x2e80:
		return 0.82
	}
	return 0.7
}

func runeDepth(r rune) float32 {
	switch {
	case strings.ContainsRune("gjpqyβγζημξρςφϕχψ", r):
		return 0.2
	case strings.ContainsRune("()[]{}|‖⟨⟩⌊⌋⌈⌉/\\", r):
		return 0.24
	case strings.ContainsRune(",;", r):
		return 0.16
	case r == '_':
		return 0.1
	case r >= 0x2e80:
		return 0.12
	}
	return 0
}

func (l *mlayout) text(s string, fs float32, italic bool, weight int, font string, col ui.Color) mbox {
	if font == "" {
		font = l.font
	}
	span := ui.Span{Text: s, Size: fs, Italic: italic, Weight: weight, Font: font}
	w, h := l.c.MeasureText(0, span)
	var a, d float32
	for _, r := range s {
		a = max(a, runeHeight(r))
		d = max(d, runeDepth(r))
	}
	top := 0.8 * h
	b := mbox{w: w, a: a * fs, d: d * fs}
	b.put(0, 0, func(p *ui.Painter, x, y float32, def ui.Color) {
		sp := span
		sp.Color = pick(col, def)
		p.RichText(x, y-top, 0, sp)
	})
	return b
}

func italicKern(s string) float32 {
	r, _ := utf8.DecodeLastRuneInString(s)
	switch {
	case r == 'f':
		return 0.08
	case strings.ContainsRune("jpqyvwVWTYPFHIJKMNXUZ", r):
		return 0.04
	}
	return 0.02
}

func (l *mlayout) atom(n *texNode, st mstate) mbox {
	fs := l.fs(st)
	if n.large {
		return l.largeOp(n, st)
	}
	if n.size > 0 {
		return l.delimiter(n.text, n.size*l.size/2+mathAxis*fs, n.size*l.size/2-mathAxis*fs, fs, st.color, true)
	}
	if n.text == "" {
		return mbox{}
	}
	font := ""
	if n.font == fontMono {
		font = "monospace"
	}
	italic := atomItalic(n)
	b := l.text(n.text, fs, italic, atomWeight(n), font, st.color)
	if italic {
		b.w += italicKern(n.text) * fs * 0.5
	}
	return b
}

func (l *mlayout) largeOp(n *texNode, st mstate) mbox {
	fs := l.fs(st)
	integral := opLimits(n.text) < 0
	scale := float32(1.15)
	if st.level == styleDisplay {
		scale = 1.8
		if integral {
			scale = 1.9
		}
	} else if st.level >= styleScript {
		scale = 1
	}
	size := fs * scale
	top, depth := float32(0.76), float32(0.12)
	if integral {
		top, depth = 0.86, 0.2
	}
	g := l.text(n.text, size, false, 0, "", st.color)
	axis := mathAxis * fs
	base := -axis + (top-depth)/2*size
	half := (top + depth) / 2 * size
	b := mbox{w: g.w + 0.06*fs, a: axis + half, d: half - axis}
	b.add(g, 0.03*fs, base)
	return b
}

func (l *mlayout) frac(n *texNode, st mstate) mbox {
	s := st
	switch n.style {
	case 1:
		s.level = styleDisplay
	case 2:
		s.level = max(styleText, st.level)
	}
	inner := s
	inner.level = fracLevel(s.level)
	num := l.node(n.over, inner)
	den := l.node(n.under, inner)
	fs := l.fs(s)
	t := l.rule(fs)
	axis := mathAxis * fs
	var u, v, phi float32
	if s.level == styleDisplay {
		u, v, phi = 0.677*fs, 0.686*fs, 3*t
	} else {
		u, v, phi = 0.394*fs, 0.345*fs, t
	}
	bar := n.mark != "nobar"
	if bar {
		if g := (u - num.d) - (axis + t/2); g < phi {
			u += phi - g
		}
		if g := (axis - t/2) - (den.a - v); g < phi {
			v += phi - g
		}
	} else {
		phi = 3 * t
		if s.level == styleDisplay {
			phi = 7 * t
		}
		if g := (u - num.d) - (den.a - v); g < phi {
			u += (phi - g) / 2
			v += (phi - g) / 2
		}
	}
	pad := 0.12 * fs
	w := max(num.w, den.w) + 0.08*fs
	b := mbox{w: w + 2*pad, a: u + num.a, d: v + den.d}
	b.add(num, pad+(w-num.w)/2, -u)
	b.add(den, pad+(w-den.w)/2, v)
	if bar {
		col := st.color
		b.put(pad, -axis-t/2, func(p *ui.Painter, x, y float32, def ui.Color) {
			p.Fill(ui.Rect{X: x, Y: y, W: w, H: t}, pick(col, def), 0)
		})
	}
	if n.left != "" || n.right != "" {
		return l.fenced(n.left, n.right, b, s)
	}
	return b
}

func (l *mlayout) scripts(n *texNode, st mstate) mbox {
	base := l.node(n.body, st)
	fs := l.fs(st)
	ss := st
	ss.level = scriptLevel(st.level)
	sfs := l.fs(ss)
	var sup, sub mbox
	if n.over != nil {
		sup = l.node(n.over, ss)
	}
	if n.under != nil {
		sub = l.node(n.under, ss)
	}
	if limitsOf(n, st) {
		return l.limits(base, sup, sub, n.over != nil, n.under != nil, fs)
	}
	t := l.rule(fs)
	single := n.body != nil && n.body.kind == texAtom && !n.body.large
	var u, v float32
	if !single {
		u = base.a - 0.386*sfs
		v = base.d + 0.05*sfs
	}
	kern := float32(0)
	if single && atomItalic(n.body) {
		kern = italicKern(n.body.text) * fs
	}
	if n.body != nil && n.body.large && opLimits(n.body.text) < 0 {
		kern = 0.06 * fs
		if st.level == styleDisplay {
			kern = 0.12 * fs
		}
	}
	b := mbox{w: base.w, a: base.a, d: base.d}
	b.add(base, 0, 0)
	switch {
	case n.over != nil && n.under != nil:
		up := float32(0.363)
		if st.level == styleDisplay {
			up = 0.413
		}
		u = max(u, up*fs, sup.d+0.25*mathXH*fs)
		v = max(v, 0.247*fs)
		if g := (u - sup.d) - (sub.a - v); g < 4*t {
			v += 4*t - g
			if psi := 0.8*mathXH*fs - (u - sup.d); psi > 0 {
				u += psi
				v -= psi
			}
		}
		b.add(sup, base.w+kern, -u)
		sx := base.w
		if n.body != nil && n.body.large && opLimits(n.body.text) < 0 {
			sx -= kern * 0.3
		}
		b.add(sub, sx, v)
		b.w = max(base.w+kern+sup.w, sx+sub.w) + 0.05*fs
		b.a = max(b.a, u+sup.a)
		b.d = max(b.d, v+sub.d)
	case n.over != nil:
		up := float32(0.363)
		if st.level == styleDisplay {
			up = 0.413
		}
		u = max(u, up*fs, sup.d+0.25*mathXH*fs)
		b.add(sup, base.w+kern, -u)
		b.w = base.w + kern + sup.w + 0.05*fs
		b.a = max(b.a, u+sup.a)
		b.d = max(b.d, sup.d-u)
	case n.under != nil:
		v = max(v, 0.15*fs, sub.a-0.8*mathXH*fs)
		sx := base.w
		if n.body != nil && n.body.large && opLimits(n.body.text) < 0 {
			sx -= kern * 0.3
		}
		b.add(sub, sx, v)
		b.w = sx + sub.w + 0.05*fs
		b.d = max(b.d, v+sub.d)
		b.a = max(b.a, sub.a-v)
	}
	return b
}

func limitsOf(n *texNode, st mstate) bool {
	if n.limits != 0 {
		return n.limits > 0
	}
	body := n.body
	for body != nil && body.kind == texRow && len(body.kids) == 1 {
		body = body.kids[0]
	}
	if body == nil {
		return false
	}
	if body.kind == texStack && body.limits > 0 {
		return true
	}
	if body.class == texOp && body.limits > 0 {
		return st.level == styleDisplay
	}
	return false
}

func (l *mlayout) limits(base, sup, sub mbox, hasSup, hasSub bool, fs float32) mbox {
	w := max(base.w, sup.w, sub.w)
	b := mbox{w: w, a: base.a, d: base.d}
	b.add(base, (w-base.w)/2, 0)
	if hasSup {
		y := -(base.a + max(0.111*fs, 0.2*fs-sup.d) + sup.d)
		b.add(sup, (w-sup.w)/2, y)
		b.a = -y + sup.a + 0.1*fs
	}
	if hasSub {
		y := base.d + max(0.167*fs, 0.6*fs-sub.a) + sub.a
		b.add(sub, (w-sub.w)/2, y)
		b.d = y + sub.d + 0.1*fs
	}
	return b
}

func (l *mlayout) sqrt(n *texNode, st mstate) mbox {
	body := l.node(n.body, st)
	fs := l.fs(st)
	t := l.rule(fs)
	phi := 1.25 * t
	if st.level == styleDisplay {
		phi = t + 0.25*mathXH*fs
	}
	top := max(body.a, 0.62*fs) + phi + t
	bottom := max(body.d, 0.12*fs) + 0.04*fs
	h := top + bottom
	wr := 0.6*fs + 0.06*max(h-1.1*fs, 0)
	var index mbox
	shift := float32(0)
	if n.over != nil {
		is := st
		is.level = styleScriptScript
		index = l.node(n.over, is)
		shift = max(index.w-0.3*fs, 0)
	}
	b := mbox{w: shift + wr + body.w + 0.1*fs, a: top + 0.04*fs, d: bottom}
	b.add(body, shift+wr+0.02*fs, 0)
	if n.over != nil {
		y := bottom - 0.55*h - index.d
		b.add(index, shift+0.3*fs-index.w, y)
		b.a = max(b.a, -y+index.a)
	}
	col := st.color
	end := b.w
	b.put(shift, 0, func(p *ui.Painter, x, y float32, def ui.Color) {
		c := pick(col, def)
		yT := y - top + t/2
		yB := y + bottom
		hook := (&ui.Path{}).MoveTo(x+0.02*fs, yB-0.4*fs).LineTo(x+0.14*fs, yB-0.47*fs)
		p.StrokePath(hook, t*1.1, c)
		down := (&ui.Path{}).MoveTo(x+0.13*fs, yB-0.47*fs).LineTo(x+0.3*fs, yB-t)
		p.StrokePath(down, t*2, c)
		up := (&ui.Path{}).MoveTo(x+0.3*fs, yB-t).LineTo(x+wr, yT).LineTo(x+end-shift, yT)
		p.StrokePath(up, t, c)
	})
	return b
}

func (l *mlayout) fenced(left, right string, body mbox, st mstate) mbox {
	fs := l.fs(st)
	ld := l.delimiter(left, body.a, body.d, fs, st.color, false)
	rd := l.delimiter(right, body.a, body.d, fs, st.color, false)
	b := mbox{w: ld.w + body.w + rd.w, a: max(ld.a, body.a, rd.a), d: max(ld.d, body.d, rd.d)}
	b.add(ld, 0, 0)
	b.add(body, ld.w, 0)
	b.add(rd, ld.w+body.w, 0)
	return b
}

func (l *mlayout) delimiter(d string, a, dep, fs float32, col ui.Color, exact bool) mbox {
	if d == "" {
		return mbox{w: 0.12 * fs}
	}
	axis := mathAxis * fs
	half := max(a-axis, dep+axis)
	h := max(2*half*0.901, 2*half-0.5*fs)
	if exact {
		h = a + dep
	}
	if !exact && h <= 1.2*fs || !strings.ContainsAny(d, "()[]{}|‖⟨⟩⌊⌋⌈⌉/\\") {
		return l.text(d, fs, false, 0, "", col)
	}
	t := l.rule(fs)
	top := -(axis + h/2)
	bot := h/2 - axis
	var w float32
	switch d {
	case "(", ")":
		w = 0.38*fs + 0.04*h
	case "{", "}":
		w = 0.5 * fs
	case "|", "‖":
		w = 0.28 * fs
		if d == "‖" {
			w = 0.42 * fs
		}
	default:
		w = 0.38 * fs
	}
	b := mbox{w: w, a: -top, d: bot}
	lw := max(t*1.15, 0.065*fs)
	b.put(0, 0, func(p *ui.Painter, x, y float32, def ui.Color) {
		c := pick(col, def)
		y0, y1 := y+top, y+bot
		hh := y1 - y0
		switch d {
		case "(", ")":
			xo, xi := x+w*0.82, x+w*0.18
			th := lw * 1.6
			if d == ")" {
				xo, xi = x+w*0.18, x+w*0.82
				th = -th
			}
			path := (&ui.Path{}).MoveTo(xo, y0).
				CubeTo(xi-(xo-xi)*0.05, y0+hh*0.22, xi-(xo-xi)*0.05, y1-hh*0.22, xo, y1).
				CubeTo(xi+th, y1-hh*0.24, xi+th, y0+hh*0.24, xo, y0).Close()
			p.FillPath(path, c)
		case "[", "]":
			xo, xi := x+w*0.75, x+w*0.3
			if d == "]" {
				xo, xi = x+w*0.25, x+w*0.7
			}
			path := (&ui.Path{}).MoveTo(xo, y0+lw/2).LineTo(xi, y0+lw/2).LineTo(xi, y1-lw/2).LineTo(xo, y1-lw/2)
			p.StrokePath(path, lw, c)
		case "⌊", "⌋", "⌈", "⌉":
			xo, xi := x+w*0.75, x+w*0.3
			if d == "⌋" || d == "⌉" {
				xo, xi = x+w*0.25, x+w*0.7
			}
			path := &ui.Path{}
			if d == "⌊" || d == "⌋" {
				path.MoveTo(xi, y0).LineTo(xi, y1-lw/2).LineTo(xo, y1-lw/2)
			} else {
				path.MoveTo(xi, y1).LineTo(xi, y0+lw/2).LineTo(xo, y0+lw/2)
			}
			p.StrokePath(path, lw, c)
		case "{", "}":
			xr, xm, xl := x+w*0.85, x+w*0.5, x+w*0.15
			if d == "}" {
				xr, xl = x+w*0.15, x+w*0.85
			}
			r := min(w*0.35, hh*0.1)
			ym := (y0 + y1) / 2
			path := (&ui.Path{}).MoveTo(xr, y0+lw/2).
				QuadTo(xm, y0+lw/2, xm, y0+r).LineTo(xm, ym-r).
				QuadTo(xm, ym, xl, ym).QuadTo(xm, ym, xm, ym+r).
				LineTo(xm, y1-r).QuadTo(xm, y1-lw/2, xr, y1-lw/2)
			p.StrokePath(path, lw, c)
		case "|":
			p.Fill(ui.Rect{X: x + w/2 - lw/2, Y: y0, W: lw, H: hh}, c, 0)
		case "‖":
			p.Fill(ui.Rect{X: x + w*0.3 - lw/2, Y: y0, W: lw, H: hh}, c, 0)
			p.Fill(ui.Rect{X: x + w*0.7 - lw/2, Y: y0, W: lw, H: hh}, c, 0)
		case "⟨", "⟩":
			xo, xi := x+w*0.8, x+w*0.2
			if d == "⟩" {
				xo, xi = x+w*0.2, x+w*0.8
			}
			path := (&ui.Path{}).MoveTo(xo, y0).LineTo(xi, (y0+y1)/2).LineTo(xo, y1)
			p.StrokePath(path, lw, c)
		case "/":
			p.StrokePath((&ui.Path{}).MoveTo(x+w*0.85, y0).LineTo(x+w*0.15, y1), lw, c)
		case "\\":
			p.StrokePath((&ui.Path{}).MoveTo(x+w*0.15, y0).LineTo(x+w*0.85, y1), lw, c)
		}
	})
	return b
}

func (l *mlayout) accent(n *texNode, st mstate) mbox {
	body := l.node(n.body, st)
	fs := l.fs(st)
	t := l.rule(fs)
	single := n.body != nil && n.body.kind == texRow && len(n.body.kids) == 1 && n.body.kids[0].kind == texAtom && utf8.RuneCountInString(n.body.kids[0].text) == 1
	if n.body != nil && n.body.kind == texAtom && utf8.RuneCountInString(n.body.text) == 1 {
		single = true
	}
	skew := float32(0)
	if single {
		k := n.body
		if k.kind == texRow {
			k = k.kids[0]
		}
		if atomItalic(k) {
			skew = 0.07 * fs
		}
	}
	b := mbox{w: body.w, a: body.a, d: body.d}
	b.add(body, 0, 0)
	col := st.color
	mark := strings.ToLower(n.mark)
	wide := !single || strings.HasPrefix(mark, "wide") || strings.HasPrefix(mark, "over") || strings.HasPrefix(mark, "under") || mark == "utilde"
	below := strings.HasPrefix(mark, "under") || mark == "utilde"
	aw := min(max(body.w*0.9, 0.4*fs), 0.5*fs)
	if wide {
		aw = max(body.w, 0.4*fs)
	}
	cx := body.w/2 + skew
	var ah float32
	switch mark {
	case "overline", "underline", "bar":
		ah = t
	case "dot", "ddot", "dddot":
		ah = 0.12 * fs
	case "hat", "widehat", "check", "widecheck":
		ah = 0.2 * fs
		if wide {
			ah = 0.26 * fs
		}
	case "tilde", "widetilde", "utilde":
		ah = 0.14 * fs
	default:
		ah = 0.24 * fs
	}
	gap := 0.1 * fs
	if mark == "overline" || mark == "underline" {
		gap = 3 * t
	}
	var y0 float32
	if below {
		y0 = body.d + gap
		b.d = y0 + ah + t
	} else {
		y0 = -(body.a + gap + ah)
		b.a = body.a + gap + ah + t
	}
	lw := t
	b.put(cx-aw/2, y0, func(p *ui.Painter, x, y float32, def ui.Color) {
		c := pick(col, def)
		x1 := x + aw
		switch mark {
		case "overline", "underline", "bar":
			p.Fill(ui.Rect{X: x, Y: y, W: aw, H: t}, c, 0)
		case "dot", "mathring":
			if mark == "mathring" {
				p.StrokePath((&ui.Path{}).Circle(x+aw/2, y+ah/2, ah/2), lw, c)
				return
			}
			p.FillPath((&ui.Path{}).Circle(x+aw/2, y+ah/2, 0.055*fs), c)
		case "ddot":
			p.FillPath((&ui.Path{}).Circle(x+aw/2-0.12*fs, y+ah/2, 0.055*fs), c)
			p.FillPath((&ui.Path{}).Circle(x+aw/2+0.12*fs, y+ah/2, 0.055*fs), c)
		case "dddot":
			for _, dx := range []float32{-0.18, 0, 0.18} {
				p.FillPath((&ui.Path{}).Circle(x+aw/2+dx*fs, y+ah/2, 0.05*fs), c)
			}
		case "hat", "widehat":
			p.StrokePath((&ui.Path{}).MoveTo(x, y+ah).LineTo(x+aw/2, y).LineTo(x1, y+ah), lw, c)
		case "check", "widecheck":
			p.StrokePath((&ui.Path{}).MoveTo(x, y).LineTo(x+aw/2, y+ah).LineTo(x1, y), lw, c)
		case "tilde", "widetilde", "utilde":
			p.StrokePath((&ui.Path{}).MoveTo(x, y+ah*0.8).CubeTo(x+aw*0.3, y-ah*0.4, x+aw*0.7, y+ah*1.4, x1, y+ah*0.2), lw, c)
		case "breve":
			p.StrokePath((&ui.Path{}).MoveTo(x+aw*0.15, y).QuadTo(x+aw/2, y+ah*1.6, x+aw*0.85, y), lw, c)
		case "acute":
			p.StrokePath((&ui.Path{}).MoveTo(x+aw*0.4, y+ah).LineTo(x+aw*0.7, y), lw*1.2, c)
		case "grave":
			p.StrokePath((&ui.Path{}).MoveTo(x+aw*0.3, y).LineTo(x+aw*0.6, y+ah), lw*1.2, c)
		case "overparen", "underparen", "overarc", "overgroup", "undergroup":
			if below {
				p.StrokePath((&ui.Path{}).MoveTo(x, y).QuadTo(x+aw/2, y+ah*1.6, x1, y), lw, c)
			} else {
				p.StrokePath((&ui.Path{}).MoveTo(x, y+ah).QuadTo(x+aw/2, y-ah*0.6, x1, y+ah), lw, c)
			}
		default:
			ym := y + ah/2
			left := strings.Contains(mark, "left")
			right := strings.Contains(mark, "right") || mark == "vec"
			if mark == "overrightarrow" || mark == "underrightarrow" {
				left = false
			}
			head := min(0.2*fs, aw*0.45)
			double := n.mark == "Overrightarrow"
			if double {
				p.Fill(ui.Rect{X: x, Y: ym - 0.08*fs - lw/2, W: aw - lw, H: lw}, c, 0)
				p.Fill(ui.Rect{X: x, Y: ym + 0.08*fs - lw/2, W: aw - lw, H: lw}, c, 0)
			} else {
				p.Fill(ui.Rect{X: x, Y: ym - lw/2, W: aw, H: lw}, c, 0)
			}
			if right {
				p.StrokePath((&ui.Path{}).MoveTo(x1-head, ym-head*0.6).LineTo(x1, ym).LineTo(x1-head, ym+head*0.6), lw, c)
			}
			if left {
				p.StrokePath((&ui.Path{}).MoveTo(x+head, ym-head*0.6).LineTo(x, ym).LineTo(x+head, ym+head*0.6), lw, c)
			}
		}
	})
	return b
}

func (l *mlayout) stack(n *texNode, st mstate) mbox {
	body := l.node(n.body, st)
	fs := l.fs(st)
	t := l.rule(fs)
	ss := st
	ss.level = scriptLevel(st.level)
	if strings.Contains(n.mark, "brace") || strings.Contains(n.mark, "bracket") {
		bh := 0.3 * fs
		b := mbox{w: body.w, a: body.a, d: body.d}
		b.add(body, 0, 0)
		over := strings.HasPrefix(n.mark, "over")
		var y float32
		if over {
			y = -(body.a + 0.12*fs + bh)
			b.a = -y + 0.04*fs
		} else {
			y = body.d + 0.12*fs
			b.d = y + bh + 0.04*fs
		}
		col := st.color
		w := body.w
		bracket := strings.Contains(n.mark, "bracket")
		b.put(0, y, func(p *ui.Painter, x, y float32, def ui.Color) {
			c := pick(col, def)
			y0, y1 := y, y+bh
			if !over {
				y0, y1 = y+bh, y
			}
			if bracket {
				p.StrokePath((&ui.Path{}).MoveTo(x, y1).LineTo(x, y0).LineTo(x+w, y0).LineTo(x+w, y1), t, c)
				return
			}
			ym := (y0 + y1) / 2
			r := min(bh/2, w/6)
			path := (&ui.Path{}).MoveTo(x, y1).QuadTo(x, ym, x+r, ym).LineTo(x+w/2-r, ym).
				QuadTo(x+w/2, ym, x+w/2, y0).QuadTo(x+w/2, ym, x+w/2+r, ym).
				LineTo(x+w-r, ym).QuadTo(x+w, ym, x+w, y1)
			p.StrokePath(path, t*1.3, c)
		})
		return b
	}
	var over, under mbox
	if n.over != nil {
		over = l.node(n.over, ss)
	}
	if n.under != nil {
		under = l.node(n.under, ss)
	}
	w := max(body.w, over.w, under.w)
	b := mbox{w: w, a: body.a, d: body.d}
	b.add(body, (w-body.w)/2, 0)
	if n.over != nil {
		y := -(body.a + 0.12*fs + over.d)
		b.add(over, (w-over.w)/2, y)
		b.a = -y + over.a
	}
	if n.under != nil {
		y := body.d + 0.12*fs + under.a
		b.add(under, (w-under.w)/2, y)
		b.d = y + under.d
	}
	return b
}

func (l *mlayout) arrow(n *texNode, st mstate) mbox {
	fs := l.fs(st)
	t := l.rule(fs)
	ss := st
	ss.level = scriptLevel(st.level)
	var over, under mbox
	if n.over != nil {
		over = l.node(n.over, ss)
	}
	if n.under != nil {
		under = l.node(n.under, ss)
	}
	w := max(max(over.w, under.w)+0.7*fs, 1.5*fs)
	axis := mathAxis * fs
	head := 0.2 * fs
	b := mbox{w: w + 0.2*fs, a: axis + head, d: max(head-axis, 0)}
	x0 := 0.1 * fs
	if n.over != nil && over.w > 0 {
		y := -(axis + 0.16*fs + over.d)
		b.add(over, x0+(w-over.w)/2, y)
		b.a = max(b.a, -y+over.a)
	}
	if n.under != nil && under.w > 0 {
		y := -axis + 0.2*fs + under.a
		b.add(under, x0+(w-under.w)/2, y)
		b.d = max(b.d, y+under.d)
	}
	col := st.color
	mark := n.mark
	b.put(x0, -axis, func(p *ui.Painter, x, y float32, def ui.Color) {
		c := pick(col, def)
		x1 := x + w
		lw := t
		switch mark {
		case "xRightarrow", "xLeftarrow", "xLeftrightarrow", "xlongequal":
			g := 0.09 * fs
			inset := float32(0)
			if mark != "xlongequal" {
				inset = head * 0.5
			}
			l0, l1 := x, x1
			if mark == "xRightarrow" || mark == "xLeftrightarrow" {
				l1 -= inset
			}
			if mark == "xLeftarrow" || mark == "xLeftrightarrow" {
				l0 += inset
			}
			p.Fill(ui.Rect{X: l0, Y: y - g - lw/2, W: l1 - l0, H: lw}, c, 0)
			p.Fill(ui.Rect{X: l0, Y: y + g - lw/2, W: l1 - l0, H: lw}, c, 0)
			if mark == "xRightarrow" || mark == "xLeftrightarrow" {
				p.StrokePath((&ui.Path{}).MoveTo(x1-head, y-head).LineTo(x1, y).LineTo(x1-head, y+head), lw, c)
			}
			if mark == "xLeftarrow" || mark == "xLeftrightarrow" {
				p.StrokePath((&ui.Path{}).MoveTo(x+head, y-head).LineTo(x, y).LineTo(x+head, y+head), lw, c)
			}
		case "xrightleftharpoons", "xleftrightharpoons":
			g := 0.1 * fs
			p.Fill(ui.Rect{X: x, Y: y - g - lw/2, W: w, H: lw}, c, 0)
			p.Fill(ui.Rect{X: x, Y: y + g - lw/2, W: w, H: lw}, c, 0)
			p.StrokePath((&ui.Path{}).MoveTo(x1-head, y-g-head*0.8).LineTo(x1, y-g), lw, c)
			p.StrokePath((&ui.Path{}).MoveTo(x+head, y+g+head*0.8).LineTo(x, y+g), lw, c)
		default:
			p.Fill(ui.Rect{X: x, Y: y - lw/2, W: w, H: lw}, c, 0)
			hs := head * 0.75
			if mark != "xleftarrow" && mark != "xleftharpoonup" && mark != "xleftharpoondown" && mark != "xhookleftarrow" {
				p.StrokePath((&ui.Path{}).MoveTo(x1-hs, y-hs).LineTo(x1, y).LineTo(x1-hs, y+hs), lw, c)
				if mark == "xtwoheadrightarrow" {
					p.StrokePath((&ui.Path{}).MoveTo(x1-2*hs, y-hs).LineTo(x1-hs, y).LineTo(x1-2*hs, y+hs), lw, c)
				}
			}
			if mark == "xleftarrow" || mark == "xleftrightarrow" || mark == "xtofrom" || mark == "xhookleftarrow" {
				p.StrokePath((&ui.Path{}).MoveTo(x+hs, y-hs).LineTo(x, y).LineTo(x+hs, y+hs), lw, c)
			}
			if mark == "xmapsto" {
				p.Fill(ui.Rect{X: x, Y: y - hs, W: lw, H: 2 * hs}, c, 0)
			}
			if mark == "xhookrightarrow" {
				p.StrokePath((&ui.Path{}).MoveTo(x, y).QuadTo(x-hs, y-hs*0.5, x, y-hs), lw, c)
			}
		}
	})
	return b
}

func (l *mlayout) table(n *texNode, st mstate) mbox {
	fs := l.fs(st)
	cs := st
	switch {
	case n.small:
		cs.level = scriptLevel(st.level)
	case n.mark == "aligned" || n.mark == "gathered" || n.style == 1:
		cs.level = st.level
	default:
		cs.level = max(st.level, styleText)
	}
	cfs := l.fs(cs)
	stretch := float32(1)
	gap := float32(0)
	switch n.mark {
	case "cases":
		stretch = 1.2
	case "aligned", "gathered":
		gap = 0.25 * cfs
	case "substack":
		stretch = 0.85
	}
	strutA, strutD := 0.84*stretch*cfs, 0.36*stretch*cfs
	if n.small {
		strutA, strutD = 0.7*cfs, 0.25*cfs
	}
	ncol := 0
	for _, r := range n.rows {
		ncol = max(ncol, len(r))
	}
	if ncol == 0 {
		return mbox{}
	}
	cells := make([][]mbox, len(n.rows))
	widths := make([]float32, ncol)
	as := make([]float32, len(n.rows))
	ds := make([]float32, len(n.rows))
	for i, r := range n.rows {
		cells[i] = make([]mbox, len(r))
		as[i], ds[i] = strutA, strutD
		for j, cell := range r {
			var kids []*texNode
			if cell != nil {
				kids = cell.kids
				if cell.kind != texRow {
					kids = []*texNode{cell}
				}
			}
			cb := l.row(kids, cs, n.mark == "aligned" && j%2 == 1)
			cells[i][j] = cb
			widths[j] = max(widths[j], cb.w)
			as[i] = max(as[i], cb.a)
			ds[i] = max(ds[i], cb.d)
		}
	}
	colGap := 1.0 * cfs
	edge := float32(0)
	switch n.mark {
	case "array":
		edge = 0.5 * cfs
	case "cases":
		colGap = 1.0 * cfs
	case "substack", "gathered":
		colGap = 0.5 * cfs
	}
	if n.small {
		colGap = 0.6 * cfs
	}
	xs := make([]float32, ncol)
	x := edge
	for j := range widths {
		if j > 0 {
			switch {
			case n.mark == "aligned" && j%2 == 1:
			case n.mark == "aligned":
				x += 1.2 * cfs
			default:
				x += colGap
			}
		}
		xs[j] = x
		x += widths[j]
	}
	total := x + edge
	var height float32
	for i := range n.rows {
		if i > 0 {
			height += gap
		}
		height += as[i] + ds[i]
	}
	axis := mathAxis * fs
	top := -(axis + height/2)
	b := mbox{w: total, a: -top, d: height/2 - axis}
	y := top
	for i, r := range cells {
		if i > 0 {
			y += gap
		}
		base := y + as[i]
		for j, cb := range r {
			align := byte('c')
			switch {
			case n.align == "":
			case j < len(n.align):
				align = n.align[j]
			case n.mark == "aligned":
				align = n.align[j%2]
			default:
				align = n.align[len(n.align)-1]
			}
			cx := xs[j]
			switch align {
			case 'c':
				cx += (widths[j] - cb.w) / 2
			case 'r':
				cx += widths[j] - cb.w
			}
			b.add(cb, cx, base)
		}
		y = base + ds[i]
	}
	if n.left != "" || n.right != "" {
		inner := b
		if n.left != "" {
			pad := 0.1 * cfs
			inner = mbox{w: b.w + 2*pad, a: b.a, d: b.d}
			inner.add(b, pad, 0)
		}
		return l.fenced(n.left, n.right, inner, st)
	}
	return b
}

func (l *mlayout) boxed(n *texNode, st mstate) mbox {
	body := l.node(n.body, st)
	fs := l.fs(st)
	t := l.rule(fs)
	pad := 0.3 * fs
	b := mbox{w: body.w + 2*pad, a: max(body.a, 0.7*fs) + pad, d: max(body.d, 0.2*fs) + pad}
	col := st.color
	bg := ui.Color{}
	if n.color != "" {
		bg = parseColor(n.color, ui.Color{})
	}
	w, h, a := b.w, b.a+b.d, b.a
	b.put(0, 0, func(p *ui.Painter, x, y float32, def ui.Color) {
		r := ui.Rect{X: x + t/2, Y: y - a + t/2, W: w - t, H: h - t}
		if bg.A > 0 {
			p.Fill(r, bg, 0)
			return
		}
		p.Stroke(r, pick(col, def), 0, t)
	})
	b.add(body, pad, 0)
	return b
}

func (l *mlayout) cancel(n *texNode, st mstate) mbox {
	body := l.node(n.body, st)
	fs := l.fs(st)
	t := l.rule(fs)
	b := body
	b.items = append([]mitem(nil), body.items...)
	col := st.color
	w, a, d := body.w, body.a, body.d
	mark := n.mark
	b.put(0, 0, func(p *ui.Painter, x, y float32, def ui.Color) {
		c := pick(col, def)
		if mark == "sout" {
			p.Fill(ui.Rect{X: x, Y: y - mathAxis*fs - t/2, W: w, H: t}, c, 0)
			return
		}
		if mark != "bcancel" {
			p.StrokePath((&ui.Path{}).MoveTo(x, y+d).LineTo(x+w, y-a), t, c)
		}
		if mark != "cancel" {
			p.StrokePath((&ui.Path{}).MoveTo(x, y-a).LineTo(x+w, y+d), t, c)
		}
	})
	return b
}

var cssColors = map[string]string{
	"red": "#df0030", "blue": "#0000ff", "green": "#008000", "black": "#000000", "white": "#ffffff",
	"gray": "#808080", "grey": "#808080", "orange": "#ff8000", "purple": "#800080", "yellow": "#e6c200",
	"cyan": "#00b7eb", "magenta": "#ff00ff", "brown": "#8b4513", "pink": "#ff69b4", "teal": "#008080",
	"violet": "#8f00ff", "olive": "#808000", "navy": "#000080", "maroon": "#800000", "lime": "#32cd32",
	"darkgreen": "#006400", "darkblue": "#00008b", "darkred": "#8b0000", "darkorange": "#ff8c00",
	"lightgray": "#a0a0a0", "lightgrey": "#a0a0a0", "darkgray": "#555555", "darkgrey": "#555555",
	"gold": "#d4a017", "indigo": "#4b0082", "crimson": "#dc143c", "royalblue": "#4169e1", "skyblue": "#4ea8de",
	"forestgreen": "#228b22", "tomato": "#ff6347", "coral": "#ff7f50", "salmon": "#fa8072",
}

func parseColor(s string, def ui.Color) ui.Color {
	s = strings.ToLower(strings.TrimSpace(s))
	if hex, ok := cssColors[s]; ok {
		s = hex
	}
	if strings.HasPrefix(s, "#") && (len(s) == 4 || len(s) == 7) {
		if len(s) == 4 {
			s = "#" + strings.Repeat(s[1:2], 2) + strings.Repeat(s[2:3], 2) + strings.Repeat(s[3:4], 2)
		}
		var v [3]uint8
		for i := range v {
			x, ok := hexByte(s[1+2*i : 3+2*i])
			if !ok {
				return def
			}
			v[i] = x
		}
		return ui.RGB(v[0], v[1], v[2])
	}
	return def
}

func hexByte(s string) (uint8, bool) {
	var v uint8
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			v = v*16 + c - '0'
		case c >= 'a' && c <= 'f':
			v = v*16 + c - 'a' + 10
		default:
			return 0, false
		}
	}
	return v, true
}

type formulaKey struct {
	tex    string
	size   float32
	font   string
	locale string
}

type formulaCache map[formulaKey]*mbox

func (fc *formulaCache) get(c *ui.Context, tex string, size float32, font string) *mbox {
	k := formulaKey{tex, size, font, c.Locale()}
	if b, ok := (*fc)[k]; ok {
		return b
	}
	if *fc == nil || len(*fc) > 512 {
		*fc = formulaCache{}
	}
	l := &mlayout{c: c, size: size, font: font}
	b := l.node(parseTeX(tex), mstate{level: styleDisplay})
	if math.IsNaN(float64(b.w)) {
		b = mbox{}
	}
	(*fc)[k] = &b
	return &b
}
