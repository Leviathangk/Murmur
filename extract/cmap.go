package extract

import (
	"io"
	"strconv"
	"strings"

	"github.com/ledongthuc/pdf"
)

// cmap is a CMap of a PDF: the codes of a font, and the text of each, as
// its ToUnicode map tells it.
type cmap struct {
	space  []codeRange
	chars  map[string]string
	ranges []bfRange
}

// codeRange is a range of codes of the same length, each byte of which
// is between those of lo and hi.
type codeRange struct{ lo, hi string }

func (r codeRange) has(code string) bool {
	if len(code) != len(r.lo) || len(r.hi) != len(r.lo) {
		return false
	}
	for i := range len(code) {
		if code[i] < r.lo[i] || code[i] > r.hi[i] {
			return false
		}
	}
	return true
}

// bfRange maps the codes from lo to hi to the text of dst, in UTF-16, and
// the next characters, or to the texts of dsts.
type bfRange struct {
	lo, hi string
	dst    string
	dsts   []string
}

func codeValue(code string) int {
	n := 0
	for i := range len(code) {
		n = n<<8 | int(code[i])
	}
	return n
}

// lookup returns the text of code.
func (m *cmap) lookup(code string) (string, bool) {
	if s, ok := m.chars[code]; ok {
		return s, true
	}
	for _, r := range m.ranges {
		if len(code) != len(r.lo) || code < r.lo || code > r.hi {
			continue
		}
		off := codeValue(code) - codeValue(r.lo)
		if r.dsts != nil {
			if off < len(r.dsts) {
				return r.dsts[off], true
			}
			return "", false
		}
		if r.dst == "" {
			return "", false
		}
		// The last character goes up with the code.
		b := []byte(r.dst)
		n := len(b)
		if n%2 == 1 {
			b = append([]byte{0}, b...)
			n++
		}
		v := int(b[n-2])<<8 | int(b[n-1]) + off
		b[n-2], b[n-1] = byte(v>>8), byte(v)
		return utf16String(string(b)), true
	}
	return "", false
}

// maxCMap is the most bytes of a CMap read.
const maxCMap = 16 << 20

// readCMap reads the CMap of stream v: its codespace, and the text of its
// codes, of bfchar and bfrange.
func readCMap(v pdf.Value) (m *cmap) {
	defer func() {
		if recover() != nil {
			m = nil
		}
	}()
	rc := v.Reader()
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxCMap))
	if err != nil && len(data) == 0 {
		return nil
	}
	m = &cmap{chars: map[string]string{}}
	var args []token // the operands of the next operator
	for _, t := range tokens(data) {
		if t.kind != keyword {
			args = append(args, t)
			continue
		}
		switch t.s {
		case "endcodespacerange":
			for i := 0; i+1 < len(args); i += 2 {
				if args[i].kind == str && args[i+1].kind == str && len(args[i].s) > 0 && len(args[i].s) <= 4 {
					m.space = append(m.space, codeRange{args[i].s, args[i+1].s})
				}
			}
		case "endbfchar":
			for i := 0; i+1 < len(args); i += 2 {
				if args[i].kind == str && args[i+1].kind == str {
					m.chars[args[i].s] = utf16String(args[i+1].s)
				}
			}
		case "endbfrange":
			for i := 0; i+2 < len(args); i += 3 {
				lo, hi, dst := args[i], args[i+1], args[i+2]
				if lo.kind != str || hi.kind != str || len(lo.s) != len(hi.s) {
					continue
				}
				r := bfRange{lo: lo.s, hi: hi.s}
				switch dst.kind {
				case str:
					r.dst = dst.s
				case array:
					for _, s := range dst.arr {
						r.dsts = append(r.dsts, utf16String(s))
					}
				default:
					continue
				}
				m.ranges = append(m.ranges, r)
			}
		}
		args = args[:0]
	}
	return m
}

type tokenKind int

const (
	keyword tokenKind = iota
	str
	number
	name
	array
)

// token is a token of PostScript, as a CMap is written in: a string holds
// its bytes, an array its strings.
type token struct {
	kind tokenKind
	s    string
	arr  []string
}

// tokens splits the PostScript of a CMap into tokens, leaving out its
// dictionaries' marks and procedures' braces.
func tokens(data []byte) []token {
	var out []token
	var arr []string
	inArray := false
	emit := func(t token) {
		if inArray {
			if t.kind == str {
				arr = append(arr, t.s)
			}
			return
		}
		out = append(out, t)
	}
	for i := 0; i < len(data); {
		c := data[i]
		switch {
		case isSpace(c):
			i++
		case c == '%':
			for i < len(data) && data[i] != '\n' && data[i] != '\r' {
				i++
			}
		case c == '<' && i+1 < len(data) && data[i+1] == '<', c == '>' && i+1 < len(data) && data[i+1] == '>':
			i += 2
		case c == '<':
			j := i + 1
			for j < len(data) && data[j] != '>' {
				j++
			}
			emit(token{kind: str, s: unhex(data[i+1 : j])})
			i = j + 1
		case c == '(':
			s, n := literal(data[i:])
			emit(token{kind: str, s: s})
			i += n
		case c == '[':
			inArray, arr = true, nil
			i++
		case c == ']':
			if inArray {
				inArray = false
				out = append(out, token{kind: array, arr: arr})
			}
			i++
		case c == '{' || c == '}' || c == '>' || c == ')':
			i++
		default:
			j := i + 1
			for j < len(data) && !isSpace(data[j]) && !isDelim(data[j]) {
				j++
			}
			word := string(data[i:j])
			switch {
			case c == '/':
				emit(token{kind: name, s: word[1:]})
			case isNumber(word):
				emit(token{kind: number, s: word})
			default:
				emit(token{kind: keyword, s: word})
			}
			i = j
		}
	}
	return out
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == 0
}

