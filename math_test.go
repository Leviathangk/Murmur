package main

import (
	"context"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"

	"github.com/Qsnh/Murmur/llm"
)

func TestPlainMath(t *testing.T) {
	for _, tc := range []struct{ tex, want string }{
		{`f(x)=x^3-3x^2+2`, "f(x) = x³ − 3x² + 2"},
		{`f'(x)=3x(x-2)`, "f′(x) = 3x(x − 2)"},
		{`x=0,\quad x=2`, "x = 0,\u2003x = 2"},
		{`(-\infty,0),\ (2,+\infty)`, "(−∞, 0), (2, +∞)"},
		{`n(\text{MnO}_2)=\frac{17.4}{87}=0.2\ \text{mol}`, "n(MnO₂) = 17.4/87 = 0.2 mol"},
		{`\text{MnO}_2 + 4\text{HCl(浓)} \xrightarrow{\Delta} \text{MnCl}_2 + \text{Cl}_2\uparrow + 2\text{H}_2\text{O}`, "MnO₂ + 4HCl(浓) —Δ→ MnCl₂ + Cl₂↑ + 2H₂O"},
		{`\ce{MnO2 + 4HCl(浓) ->[\Delta] MnCl2 + Cl2 ^ + 2H2O}`, "MnO₂ + 4HCl(浓) —Δ→ MnCl₂ + Cl₂↑ + 2H₂O"},
		{`\ce{SO4^2- + Ba^2+ -> BaSO4 v}`, "SO₄²⁻ + Ba²⁺ → BaSO₄↓"},
		{`\ce{CuSO4.5H2O}`, "CuSO₄·5H₂O"},
		{`\frac{x+1}{2x}`, "(x + 1)/(2x)"},
		{`\dfrac{\sqrt{3}}{2}`, "√3/2"},
		{`\sqrt{x^2+1}+\sqrt[3]{8}`, "√(x² + 1) + ∛8"},
		{`\sum_{i=1}^{n} i^2`, "∑ᵢ₌₁ⁿ i²"},
		{`\lim_{x\to 0}\frac{\sin x}{x}`, "lim(x→0) (sin x)/x"},
		{`e^{i\pi}+1=0`, "e^(iπ) + 1 = 0"},
		{`a_{n+1}=2a_n`, "aₙ₊₁ = 2aₙ"},
		{`\angle ABC=90^\circ`, "∠ABC = 90°"},
		{`x\in\mathbb{R},\ \alpha\ne\beta`, "x ∈ ℝ, α ≠ β"},
		{`\left(\frac{a}{b}\right)^2`, "(a/b)²"},
		{`\vec{a}\cdot\vec{b}`, "a\u20d7 ⋅ b\u20d7"},
		{`\overline{AB}`, "A\u0305B\u0305"},
		{`\{x \mid x > 0\}`, "{x | x > 0}"},
		{`-x`, "−x"},
		{`x=-\frac{b}{2a}`, "x = −b/(2a)"},
		{`x_{1,2}`, "x₁,₂"},
		{`\binom{n}{k}`, "C(n, k)"},
		{`\begin{cases} x, & x\ge0\\ -x, & x<0\end{cases}`, "{ x, x ≥ 0; −x, x < 0"},
		{`\begin{aligned} a &= b\\ &= c\end{aligned}`, "a = b; = c"},
		{`50\%`, "50%"},
		{`\pu{9.8 m/s^2}`, "9.8 m/s²"},
		{`\unknowncmd x`, `\unknowncmd x`},
		{`2\text{H}_2\tex`, "2H₂"},
		{``, ""},
	} {
		if got := plainMath(tc.tex); got != tc.want {
			t.Errorf("plainMath(%q)\n = %q\nwant %q", tc.tex, got, tc.want)
		}
	}
}

