package extract

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// The PDF is parsed by github.com/ledongthuc/pdf: its objects, streams
// and encryption. The text is read here, as the package prefers a font's
// encoding to its ToUnicode map, which macOS and Word write the text in,
// and shows no spaces nor lines where a page leaves room for them.

var errPDF = errors.New("PDF 已损坏或格式不支持，无法读取")

// maxForms is how deep forms are read within others.
const maxForms = 8

// readPDF writes the text of a PDF, page by page, as each page draws it:
// lines where the text moves to another, spaces where it leaves a gap.
func readPDF(w *writer, data []byte) (err error) {
	if !bytes.Contains(data[:min(len(data), 1024)], []byte("%PDF-")) {
		return errors.New("不是 PDF 文件")
	}
	defer func() {
		if recover() != nil {
			err = errPDF
		}
	}()
	if bytes.HasPrefix(data, []byte("%PDF-2.")) {
		// The package reads PDF 1.x, which 2.0 reads as.
		data = append([]byte("%PDF-1.7"), data[len("%PDF-2.0"):]...)
	}
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		if strings.Contains(err.Error(), "encrypt") {
			return errors.New("PDF 已加密（有密码或权限保护），请去掉密码或另存一份不加密的 PDF 后再添加")
		}
		return errPDF
	}
	var pages []pageRef
	listPages(r.Trailer().Key("Root").Key("Pages"), pdf.Value{}, 0, &pages)
	for _, pg := range pages {
		if w.full() {
			break
		}
		var p page
		p.read(pg)
		w.WriteString(p.b.String() + "\n\n")
	}
	return nil
}

// pageRef is a page, with the resources it inherits.
type pageRef struct{ v, res pdf.Value }

// listPages adds the pages of the tree under node to out, in their order.
func listPages(node, res pdf.Value, depth int, out *[]pageRef) {
	if depth > 64 || len(*out) >= 1<<16 {
		return
	}
	if r := node.Key("Resources"); r.Kind() == pdf.Dict {
		res = r
	}
	if kids := node.Key("Kids"); kids.Kind() == pdf.Array {
		for i := range kids.Len() {
			listPages(kids.Index(i), res, depth+1, out)
		}
		return
	}
	if node.Kind() == pdf.Dict {
		*out = append(*out, pageRef{node, res})
	}
}

type matrix [6]float64 // a b c d e f, as PDF writes them

var identity = matrix{1, 0, 0, 1, 0, 0}

func (m matrix) mul(n matrix) matrix {
	return matrix{
		m[0]*n[0] + m[1]*n[2], m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2], m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4], m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

func translate(x, y float64) matrix { return matrix{1, 0, 0, 1, x, y} }

func matrixOf(args []pdf.Value) matrix {
	var m matrix
	for i := range m {
		m[i] = args[i].Float64()
	}
	return m
}

// state is the graphics state that places text.
type state struct {
	ctm   matrix
	font  *font
	size  float64 // of the font, in text space
	cs    float64 // character spacing
	ws    float64 // word spacing
	scale float64 // horizontal scaling
	lead  float64
	rise  float64
}

// resources are the fonts and forms a content stream draws with.
type resources struct {
	v     pdf.Value
	fonts map[string]*font
}

func (r *resources) font(name string) *font {
	if f, ok := r.fonts[name]; ok {
		return f
	}
	var f *font
	if fv := r.v.Key("Font").Key(name); fv.Kind() == pdf.Dict {
		f = newFont(fv)
	}
	r.fonts[name] = f
	return f
}

// page is the text of a page, as it is read.
type page struct {
	b       strings.Builder
	started bool
	ex, ey  float64 // where the last glyph ended, on the page
	size    float64 // the size of its font there
	last    rune    // the last character written
	forms   int     // how deep in forms the reading is
}

