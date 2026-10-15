package extract

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
)

func read(t *testing.T, name string, data []byte) string {
	t.Helper()
	text, more, err := Read(name, data, 100000)
	if err != nil || more {
		t.Fatalf("%s: %v, more %v", name, err, more)
	}
	return text
}

func TestText(t *testing.T) {
	// Lines end in \n, without spaces after them, and blank lines between
	// paragraphs are one; the first line keeps its indent.
	if got := read(t, "main.go", []byte("\xef\xbb\xbf\n\n\tfunc main() {}  \r\n\r\n\r\n\r\n// 完\r\n")); got != "\tfunc main() {}\n\n// 完" {
		t.Errorf("UTF-8: %q", got)
	}
	// Excel in Chinese saves CSV in GB18030, Notepad UTF-16.
	gb, _ := simplifiedchinese.GB18030.NewEncoder().String("姓名,城市\n张三,北京\n")
	if got := read(t, "名单.csv", []byte(gb)); got != "姓名,城市\n张三,北京" {
		t.Errorf("GB18030: %q", got)
	}
	u16, _ := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder().String("你好 notes")
	if got := read(t, "notes.txt", []byte(u16)); got != "你好 notes" {
		t.Errorf("UTF-16: %q", got)
	}
	// A file of no extension is text when it is.
	if got := read(t, "Makefile", []byte("all:\n\tgo build\n")); got != "all:\n\tgo build" {
		t.Errorf("Makefile: %q", got)
	}

	for name, data := range map[string][]byte{
		"app.exe":     {'M', 'Z', 0x90, 0, 3, 0, 0, 0},
		"archive.zip": []byte("PK\x03\x04\x14\x00\x00\x00"),
	} {
		if _, _, err := Read(name, data, 100); err == nil || !strings.Contains(err.Error(), "不是支持的文件") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, _, err := Read("empty.txt", []byte(" \n\n "), 100); err == nil || err.Error() != "没有读到文字" {
		t.Errorf("empty: %v", err)
	}
	if _, _, err := Read("旧.doc", []byte("\xd0\xcf\x11\xe0"), 100); err == nil || !strings.Contains(err.Error(), "另存为 .docx") {
		t.Errorf(".doc: %v", err)
	}
}

// Text past max characters is left out.
func TestMore(t *testing.T) {
	long := strings.Repeat("一二三四五六七八九十\n", 1000)
	text, more, err := Read("long.txt", []byte(long), 25)
	if err != nil || !more || text != "一二三四五六七八九十\n一二三四五六七八九十\n一二三" {
		t.Errorf("%q, more %v, %v", text, more, err)
	}
	if n := utf8.RuneCountInString(text); n != 25 {
		t.Errorf("%d characters", n)
	}
	// What is not read whole is cut too.
	text, more, err = Read("long.docx", docx(t, strings.Repeat(`<w:p><w:r><w:t>一段很长的文字</w:t></w:r></w:p>`, 1000)), 50)
	if err != nil || !more || utf8.RuneCountInString(text) > 50 {
		t.Errorf("%q, more %v, %v", text, more, err)
	}
}

// zipOf returns a zip of the files given, by their names.
func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for name, s := range files {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(s))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const wordNS = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006"`

// docx returns a Word document whose body is body, whose heading 1 style
// is named 1, as Word in Chinese names it.
func docx(t *testing.T, body string) []byte {
	return zipOf(t, map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?><w:document ` + wordNS + `><w:body>` + body + `</w:body></w:document>`,
		"word/styles.xml": `<w:styles ` + wordNS + `><w:style w:type="paragraph" w:styleId="1"><w:name w:val="heading 1"/></w:style>` +
			`<w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/></w:style></w:styles>`,
	})
}

func TestDocx(t *testing.T) {
	body := `<w:p><w:pPr><w:pStyle w:val="1"/></w:pPr><w:r><w:t>周报</w:t></w:r></w:p>` +
		`<w:p><w:pPr><w:tabs><w:tab w:val="left" w:pos="720"/></w:tabs></w:pPr><w:r><w:t xml:space="preserve">本周完成了 </w:t></w:r>` +
		`<w:r><w:rPr><w:b/></w:rPr><w:t>发送文档</w:t></w:r><w:del><w:r><w:delText>删掉的</w:delText></w:r></w:del>` +
		`<w:r><w:t>。</w:t><w:tab/><w:t>完</w:t><w:br/><w:t>下一行</w:t></w:r></w:p>` +
		`<w:p><w:pPr><w:pStyle w:val="Heading2"/></w:pPr><w:r><w:t>进展</w:t></w:r></w:p>` +
		`<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t>支持 PDF</w:t></w:r></w:p>` +
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>模块</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>进度</w:t></w:r></w:p></w:tc></w:tr>` +
		`<w:tr><w:tc><w:p><w:r><w:t>解析</w:t></w:r></w:p><w:p><w:r><w:t>和界面</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>80|90%</w:t></w:r></w:p></w:tc></w:tr></w:tbl>` +
		// A text box, which Word repeats for older readers.
		`<w:p><w:r><w:t>见图</w:t></w:r><w:r><mc:AlternateContent><mc:Choice Requires="wps"><w:drawing><w:txbxContent><w:p><w:r><w:t>框里的字</w:t></w:r></w:p></w:txbxContent></w:drawing></mc:Choice>` +
		`<mc:Fallback><w:pict><w:txbxContent><w:p><w:r><w:t>框里的字</w:t></w:r></w:p></w:txbxContent></w:pict></mc:Fallback></mc:AlternateContent></w:r></w:p>`
	want := "# 周报\n\n本周完成了 发送文档。\t完\n下一行\n\n## 进展\n\n- 支持 PDF\n\n" +
		"| 模块 | 进度 |\n| --- | --- |\n| 解析 和界面 | 80\\|90% |\n\n框里的字\n见图"
	if got := read(t, "周报.docx", docx(t, body)); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if _, _, err := Read("坏.docx", []byte("not a zip"), 100); err == nil || err.Error() != "文件已损坏，无法读取" {
		t.Errorf("damaged: %v", err)
	}
}

func TestPptx(t *testing.T) {
	const ns = `xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	slide := func(paras ...string) string {
		s := `<p:sld ` + ns + `><p:cSld><p:spTree><p:sp><p:txBody>`
		for _, p := range paras {
			s += `<a:p><a:r><a:t>` + p + `</a:t></a:r></a:p>`
		}
		return s + `</p:txBody></p:sp></p:spTree></p:cSld></p:sld>`
	}
	// The slides show in the order of the presentation, not of their files.
	data := zipOf(t, map[string]string{
		"ppt/presentation.xml":            `<p:presentation ` + ns + `><p:sldIdLst><p:sldId id="256" r:id="rId3"/><p:sldId id="257" r:id="rId2"/></p:sldIdLst></p:presentation>`,
		"ppt/_rels/presentation.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId2" Target="slides/slide1.xml"/><Relationship Id="rId3" Target="/ppt/slides/slide2.xml"/></Relationships>`,
		"ppt/slides/slide1.xml":           slide("结尾", "谢谢"),
		"ppt/slides/slide2.xml":           slide("标题页"),
	})
	if got, want := read(t, "演示.pptx", data), "## 第 1 页\n\n标题页\n\n## 第 2 页\n\n结尾\n谢谢"; got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestXlsx(t *testing.T) {
	const ns = `xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	data := zipOf(t, map[string]string{
		"xl/workbook.xml": `<workbook ` + ns + `><sheets><sheet name="销售" sheetId="1" r:id="rId1"/><sheet name="隐藏" sheetId="2" state="hidden" r:id="rId2"/>` +
			`<sheet name="说明" sheetId="3" r:id="rId3"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Target="worksheets/sheet1.xml"/>` +
			`<Relationship Id="rId2" Target="worksheets/sheet2.xml"/><Relationship Id="rId3" Target="worksheets/sheet3.xml"/></Relationships>`,
		"xl/sharedStrings.xml": `<sst ` + ns + `><si><t>日期</t></si><si><t>产品</t></si><si><r><t>苹果</t></r><r><t>, 红</t></r></si><si><t>香蕉</t><rPh><t>バナナ</t></rPh></si></sst>`,
		"xl/styles.xml": `<styleSheet ` + ns + `><numFmts><numFmt numFmtId="164" formatCode="yyyy/m/d\ h:mm"/><numFmt numFmtId="165" formatCode="&quot;￥&quot;#,##0.00"/></numFmts>` +
			`<cellXfs><xf numFmtId="0"/><xf numFmtId="14"/><xf numFmtId="164"/><xf numFmtId="165"/></cellXfs></styleSheet>`,
		"xl/worksheets/sheet1.xml": `<worksheet ` + ns + `><sheetData>` +
			`<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c><c r="C1" t="inlineStr"><is><t>数量</t></is></c></row>` +
			`<row r="2"><c r="A2" s="1"><v>46296</v></c><c r="B2" t="s"><v>2</v></c><c r="C2" s="3"><v>0.30000000000000004</v></c></row>` +
			`<row r="3"><c r="A3" s="2"><v>46297.604166666664</v></c><c r="B3" t="s"><v>3</v></c><c r="C3"><f>C2*2</f><v>0.6</v></c><c r="D3" t="b"><v>1</v></c></row>` +
			`<row r="4"/><row r="5"><c r="E5" t="str"><v>远处</v></c></row></sheetData></worksheet>`,
		"xl/worksheets/sheet2.xml": `<worksheet ` + ns + `><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>不该出现</t></is></c></row></sheetData></worksheet>`,
		"xl/worksheets/sheet3.xml": `<worksheet ` + ns + `><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>第二张表</t></is></c></row></sheetData></worksheet>`,
	})
	want := "## 销售\n\n日期,产品,数量\n2026-10-01,\"苹果, 红\",0.3\n2026-10-02 14:30:00,香蕉,0.6,TRUE\n,,,,远处\n\n## 说明\n\n第二张表"
	if got := read(t, "表.xlsx", data); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// pdfOf returns a PDF of one page, drawing content with resources, and of
// the objects given after its first four: the catalog, the pages, the page
// and its content.
func pdfOf(content, resources string, objs ...string) []byte {
	objs = append([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources " + resources + " /Contents 4 0 R >>",
		pdfStream("", content),
	}, objs...)
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

func pdfStream(dict, data string) string {
	return fmt.Sprintf("<< %s /Length %d >>\nstream\n%s\nendstream", dict, len(data), data)
}

func TestPDF(t *testing.T) {
	// Helvetica, of no widths given: a gap in TJ is a space, Td down a line
	// a line, further a paragraph.
	helvetica := "<< /Font << /F1 5 0 R >> >>"
	font := "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>"
	got := read(t, "a.pdf", pdfOf("BT /F1 12 Tf 72 700 Td [(Hello)-280(world)] TJ 0 -14 Td (Second line) Tj 0 -40 Td (New paragraph) Tj ET", helvetica, font))
	if want := "Hello world\nSecond line\n\nNew paragraph"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// A CID font: its ToUnicode map gives the text, in ranges and alone; a
	// glyph it does not map, as a space, leaves a gap.
	cmap := "/CIDInit /ProcSet findresource begin 12 dict begin begincmap\n" +
		"1 begincodespacerange <0000> <FFFF> endcodespacerange\n" +
		"2 beginbfchar <0001> <4F60> <0002> <597D> endbfchar\n" +
		"2 beginbfrange <0010> <0012> <0041> <0020> <0021> [<2F42> <2ED3>] endbfrange\n" +
		"endcmap CMapName currentdict /CMap defineresource pop end end"
	cid := "<< /Type /Font /Subtype /Type0 /BaseFont /PingFangSC /Encoding /Identity-H /ToUnicode 6 0 R " +
		"/DescendantFonts [<< /Type /Font /Subtype /CIDFontType2 /BaseFont /PingFangSC /DW 1000 /W [16 [600 600 600] 3 [300]] >>] >>"
	content := "BT /F1 10 Tf 72 700 Td <0010001100120003000100020020> Tj " +
		// Characters of Chinese set apart to justify a line join.
		"[<0021> -500 <0001>] TJ ET"
	got = read(t, "cid.pdf", pdfOf(content, helvetica, cid, pdfStream("", cmap)))
	if want := "ABC 你好文长你"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// CMaps of Adobe's, without ToUnicode: UCS-2 and GBK.
	ucs2 := "<< /Type /Font /Subtype /Type0 /BaseFont /STSong /Encoding /UniGB-UCS2-H /DescendantFonts [<< /Subtype /CIDFontType0 /BaseFont /STSong >>] >>"
	gbk := "<< /Type /Font /Subtype /Type0 /BaseFont /SimSun /Encoding /GBK-EUC-H /DescendantFonts [<< /Subtype /CIDFontType0 /BaseFont /SimSun >>] >>"
	got = read(t, "cjk.pdf", pdfOf("BT /F1 12 Tf 72 700 Td <4F60597D> Tj /F2 12 Tf 0 -20 Td <C4E3BAC3> Tj (OK) Tj ET", "<< /Font << /F1 5 0 R /F2 6 0 R >> >>", ucs2, gbk))
	if want := "你好\n你好OK"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// The text of forms, with their own fonts.
	form := pdfStream("/Type /XObject /Subtype /Form /BBox [0 0 100 100] /Matrix [1 0 0 1 0 -100] /Resources << /Font << /F9 6 0 R >> >>", "BT /F9 12 Tf 72 700 Td (In a form) Tj ET")
	got = read(t, "form.pdf", pdfOf("BT /F1 12 Tf 72 700 Td (Page) Tj ET /X1 Do", "<< /Font << /F1 6 0 R >> /XObject << /X1 5 0 R >> >>", form, font))
	if want := "Page\n\nIn a form"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// PDF 2.0 reads as 1.x.
	pdf2 := pdfOf("BT /F1 12 Tf 72 700 Td (Version 2) Tj ET", helvetica, font)
	copy(pdf2, "%PDF-2.0")
	if got := read(t, "v2.pdf", pdf2); got != "Version 2" {
		t.Errorf("PDF 2.0: %q", got)
	}

	// A page of no text, as a scan.
	if _, _, err := Read("scan.pdf", pdfOf("q 612 0 0 792 0 0 cm Q", "<< >>"), 100); err == nil || !strings.Contains(err.Error(), "扫描件") {
		t.Errorf("scan: %v", err)
	}
	for name, data := range map[string]string{"x.pdf": "hello", "y.pdf": "%PDF-1.4\ngarbage"} {
		if _, _, err := Read(name, []byte(data), 100); err == nil {
			t.Errorf("%s read", name)
		}
	}
}

// PDFs as macOS writes them: the fonts' encodings name glyphs, which the
// ToUnicode maps tell; some characters map to radicals.
func TestPDFOfMacOS(t *testing.T) {
	data, err := os.ReadFile("testdata/macos.pdf")
	if err != nil {
		t.Fatal(err)
	}
	got := read(t, "macos.pdf", data)
	for _, s := range []string{"季度报告", "一、总体情况\n本季度营业收入为 1,234 万元，同比增长 15%。", "The quick brown fox jumps over the lazy dog."} {
		if !strings.Contains(got, s) {
			t.Errorf("no %q in\n%s", s, got)
		}
	}
	data, err = os.ReadFile("testdata/password.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Read("password.pdf", data, 100); err == nil || !strings.Contains(err.Error(), "密码") {
		t.Errorf("password: %v", err)
	}
}