func shownText(d *parser.Document) string {
	var b strings.Builder
	for i := 1; i < d.Len(); i++ {
		for _, n := range d.Inlines(i) {
			switch n.Kind {
			case parser.Text:
				b.WriteString(n.Text)
			case parser.Math:
				b.WriteString("[math " + n.Text + "]")
			}
		}
	}
	return b.String()
}

func TestInlineMathMarkdown(t *testing.T) {
	for _, tex := range []string{
		`f(x)`, `(-\infty,0)`, `[0,1]`, `a*b*c`, `x_1+x_2`, `\{1,2\}`, `\$5`, `a<b>c`, `|x|`,
		`\#`, `1.`, `\text{a\_b}`, `x^{*}`, `\backslash(`, `\overline{AB}`, `\lbrack 0, 1\rbrack(`, `\&`,
		`\text{a@b.cd}`, `\text{www.a.com}`, `\text{http://a.com}`,
	} {
		src := "前 \\(" + tex + "\\) 后"
		d := parser.New(mathExtensions)
		d.Set(src)
		out := typesetInline(d, false)
		shown := parser.New(mathExtensions)
		shown.Set(out)
		if got, want := shownText(shown), "前 "+plainMath(tex)+" 后"; got != want {
			t.Errorf("%q typeset as %q, which shows %q, want %q", tex, out, got, want)
		}
	}
}

func TestTypesetBlocks(t *testing.T) {
	for _, src := range []string{"\\(1\\). 已知", "上一行\n\\(1\\). 已知", "\\(\\text{1)}\\) 已知", "> \\(1\\). 已知"} {
		d := parser.New(mathExtensions)
		d.Set(src)
		shown := parser.New(mathExtensions)
		shown.Set(typesetInline(d, false))
		for i := 1; i < shown.Len(); i++ {
			if k := shown.Block(i).Kind; k == parser.List || k == parser.Item {
				t.Errorf("%q typeset as %q, a list", src, shown.Source())
			}
		}
	}
}

func TestTeXLimits(t *testing.T) {
	for _, tc := range []struct{ tex, want string }{
		{`\unicode{0x41}`, "A"},
		{`\unicode{x41}`, "A"},
		{`\unicode{65}`, "A"},
		{`a\hspace{1e30em}b`, "a\u2003\u2003\u2003\u2003b"},
		{`a\hspace{Infem}b`, "a\u2003\u2003\u2003\u2003b"},
		{`a\kern99999999999em b`, "a\u2003\u2003\u2003\u2003b"},
	} {
		if got := plainMath(tc.tex); got != tc.want {
			t.Errorf("plainMath(%q) = %q, want %q", tc.tex, got, tc.want)
		}
	}
	start := time.Now()
	plainMath(strings.Repeat(`\sqrt[{`, 30) + "x" + strings.Repeat(`}]{y}`, 30))
	if d := time.Since(start); d > time.Second {
		t.Errorf("nested roots take %v", d)
	}
}

func TestTypesetInline(t *testing.T) {
	src := "# 求 \\(f(x)\\)\n\n用 `\\(x\\)` 写公式，价格 $5 和 $10，$5/$10，$x^2$ 与 $$y$$，$0.2\\ \\text{mol}$、$25^\\circ\\mathrm{C}$、$4.48$。\n\n$$\n\\frac{1}{2}\n$$\n\n| a | b |\n|---|---|\n| \\(\\alpha\\) | 2 |\n"
	d := parser.New(mathExtensions)
	d.Set(src)
	got := typesetInline(d, false)
	want := "# 求 <i>f</i>(<i>x</i>)\n\n用 `\\(x\\)` 写公式，价格 $5 和 $10，$5/$10，<i>x</i>² 与 <i>y</i>，&#48;\\.2 mol、&#50;5°C、$4.48$。\n\n$$\n\\frac{1}{2}\n$$\n\n| a | b |\n|---|---|\n| <i>α</i> | 2 |\n"
	if got != want {
		t.Errorf("typeset\n%q\nwant\n%q", got, want)
	}
	if got := (&mathText{}).text("没有公式。", true); got != "没有公式。" {
		t.Errorf("text without math %q", got)
	}
}

