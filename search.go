package main

import (
	"strings"
	"unicode/utf8"
)

// found is a chat that a search matched.
type found struct {
	chat *Chat
	// message is the first message where the search matched, -1 when the
	// title holds every word of it.
	message int
	// snippet is the text around the match in message, to show under the
	// title.
	snippet string
}

// searchChats returns the chats, in their order, holding every word of
// query, whatever their case, in their title or the text of their
// messages: the thinking is not searched, as it hides.
func searchChats(chats []*Chat, query string) []found {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return nil
	}
	var out []found
	for _, ch := range chats {
		if f, ok := match(ch, words); ok {
			out = append(out, f)
		}
	}
	return out
}

// match reports whether ch holds every one of words, which are in lower
// case, and where the first not in its title is.
func match(ch *Chat, words []string) (found, bool) {
	f := found{chat: ch, message: -1}
	title := strings.ToLower(ch.Title)
	texts := make([]string, len(ch.Messages)) // in lower case, made as needed
	for _, w := range words {
		if strings.Contains(title, w) {
			continue
		}
		hit := false
		for i, m := range ch.Messages {
			if texts[i] == "" {
				texts[i] = strings.ToLower(m.Content)
			}
			j := strings.Index(texts[i], w)
			if j < 0 {
				continue
			}
			if f.message < 0 {
				// ToLower maps rune to rune: the match starts as many
				// runes into the text.
				at := utf8.RuneCountInString(texts[i][:j])
				f.message, f.snippet = i, snippet(m.Content, at, utf8.RuneCountInString(w))
			}
			hit = true
			break
		}
		if !hit {
			return f, false
		}
	}
	return f, true
}

// snippet returns the text around the n runes of text at rune at, on one
// line, without the marks of bold and code that a reply's Markdown shows
// as such: a few words before them, so that they show near the start, and
// what follows.
func snippet(text string, at, n int) string {
	const before, after = 8, 60
	r := []rune(text)
	start := max(at-before, 0)
	end := min(at+n+after, len(r))
	s := strings.NewReplacer("**", "", "`", "").Replace(string(r[start:end]))
	s = strings.Join(strings.Fields(s), " ")
	if start > 0 {
		s = "…" + s
	}
	return s
}

// results returns the chats matching what is searched for in the sidebar,
// searched again as the search or the chats change.
func (a *app) results() []found {
	q := strings.TrimSpace(a.search)
	if q != a.searched || a.searchStale {
		a.found, a.searched, a.searchStale = searchChats(a.chats, q), q, false
	}
	return a.found
}
