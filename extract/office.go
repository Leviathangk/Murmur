package extract

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var errDamaged = errors.New("文件已损坏，无法读取")

// maxPart is the most bytes a part of an Office file unpacks to: a file
// packed to bomb its reader is not read whole.
const maxPart = 512 << 20

// office is the parts of an Office file, a zip of XML.
type office struct{ files map[string]*zip.File }

func openOffice(data []byte) (*office, error) {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errDamaged
	}
	o := &office{files: map[string]*zip.File{}}
	for _, f := range z.File {
		o.files[strings.TrimPrefix(f.Name, "/")] = f
	}
	return o, nil
}

// open returns a decoder of the XML of the part at name, or nil when the
// file has none.
func (o *office) open(name string) (*xml.Decoder, func()) {
	f := o.files[name]
	if f == nil {
		return nil, func() {}
	}
	rc, err := f.Open()
	if err != nil {
		return nil, func() {}
	}
	d := xml.NewDecoder(io.LimitReader(rc, maxPart))
	d.Strict = false
	return d, func() { rc.Close() }
}

// rels returns the targets of the relationships of the part at name, by
// their IDs, as paths in the file.
func (o *office) rels(name string) map[string]string {
	dir, file := path.Split(name)
	d, done := o.open(dir + "_rels/" + file + ".rels")
	defer done()
	out := map[string]string{}
	if d == nil {
		return out
	}
	for {
		tok, err := d.Token()
		if err != nil {
			return out
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "Relationship" {
			target := attr(se, "Target")
			if strings.HasPrefix(target, "/") {
				target = target[1:]
			} else {
				target = path.Join(dir, target)
			}
			out[attr(se, "Id")] = target
		}
	}
}

// attr returns the value of the attribute of e of the given local name.
func attr(e xml.StartElement, local string) string {
	for _, a := range e.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

// relID returns the ID of the relationship e refers to, its r:id, which
// is not its id.
func relID(e xml.StartElement) string {
	for _, a := range e.Attr {
		if a.Name.Local == "id" && a.Name.Space != "" {
			return a.Value
		}
	}
	return ""
}

// readDocx writes the text of a Word document: its paragraphs, headings
// marked with #, items of lists with -, and tables as Markdown's.
func readDocx(w *writer, data []byte) error {
	o, err := openOffice(data)
	if err != nil {
		return err
	}
	d, done := o.open("word/document.xml")
	defer done()
	if d == nil {
		return errDamaged
	}
	headings := docxHeadings(o)

	type table struct {
		rows [][]string
		row  []string
	}
	var (
		paras  []*strings.Builder // the paragraphs being read: text boxes are within others
		tables []*table
		cell   *strings.Builder // the cell being read
		prefix string           // what the paragraph read starts with
		run    int              // the depth of runs, whose tabs are text
		inT    bool             // in a run's text
		skip   int              // the depth in a part not read
	)
	// emit writes a paragraph, a heading or a table: those stand apart,
	// as Markdown needs them to.
	emit := func(s string, apart bool) {
		if cell != nil && len(tables) > 0 {
			if cell.Len() > 0 && s != "" {
				cell.WriteString(" ")
			}
			cell.WriteString(s)
			return
		}
		if apart {
			s = "\n" + s + "\n"
		}
		w.WriteString(s + "\n")
	}
	text := func(s string) {
		if n := len(paras); n > 0 {
			paras[n-1].WriteString(s)
		}
	}
	for !w.full() {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errDamaged
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if skip > 0 {
				skip++
				continue
			}
			switch t.Name.Local {
			case "Fallback", "delText", "instrText", "footnoteReference", "commentReference":
				// Fallback repeats a drawing's text for older readers.
				skip = 1
			case "p":
				paras = append(paras, &strings.Builder{})
				prefix = ""
			case "pStyle":
				if n := headings[attr(t, "val")]; n > 0 {
					prefix = strings.Repeat("#", n) + " "
				}
			case "numPr":
				if prefix == "" {
					prefix = "- "
				}
			case "r":
				run++
			case "t":
				inT = run > 0
			case "tab":
				if run > 0 {
					text("\t")
				}
			case "br", "cr":
				if run > 0 {
					text("\n")
				}
			case "noBreakHyphen":
				text("-")
			case "tbl":
				tables = append(tables, &table{})
			case "tr":
				if n := len(tables); n > 0 {
					tables[n-1].row = nil
				}
			case "tc":
				cell = &strings.Builder{}
			}
		case xml.EndElement:
			if skip > 0 {
				skip--
				continue
			}
			switch t.Name.Local {
			case "p":
				if n := len(paras); n > 0 {
					s := strings.TrimSpace(paras[n-1].String())
					paras = paras[:n-1]
					if s != "" {
						emit(prefix+s, strings.HasPrefix(prefix, "#"))
					}
					prefix = ""
				}
			case "r":
				run--
			case "t":
				inT = false
			case "tc":
				if n := len(tables); n > 0 && cell != nil {
					tables[n-1].row = append(tables[n-1].row, cell.String())
				}
				cell = nil
			case "tr":
				if n := len(tables); n > 0 {
					tables[n-1].rows = append(tables[n-1].rows, tables[n-1].row)
				}
			case "tbl":
				if n := len(tables); n > 0 {
					t := tables[n-1]
					tables = tables[:n-1]
					emit(markdownTable(t.rows), true)
				}
			}
		case xml.CharData:
			if skip == 0 && inT {
				text(string(t))
			}
		}
	}
	return nil
}

// docxHeadings returns the levels of the headings of a Word document, by
// the IDs of their styles: Word in Chinese names them 1, 2…, in English
// Heading1, Heading2…, and both call them "heading 1" within.
func docxHeadings(o *office) map[string]int {
	out := map[string]int{}
	d, done := o.open("word/styles.xml")
	defer done()
	if d == nil {
		return out
	}
	id := ""
	for {
		tok, err := d.Token()
		if err != nil {
			return out
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "style":
			id = attr(se, "styleId")
		case "name":
			name := strings.ToLower(attr(se, "val"))
			if name == "title" {
				out[id] = 1
			} else if s, ok := strings.CutPrefix(name, "heading "); ok {
				if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= 6 {
					out[id] = n
				}
			}
		}
	}
}

// markdownTable returns rows as a table of Markdown, the first as its
// header.
func markdownTable(rows [][]string) string {
	cols := 0
	for _, r := range rows {
		cols = max(cols, len(r))
	}
	if cols == 0 {
		return ""
	}
	esc := strings.NewReplacer("|", `\|`, "\n", " ")
	var b strings.Builder
	for i, r := range rows {
		b.WriteString("|")
		for j := range cols {
			s := ""
			if j < len(r) {
				s = strings.TrimSpace(esc.Replace(r[j]))
			}
			b.WriteString(" " + s + " |")
		}
		b.WriteString("\n")
		if i == 0 {
			b.WriteString("|" + strings.Repeat(" --- |", cols) + "\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// readPptx writes the text of a presentation, slide by slide in their
// order, each under its number.
func readPptx(w *writer, data []byte) error {
	o, err := openOffice(data)
	if err != nil {
		return err
	}
	slides := pptxSlides(o)
	if len(slides) == 0 {
		return errDamaged
	}
	for i, name := range slides {
		if w.full() {
			break
		}
		d, done := o.open(name)
		if d == nil {
			done()
			continue
		}
		var paras []string
		var p *strings.Builder
		inT := false
		for {
			tok, err := d.Token()
			if err != nil {
				break
			}
			switch t := tok.(type) {
			case xml.StartElement:
				switch t.Name.Local {
				case "p":
					p = &strings.Builder{}
				case "t":
					inT = true
				case "br":
					if p != nil {
						p.WriteString("\n")
					}
				}
			case xml.EndElement:
				if t.Name.Local == "t" {
					inT = false
				}
				if t.Name.Local == "p" && p != nil {
					if s := strings.TrimSpace(p.String()); s != "" {
						paras = append(paras, s)
					}
					p = nil
				}
			case xml.CharData:
				if p != nil && inT {
					p.WriteString(string(t))
				}
			}
		}
		done()
		w.WriteString(fmt.Sprintf("## 第 %d 页\n\n", i+1))
		if len(paras) > 0 {
			w.WriteString(strings.Join(paras, "\n") + "\n\n")
		}
	}
	return nil
}

// pptxSlides returns the parts of the slides of a presentation, in the
// order it shows them, or by their numbers without that order.
func pptxSlides(o *office) []string {
	const pres = "ppt/presentation.xml"
	rels := o.rels(pres)
	var out []string
	if d, done := o.open(pres); d != nil {
		for {
			tok, err := d.Token()
			if err != nil {
				break
			}
			if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "sldId" {
				if target := rels[relID(se)]; o.files[target] != nil {
					out = append(out, target)
				}
			}
		}
		done()
	}
	if len(out) > 0 {
		return out
	}
	for name := range o.files {
		if slideFile.MatchString(name) {
			out = append(out, name)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return slideNumber(a) - slideNumber(b) })
	return out
}

var slideFile = regexp.MustCompile(`^ppt/slides/slide(\d+)\.xml$`)

func slideNumber(name string) int {
	n, _ := strconv.Atoi(slideFile.FindStringSubmatch(name)[1])
	return n
}

// readXlsx writes the cells of a workbook, sheet by sheet under their
// names, as CSV: numbers as Excel shows them in General, dates as dates.
func readXlsx(w *writer, data []byte) error {
	o, err := openOffice(data)
	if err != nil {
		return err
	}
	const book = "xl/workbook.xml"
	rels := o.rels(book)
	type sheet struct{ name, part string }
	var sheets []sheet
	date1904 := false
	d, done := o.open(book)
	if d == nil {
		done()
		return errDamaged
	}
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "workbookPr":
			v := attr(se, "date1904")
			date1904 = v == "1" || v == "true"
		case "sheet":
			if attr(se, "state") == "" || attr(se, "state") == "visible" {
				sheets = append(sheets, sheet{attr(se, "name"), rels[relID(se)]})
			}
		}
	}
	done()
	strs := sharedStrings(o)
	dates := dateStyles(o)
	for _, s := range sheets {
		if w.full() {
			break
		}
		d, done := o.open(s.part)
		if d == nil {
			done()
			continue
		}
		w.WriteString("## " + s.name + "\n\n")
		var row []string
		var c struct {
			col       int
			typ       string
			style     int
			v         strings.Builder
			inV, inIs bool
		}
		cw := csv.NewWriter(writerFunc(w.WriteString))
		next := 0 // the column of a cell without a reference
		for !w.full() {
			tok, err := d.Token()
			if err != nil {
				break
			}
			switch t := tok.(type) {
			case xml.StartElement:
				switch t.Name.Local {
				case "row":
					row, next = nil, 0
				case "c":
					c.col, c.typ, c.inV, c.inIs = next, attr(t, "t"), false, false
					if col, ok := column(attr(t, "r")); ok {
						c.col = col
					}
					c.style, _ = strconv.Atoi(attr(t, "s"))
					c.v.Reset()
				case "v":
					c.inV = true
				case "is":
					c.inIs = true
				case "rPh":
					c.inIs = false // the reading of Japanese, not the text
				}
			case xml.EndElement:
				switch t.Name.Local {
				case "v":
					c.inV = false
				case "is":
					c.inIs = false
				case "c":
					date := c.style >= 0 && c.style < len(dates) && dates[c.style]
					s := cellText(c.typ, c.v.String(), strs, date, date1904)
					if s != "" && c.col < 1<<14 {
						for len(row) <= c.col {
							row = append(row, "")
						}
						row[c.col] = s
					}
					next = c.col + 1
				case "row":
					if len(row) > 0 {
						cw.Write(row)
						cw.Flush()
					}
				}
			case xml.CharData:
				if c.inV || c.inIs {
					c.v.Write(t)
				}
			}
		}
		done()
		w.WriteString("\n")
	}
	return nil
}

type writerFunc func(string)

func (f writerFunc) Write(b []byte) (int, error) {
	f(string(b))
	return len(b), nil
}

// column returns the column of the cell of reference ref, as B3, from 0.
func column(ref string) (int, bool) {
	n, i := 0, 0
	for ; i < len(ref) && ref[i] >= 'A' && ref[i] <= 'Z'; i++ {
		n = n*26 + int(ref[i]-'A'+1)
	}
	return n - 1, i > 0
}

// cellText returns the text of a cell of type typ and value v, whose style
// makes it a date when date is.
func cellText(typ, v string, strs []string, date, date1904 bool) string {
	switch typ {
	case "s":
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && i >= 0 && i < len(strs) {
			return strs[i]
		}
		return ""
	case "b":
		if strings.TrimSpace(v) == "1" {
			return "TRUE"
		}
		return "FALSE"
	case "str", "inlineStr", "e", "d":
		return v
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return v
	}
	if date {
		return excelDate(f, date1904)
	}
	// As General shows it: 15 digits, which takes the noise of binary
	// fractions away.
	f, _ = strconv.ParseFloat(strconv.FormatFloat(f, 'g', 15, 64), 64)
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// excelDate returns the date of serial number f, in days since Excel's
// epoch.
func excelDate(f float64, date1904 bool) string {
	epoch := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	if date1904 {
		epoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	days := math.Floor(f)
	secs := math.Round((f - days) * 86400)
	t := epoch.AddDate(0, 0, int(days)).Add(time.Duration(secs) * time.Second)
	switch {
	case days == 0 && !date1904:
		return t.Format("15:04:05")
	case secs == 0:
		return t.Format("2006-01-02")
	}
	return t.Format("2006-01-02 15:04:05")
}

// sharedStrings returns the texts of a workbook's cells of strings.
func sharedStrings(o *office) []string {
	d, done := o.open("xl/sharedStrings.xml")
	defer done()
	if d == nil {
		return nil
	}
	var out []string
	var si strings.Builder
	inT, phonetic := false, false
	for {
		tok, err := d.Token()
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				si.Reset()
			case "t":
				inT = true
			case "rPh":
				phonetic = true
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "si":
				out = append(out, si.String())
			case "t":
				inT = false
			case "rPh":
				phonetic = false
			}
		case xml.CharData:
			if inT && !phonetic {
				si.Write(t)
			}
		}
	}
}

// dateStyles reports, by the index of each style of a workbook's cells,
// whether it shows a number as a date or a time.
func dateStyles(o *office) []bool {
	d, done := o.open("xl/styles.xml")
	defer done()
	if d == nil {
		return nil
	}
	custom := map[int]bool{}
	var out []bool
	inXfs := false
	for {
		tok, err := d.Token()
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "numFmt":
				id, _ := strconv.Atoi(attr(t, "numFmtId"))
				custom[id] = dateFormat(attr(t, "formatCode"))
			case "cellXfs":
				inXfs = true
			case "xf":
				if inXfs {
					id, _ := strconv.Atoi(attr(t, "numFmtId"))
					date, ok := custom[id]
					if !ok {
						// Excel's own formats of dates and times, those of
						// Chinese, Japanese and Korean among them.
						date = id >= 14 && id <= 22 || id >= 27 && id <= 36 || id >= 45 && id <= 47 || id >= 50 && id <= 58
					}
					out = append(out, date)
				}
			}
		case xml.EndElement:
			if t.Name.Local == "cellXfs" {
				inXfs = false
			}
		}
	}
}

var quoted = regexp.MustCompile(`"[^"]*"|\[[^\]]*\]|\\.|_.|\*.`)

// dateFormat reports whether a format of numbers shows dates or times.
func dateFormat(code string) bool {
	code = strings.ToLower(quoted.ReplaceAllString(code, ""))
	if strings.Contains(code, "general") {
		return false
	}
	return strings.ContainsAny(code, "ymdhs")
}