func TestTypesetStreaming(t *testing.T) {
	for _, full := range []string{
		"令 \\(f'(x)=0\\)，得\n\\[\nx=0,\\quad x=2\n\\]\n所以 $x^2$ 和 $5，又有 $$y$$ 与 **\\(z\\)**，$0.2\\ \\text{mol}$。\n\n- 当 \\(x<0\\) 时，价格 $5 和 $10\n",
		"| 图形 | 面积 |\n|---|---|\n| 圆 | \\(S=\\pi r^2\\) |\n| 方 | $a^2$ |\n",
		"在 Markdown 里写 `\\(x^2\\)` 就行，`$x$ 或 $y$` 也行，然后 \\(z\\)。\n",
		"共 $2 \\times 3$ 个，价格 $5 和 $10，$1 + x^2$，又 $0 < x < \\frac{1}{2}$，costs $5 per month.\n",
		"a \\(x\n y\\) b，$\\alpha\n+\\beta$ c\n",
	} {
		d := parser.New(mathExtensions)
		d.Set(full)
		done := typesetInline(d, false)
		var m mathText
		for k := 1; k <= len(full); k++ {
			if !utf8RuneStart(full, k) {
				continue
			}
			got := m.text(full[:k], true)
			if !strings.HasPrefix(done, got) {
				t.Fatalf("streamed %q shows %q, not the start of %q", full[:k], got, done)
			}
		}
	}
	var m mathText
	if got := m.text("令 \\(f'(x", true); got != "令 " {
		t.Errorf("unclosed math shows as %q while streaming", got)
	}
	if got := m.text("令 \\(f'(x", false); got != "令 \\(f'(x" {
		t.Errorf("unclosed math shows as %q once done", got)
	}
}

func utf8RuneStart(s string, k int) bool {
	return k == len(s) || s[k]&0xC0 != 0x80
}

func TestPlainFormulas(t *testing.T) {
	got := preview(plainFormulas("已知函数\n\\[\nf(x)=x^3\n\\]\n令 \\(f'(x)=0\\)，价格 $5。"))
	if want := "已知函数 f(x) = x³ 令 f′(x) = 0，价格 $5。"; got != want {
		t.Errorf("preview %q, want %q", got, want)
	}
}

