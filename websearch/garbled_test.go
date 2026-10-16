package websearch

import "testing"

const garbledPage = "\u00bc\u06e3 --\n\n\u01f5 --\n\n[](###)\n\n[\u04bb](javascript:void(0)) [\u04bb](javascript:void(0))\n\n[i\u02b6](interactive.html)\n\n# 603986\n\n* [\u00b6\u032c](./)\n* [\u02fe](javascript:void(0))\n* [\u0276\u043e](./holder.html)\n* [\u04ea](javascript:void(0))\n* [\u0271\u1e79](./equity.html)\n* [\u02b1](./capital.html)\n* [\u04ef\u0524](./worth.html)\n* [\u0179](./news.html)\n* [](javascript:void(0))\n* [\u05b2](javascript:void(0))\n* [](javascript:void(0))\n* [\u05ba](javascript:void(0))\n* [\u02fe](./event.html)\n* [\u04b5\u0531](./field.html)\n\n* [\u04b5\u0524](###)\n* [\u04b5\u0524](###)\n* [\u0431](###)\n\nF10 \u04b2\u05b1\u04aa\u0139\u0723\ub870\U00061c63\u03ff\u04bb\u0176\n\n[](###) [\u04bb](###)\n\n\u0461\u0461\u00e3\u05b1\u04aa\u02be\ub870\u04bb\u01f730%\u0139\u01b1\u03fd\u0436\u0263\n\n[\u04bb](###) [](###)\n\n## \u04b5\u0524\n\n2026-10-106\u06b9 **23** \u04bb\u05f4\u00b52026\u04b5\u0524\u28fb  \n \u05242026\u00ff **14.97** \u052a\u0225\u036c **503.63%** \u05242026\uafbb **105.10** \u052a\u0225\u036c **537.71%**\n\n* [\u04b5\u0524](javascript:void(0))\n\n[](javascript:void(0)) [\u0524\u2fbb](javascript:void(0)) [\u00ff](javascript:void(0)) [\u0524\u00ff](javascript:void(0))\n\n[[\"2019\",\"1.44\",\"6.07\",\"SJ\"],[\"2020\",\"1.36\",\"8.81\",\"SJ\"],[\"2021\",\"3.54\",\"23.37\",\"SJ\"],[\"2022\",\"3.10\",\"20.53\",\"SJ\"],[\"2023\",\"0.24\",\"1.61\",\"SJ\"],[\"2024\",\"1.66\",\"11.03\",\"SJ\"],[\"2025\",\"2.48\",\"16.48\",\"SJ\"],[\"2026\",\"14.97\",\"105.10\",\"YC\"],[\"2027\",\"18.74\",\"131.56\",\"YC\"]]\n\n\u03bb\u052a--\u0524\uac68\u00ff\n\n|  | \u0524 | \u0421\u05b5 | \u05b5 | \u05b5 | \u04b5\u01bd |\n| 2026 | 23 | 5.39 | 14.97 | 22.91 | 2.68 |\n| 2027 | 23 | 7.82 | 18.74 | 33.25 | 3.99 |\n| 2028 | 22 | 9.32 | 20.85 | 37.18 | 5.46 |\n\n\u03bb\u052a--\u0524\uac68\n\n|  | \u0524 | \u0421\u05b5 | \u05b5 | \u05b5 | \u04b5\u01bd |\n| 2026 | 23 | 37.76 | 105.10 | 160.74 | 41.32 |\n| 2027 | 23 | 54.82 | 131.56 | 234.09 | 62.67 |\n| 2028 | 22 | 65.33 | 146.36 | 261.71 | 85.15 |\n\n\u0524\u0778\u0778\u043e\u056a\u00bc\u00e3\ubc7e\u0576\u07b9\u0621\n\n## \u04b5\u0524\n\n\n\n|  | \u043e\u0531 | \u0524\uac68\u00ff\u68e8\u052a | \u0524\uac68\u052a |  |\n| 2026\u0524 | 2027\u0524 | 2028\u0524 | 2026\u0524 | 2027\u0524 | 2028\u0524 |\n| \u05a4\u022f |  | 17.76 | 21.32 | 1"

func TestGarbled(t *testing.T) {
	for _, s := range []string{
		"\u05f4(603986) \u04ef\u0524_F10_\u036c\u02f3\u06b7",
		"\u05f4(603986)\u02fe_\u02f2\u01be_",
		"\u05f4(603986) _F10_\u036c\u02f3\u06b7",
		"\ue8fa  \n    \u02fe2025\uac78\u02b52025\uae6b\u02fe\u02b5\u04ea92.03\u052a\u036c25.12%\u0439\u02fe\u027616.48\u052a\u036c49.47%\u02fe\u022b\u0276\u00ff10\u0277\u05bd7.50\u052a\u02f0  \n    \u01bd\u06f5\u3363",
		garbledPage,
	} {
		if !Garbled(s) {
			t.Errorf("%q is not garbled", s)
		}
	}
	for _, s := range []string{
		"兆易创新(603986) 盈利预测_F10_同花顺金融服务网",
		"Москва — столица России, город федерального значения.",
		"Приве́т, ми́р! Э́то уда́рения в ру́сском тексте.",
		"Η Αθήνα είναι η πρωτεύουσα της Ελλάδας και μία από τις αρχαιότερες πόλεις του κόσμου.",
		"Tiếng Việt là ngôn ngữ chính thức của Việt Nam, đường phố Hà Nội rất đông.",
		"Zażółć gęślą jaźń. Příliš žluťoučký kůň úpěl ďábelské ódy.",
		"Београд је главни град Србије. Beograd je glavni grad Srbije, čiji je simbol Kalemegdan.",
		"القاهرة هي عاصمة جمهورية مصر العربية وأكبر مدنها.",
		"تهران پایتخت ایران است و بزرگ‌ترین شهر کشور به شمار می‌رود.",
		"ירושלים היא בירת ישראל והעיר הגדולה בה.",
		"Երևանը Հայաստանի մայրաքաղաքն է։",
		"妈 mā，麻 má，马 mǎ，骂 mà；注音 ㄇㄚˇ、ㄇㄚˋ。",
		"Moscow (/ˈmɒskəʊ/ MOS-koh) is the capital of Russia. Москва · Μόσχα · מוסקבה · موسكو",
		"Москва · Μόσχα · מוסקבה · موسكو",
		"Ελληνικά",
		"Мʼясо та пʼять обʼєктів: українська мова використовує апостроф у словах мʼята, бʼю, вʼюн.",
		"ʻŌlelo Hawaiʻi is the language of Hawaiʻi; ʻŌiwi and ʻāina are common words.",
		"Пʼять обʼєктів",
		"Oʻzbekiston Respublikasi, Toshkent shahri, oʻzbek tili",
		"/ˈmɒskəʊ/, /ˈmoskva/, [mɐˈskva]",
		"Ελληνικά",
		"Tiếng Việt",
		"© 2026 Café Société — naïve résumé, Größe, Ærø",
		"हिन्दी भारत की राजभाषा है।",
		"",
	} {
		if Garbled(s) {
			t.Errorf("%q is garbled", s)
		}
	}
}