// read reads the text of pg, as far as it can be read.
func (p *page) read(pg pageRef) {
	defer func() { recover() }()
	res := &resources{v: pg.res, fonts: map[string]*font{}}
	p.content(pg.v.Key("Contents"), res, state{ctm: identity, scale: 1})
}

// content reads the text that strm draws, from g.
func (p *page) content(strm pdf.Value, res *resources, g state) {
	if strm.Kind() != pdf.Stream && strm.Kind() != pdf.Array {
		return
	}
	var saved []state
	tm, tlm := identity, identity
	nextLine := func() {
		tlm = translate(0, -g.lead).mul(tlm)
		tm = tlm
	}
	pdf.Interpret(strm, func(stk *pdf.Stack, op string) {
		args := make([]pdf.Value, stk.Len())
		for i := len(args) - 1; i >= 0; i-- {
			args[i] = stk.Pop()
		}
		num := func(i int) float64 {
			if i < len(args) {
				return args[i].Float64()
			}
			return 0
		}
		switch op {
		case "q":
			saved = append(saved, g)
		case "Q":
			if n := len(saved); n > 0 {
				g, saved = saved[n-1], saved[:n-1]
			}
		case "cm":
			if len(args) == 6 {
				g.ctm = matrixOf(args).mul(g.ctm)
			}
		case "BT":
			tm, tlm = identity, identity
		case "Tf":
			if len(args) == 2 {
				g.font, g.size = res.font(args[0].Name()), num(1)
			}
		case "Tc":
			g.cs = num(0)
		case "Tw":
			g.ws = num(0)
		case "Tz":
			g.scale = num(0) / 100
		case "TL":
			g.lead = num(0)
		case "Ts":
			g.rise = num(0)
		case "TD":
			g.lead = -num(1)
			fallthrough
		case "Td":
			tlm = translate(num(0), num(1)).mul(tlm)
			tm = tlm
		case "Tm":
			if len(args) == 6 {
				tlm = matrixOf(args)
				tm = tlm
			}
		case "T*":
			nextLine()
		case "Tj":
			if len(args) == 1 {
				p.show(&g, &tm, args[0].RawString())
			}
		case "'":
			nextLine()
			if len(args) == 1 {
				p.show(&g, &tm, args[0].RawString())
			}
		case `"`:
			if len(args) == 3 {
				g.ws, g.cs = num(0), num(1)
				nextLine()
				p.show(&g, &tm, args[2].RawString())
			}
		case "TJ":
			if len(args) != 1 {
				return
			}
			for i := range args[0].Len() {
				x := args[0].Index(i)
				if x.Kind() == pdf.String {
					p.show(&g, &tm, x.RawString())
				} else {
					tm = translate(-x.Float64()/1000*g.size*g.scale, 0).mul(tm)
				}
			}
		case "Do":
			if len(args) == 1 {
				p.form(res, args[0].Name(), g)
			}
		}
	})
}

// form reads the text of the form of the given name, drawn from g.
func (p *page) form(res *resources, name string, g state) {
	x := res.v.Key("XObject").Key(name)
	if p.forms >= maxForms || x.Kind() != pdf.Stream || x.Key("Subtype").Name() != "Form" {
		return
	}
	if m := x.Key("Matrix"); m.Len() == 6 {
		g.ctm = matrixOf([]pdf.Value{m.Index(0), m.Index(1), m.Index(2), m.Index(3), m.Index(4), m.Index(5)}).mul(g.ctm)
	}
	if r := x.Key("Resources"); r.Kind() == pdf.Dict {
		res = &resources{v: r, fonts: map[string]*font{}}
	}
	p.forms++
	p.content(x, res, g)
	p.forms--
}

// show reads the glyphs of string s, each where it is drawn, moving the
// text matrix past them.
func (p *page) show(g *state, tm *matrix, s string) {
	f := g.font
	if f == nil {
		return
	}
	for len(s) > 0 {
		n := f.codeLen(s)
		code := s[:n]
		s = s[n:]
		trm := matrix{g.size * g.scale, 0, 0, g.size, 0, 0}.mul(*tm).mul(g.ctm)
		adv := f.width(code)/1000*g.size + g.cs
		if code == " " {
			adv += g.ws
		}
		*tm = translate(adv*g.scale, 0).mul(*tm)
		end := tm.mul(g.ctm)
		p.put(f.text(code), trm, end[4], end[5])
	}
}