func isDelim(c byte) bool { return strings.IndexByte("()<>[]{}/%", c) >= 0 }

func isNumber(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

// unhex returns the bytes of the digits of a hexadecimal string, a last
// digit alone followed by 0.
func unhex(h []byte) string {
	var b []byte
	hi, odd := byte(0), false
	for _, c := range h {
		var v byte
		switch {
		case c >= '0' && c <= '9':
			v = c - '0'
		case c >= 'a' && c <= 'f':
			v = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			v = c - 'A' + 10
		default:
			continue
		}
		if odd {
			b = append(b, hi<<4|v)
		} else {
			hi = v
		}
		odd = !odd
	}
	if odd {
		b = append(b, hi<<4)
	}
	return string(b)
}

// literal returns the bytes of the literal string that data starts with,
// in parentheses, and how many bytes it takes.
func literal(data []byte) (string, int) {
	var b []byte
	depth := 0
	for i := 0; i < len(data); i++ {
		c := data[i]
		switch {
		case c == '(':
			if depth > 0 {
				b = append(b, c)
			}
			depth++
		case c == ')':
			depth--
			if depth == 0 {
				return string(b), i + 1
			}
			b = append(b, c)
		case c == '\\' && i+1 < len(data):
			i++
			switch e := data[i]; e {
			case 'n':
				b = append(b, '\n')
			case 'r':
				b = append(b, '\r')
			case 't':
				b = append(b, '\t')
			case 'b':
				b = append(b, '\b')
			case 'f':
				b = append(b, '\f')
			case '\r', '\n':
			default:
				if e >= '0' && e <= '7' {
					v := 0
					for n := 0; n < 3 && i < len(data) && data[i] >= '0' && data[i] <= '7'; n++ {
						v = v*8 + int(data[i]-'0')
						i++
					}
					i--
					b = append(b, byte(v))
				} else {
					b = append(b, e)
				}
			}
		default:
			b = append(b, c)
		}
	}
	return string(b), len(data)
}

// standardWidths returns the widths of the printable ASCII of the font of
// PDF's standard fonts named name, which a PDF need not give, and of
// others: Times, Courier, and Helvetica for the rest.
func standardWidths(name string) (map[int]float64, float64) {
	if i := strings.IndexByte(name, '+'); i >= 0 {
		name = name[i+1:]
	}
	out := map[int]float64{}
	if strings.Contains(name, "Courier") {
		return out, 600
	}
	table := helvetica
	if strings.Contains(name, "Times") {
		table = times
	}
	for i, w := range table {
		out[32+i] = float64(w)
	}
	return out, 500
}

// The widths of the characters from the space to the tilde, in
// thousandths of an em.
var (
	helvetica = [...]int{
		278, 278, 355, 556, 556, 889, 667, 222, 333, 333, 389, 584, 278, 333, 278, 278,
		556, 556, 556, 556, 556, 556, 556, 556, 556, 556, 278, 278, 584, 584, 584, 556,
		1015, 667, 667, 722, 722, 667, 611, 778, 722, 278, 500, 667, 556, 833, 722, 778,
		667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611, 278, 278, 278, 469, 556,
		222, 556, 556, 500, 556, 556, 278, 556, 556, 222, 222, 500, 222, 833, 556, 556,
		556, 556, 333, 500, 278, 556, 500, 722, 500, 500, 500, 334, 260, 334, 584,
	}
	times = [...]int{
		250, 333, 408, 500, 500, 833, 778, 333, 333, 333, 500, 564, 250, 333, 250, 278,
		500, 500, 500, 500, 500, 500, 500, 500, 500, 500, 278, 278, 564, 564, 564, 444,
		921, 722, 667, 667, 722, 611, 556, 722, 722, 333, 389, 722, 611, 889, 722, 722,
		556, 722, 667, 556, 611, 722, 722, 944, 722, 722, 611, 333, 278, 333, 469, 500,
		333, 444, 500, 444, 500, 444, 333, 500, 500, 278, 278, 500, 278, 778, 500, 500,
		500, 500, 333, 389, 278, 500, 500, 722, 500, 500, 444, 480, 200, 480, 541,
	}
)