var texPieces = []string{
	`\frac{`, `}`, `{`, `\left(`, `\right)`, `\left.`, `\right|`, `^`, `_`, `&`, `\\`, `\begin{cases}`,
	`\end{cases}`, `\begin{pmatrix}`, `\end{pmatrix}`, `\begin{array}{c|l}`, `\end{array}`, `\sqrt[`, `]`,
	`\sqrt`, `x`, `2`, `+`, `-`, `=`, `\alpha`, `\text{`, `$`, `\ce{`, `H2O`, `->[`, `\overbrace{`,
	`\xrightarrow[`, `\color{red}`, `\displaystyle`, `\over`, `\not`, `\hat{`, `\boxed{`, `\sum`, `\int`,
	`\limits`, `'`, ` `, `\`, `\big(`, `\mathbb{`, `\operatorname{`, `\hspace{1em}`, `\tag{1}`, `浓`, `%`,
	`\begin{aligned}`, `\end{aligned}`, `\substack{`, `\binom`, `\middle|`, `\phantom{`, `\cancel{`,
}

func randomTeX(r *rand.Rand) string {
	var b strings.Builder
	for range r.IntN(40) {
		b.WriteString(texPieces[r.IntN(len(texPieces))])
	}
	return b.String()
}

func TestTeXAnything(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	var texs []string
	for range 2000 {
		texs = append(texs, randomTeX(r))
	}
	texs = append(texs, strings.Repeat("{", 5000), strings.Repeat(`\frac{`, 3000), strings.Repeat("x^", 3000), strings.Repeat(`\left(`, 3000))
	for _, tex := range texs {
		done := make(chan struct{})
		go func() {
			defer close(done)
			plainMath(tex)
			inlineMath(tex)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%q takes too long", tex)
		}
	}
	a, _ := testApp(t)
	view := func(c *ui.Context) {
		ui.Column(c).Children(func() {
			for i, tex := range texs {
				ui.Column(c.Key(i)).Children(func() { a.formula(c, tex) })
			}
		})
	}
	tt := ui.NewTester(view, 800, 600)
	tt.Frame()
}

func TestFormulaLayout(t *testing.T) {
	a, _ := testApp(t)
	sizes := map[string]*mbox{}
	view := func(c *ui.Context) {
		for _, tex := range []string{`x`, `\frac{1}{2}`, `\frac{\frac{a}{b}}{c}`, `x^2`, `\sqrt{x}`, `\sum_{i=1}^n i`, `\begin{pmatrix}1&2\\3&4\end{pmatrix}`} {
			sizes[tex] = a.formulas.get(c, tex, 16, "")
		}
	}
	tt := ui.NewTester(view, 400, 300)
	tt.Frame()
	x, half, nested, sq := sizes[`x`], sizes[`\frac{1}{2}`], sizes[`\frac{\frac{a}{b}}{c}`], sizes[`x^2`]
	if x.w <= 0 || x.a <= 0 {
		t.Fatalf("x is %+v", *x)
	}
	if half.a+half.d <= 1.5*(x.a+x.d) {
		t.Errorf("1/2 stands %.1f high, x %.1f: not stacked", half.a+half.d, x.a+x.d)
	}
	if nested.a+nested.d <= half.a+half.d {
		t.Errorf("a fraction over c is no higher than 1/2")
	}
	if sq.a <= x.a || sq.w <= x.w {
		t.Errorf("x² %+v is not above and beyond x %+v", *sq, *x)
	}
	for tex, b := range sizes {
		if b.w <= 0 || b.a+b.d <= 0 || len(b.items) == 0 {
			t.Errorf("%s laid out as %+v", tex, *b)
		}
	}
	if sizes[`\sum_{i=1}^n i`].a < 2*x.a {
		t.Errorf("the sum's limits are not above it in display")
	}
}

func TestReplyFormulas(t *testing.T) {
	a, _ := testApp(t)
	a.addModel(presets[0])
	reply := "令 \\(f'(x)=0\\)，得\n\\[\nx=0,\\quad x=2\n\\]\n所以 $x^2$ 和 $5 都对。"
	a.stream = func(ctx context.Context, cfg llm.Config, req llm.Request, emit func(llm.Event)) error {
		emit(llm.Event{Text: reply})
		return nil
	}
	tt := ui.NewTester(a.view, 1080, 720)
	a.send("求导")
	tt.Frame()
	tt.Frame()
	if a.current.Messages[1].Content != reply {
		t.Fatalf("reply %q", a.current.Messages[1].Content)
	}
	texts := strings.Join(tt.Texts(), "\n")
	for _, want := range []string{"令 f′(x) = 0，得", "所以 x² 和 $5 都对。"} {
		if !strings.Contains(texts, want) {
			t.Errorf("no %q in %q", want, texts)
		}
	}
	for _, raw := range []string{`\(`, `x^2`} {
		if strings.Contains(texts, raw) {
			t.Errorf("raw TeX %q shows in %q", raw, texts)
		}
	}
	r, ok := tt.Find(`x=0,\quad x=2`)
	if !ok || r.W < 60 || r.H < 15 {
		t.Errorf("the display formula is %+v, found %v", r, ok)
	}
	snapshot(t, tt, "formulas")
	tt.SetDark(true)
	tt.Frame()
	snapshot(t, tt, "formulas-dark")
}