// put writes the text of a glyph drawn by trm, which ends at ex, ey: on a
// line of its own when it is above or below the last, after a space when
// it leaves a gap after it.
func (p *page) put(text string, trm matrix, ex, ey float64) {
	size := max(math.Hypot(trm[2], trm[3]), 0.1)
	dx, dy := math.Hypot(trm[0], trm[1]), 0.0
	if dx > 0 {
		dx, dy = trm[0]/dx, trm[1]/dx // the direction the text goes
	} else {
		dx = 1
	}
	x, y := trm[4], trm[5]
	if text == "" {
		// A glyph of no character, as a space some write without one,
		// leaves a gap.
		return
	}
	if p.started {
		mx, my := x-p.ex, y-p.ey
		along, across := mx*dx+my*dy, my*dx-mx*dy
		sz := max(size, p.size)
		first, _ := utf8.DecodeRuneInString(text)
		gap := 0.15 * sz
		if wide(p.last) && wide(first) {
			gap = sz // characters of Chinese are set apart to justify lines
		}
		switch {
		case math.Abs(across) > 0.5*sz:
			if across < -2*sz {
				p.write("\n\n")
			} else {
				p.write("\n")
			}
		case along > gap || along < -sz:
			p.write(" ")
		}
	}
	p.write(text)
	p.started, p.ex, p.ey, p.size = true, ex, ey, size
}

// write writes s, but for a space after a space or a line: those at the
// ends of lines go as the text is cleaned.
func (p *page) write(s string) {
	if s == " " && (p.last == ' ' || p.last == '\n' || p.last == 0) {
		return
	}
	p.b.WriteString(s)
	p.last, _ = utf8.DecodeLastRuneInString(s)
}

// wide reports whether r is of the scripts that leave no spaces between
// words.
func wide(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) ||
		r >= 0x3000 && r <= 0x303f || r >= 0xff00 && r <= 0xffef
}

// charset is how the codes of a CID font without a ToUnicode map, but of
// a CMap that Adobe defines, are text.
type charset int

const (
	noCharset charset = iota
	utf16Charset
	gbCharset
)

// font decodes the codes of a font into text, and tells their widths.
type font struct {
	lib       *pdf.Font // the package's, for the encodings of simple fonts
	cid       bool
	space     []codeRange // the codes of a CID font, nil for two bytes each
	charset   charset
	toUnicode *cmap
	widths    map[int]float64 // by code for a simple font, by CID otherwise
	dw        float64         // the width of codes without one
	scale     float64         // the widths' unit, in thousandths of the text space
}

func newFont(v pdf.Value) *font {
	f := &font{lib: &pdf.Font{V: v}, widths: map[int]float64{}, scale: 1}
	if tu := v.Key("ToUnicode"); tu.Kind() == pdf.Stream {
		f.toUnicode = readCMap(tu)
	}
	if v.Key("Subtype").Name() == "Type0" {
		f.cid, f.dw = true, 1000
		identity := false
		switch enc := v.Key("Encoding"); enc.Kind() {
		case pdf.Name:
			name := enc.Name()
			identity = strings.HasPrefix(name, "Identity-")
			switch {
			case strings.Contains(name, "UCS2"), strings.Contains(name, "UTF16"):
				f.charset = utf16Charset
			case strings.HasPrefix(name, "GB"):
				f.charset = gbCharset
			}
		case pdf.Stream:
			if m := readCMap(enc); m != nil && len(m.space) > 0 {
				f.space, identity = m.space, true
			}
		}
		if !identity && f.charset == noCharset && f.toUnicode != nil {
			f.space = f.toUnicode.space
		}
		desc := v.Key("DescendantFonts").Index(0)
		if dw := desc.Key("DW"); dw.Kind() == pdf.Integer || dw.Kind() == pdf.Real {
			f.dw = dw.Float64()
		}
		if identity {
			cidWidths(desc.Key("W"), f.widths)
		}
		return f
	}
	first := int(v.Key("FirstChar").Int64())
	ws := v.Key("Widths")
	for i := range min(ws.Len(), 256) {
		f.widths[first+i] = ws.Index(i).Float64()
	}
	f.dw = v.Key("FontDescriptor").Key("MissingWidth").Float64()
	if ws.Len() == 0 {
		f.widths, f.dw = standardWidths(v.Key("BaseFont").Name())
	}
	if m := v.Key("FontMatrix"); v.Key("Subtype").Name() == "Type3" && m.Len() == 6 {
		f.scale = m.Index(0).Float64() * 1000
	}
	return f
}

// cidWidths reads the widths of a CID font, as W gives them: a CID and the
// widths of it and those after it, or two CIDs and the width of them and
// those between.
func cidWidths(w pdf.Value, out map[int]float64) {
	for i := 0; i < w.Len(); {
		c := int(w.Index(i).Int64())
		if next := w.Index(i + 1); next.Kind() == pdf.Array {
			for j := range min(next.Len(), 1<<16) {
				out[c+j] = next.Index(j).Float64()
			}
			i += 2
			continue
		}
		last, width := int(w.Index(i+1).Int64()), w.Index(i+2).Float64()
		for c := c; c <= min(last, c+1<<16); c++ {
			out[c] = width
		}
		i += 3
	}
}

// codeLen returns the length of the code that s starts with.
func (f *font) codeLen(s string) int {
	if !f.cid {
		return 1
	}
	switch f.charset {
	case utf16Charset:
		if len(s) >= 4 && s[0] >= 0xd8 && s[0] <= 0xdb {
			return 4
		}
	case gbCharset:
		switch {
		case s[0] < 0x81 || s[0] == 0xff || len(s) < 2:
			return 1
		case s[1] >= 0x30 && s[1] <= 0x39 && len(s) >= 4:
			return 4
		}
	}
	if f.space != nil {
		for n := 1; n <= 4 && n <= len(s); n++ {
			for _, r := range f.space {
				if r.has(s[:n]) {
					return n
				}
			}
		}
		return 1
	}
	return min(2, len(s))
}

// width returns the width of the glyph of code, in thousandths of the text
// space.
func (f *font) width(code string) float64 {
	c := 0
	for i := range len(code) {
		c = c<<8 | int(code[i])
	}
	w, ok := f.widths[c]
	if !ok {
		w = f.dw
	}
	return w * f.scale
}

// text returns the text of the glyph of code: none for a glyph that is no
// character, as a code without a mapping.
func (f *font) text(code string) string {
	s, ok := "", false
	if f.toUnicode != nil {
		s, ok = f.toUnicode.lookup(code)
		if !ok && len(code) == 1 {
			// A two-byte map of a simple font's codes.
			s, ok = f.toUnicode.lookup("\x00" + code)
		}
	}
	if !ok {
		switch {
		case f.charset == utf16Charset:
			s = utf16String(code)
		case f.charset == gbCharset:
			b, _ := simplifiedchinese.GB18030.NewDecoder().Bytes([]byte(code))
			s = string(b)
		case !f.cid:
			s = f.lib.Encoder().Decode(code)
		}
	}
	if s == "\u00a0" {
		return " "
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == utf8.RuneError || r < 0x20 || r >= 0x7f && r < 0xa0 || r == 0xad:
		case radicals[r] != 0:
			b.WriteRune(radicals[r])
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func utf16String(b string) string {
	if len(b)%2 == 1 {
		b = "\x00" + b
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
	}
	return string(utf16.Decode(u))
}
