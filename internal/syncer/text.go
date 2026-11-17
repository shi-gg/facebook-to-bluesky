package syncer

import (
	"strings"

	"github.com/rivo/uniseg"

	"github.com/shi-gg/facebook-to-bluesky/internal/feed"
)

const maxPostGraphemes = 300

func composeText(post feed.Post, includeLink bool) string {
	text := strings.TrimSpace(strings.ToValidUTF8(post.Text, ""))
	if text == "" {
		text = strings.TrimSpace(strings.ToValidUTF8(post.Title, ""))
	}

	var suffix string
	if link := strings.TrimSpace(post.URL); includeLink && link != "" && !strings.Contains(text, link) {
		suffix = "\n\n" + link
	}

	available := maxPostGraphemes - uniseg.GraphemeClusterCount(suffix)
	if available < 20 {
		return truncateGraphemes(text, maxPostGraphemes)
	}

	body := truncateGraphemes(text, available)
	if body == "" {
		return strings.TrimSpace(suffix)
	}

	return body + suffix
}

func truncateGraphemes(text string, max int) string {
	if max <= 0 {
		return ""
	}
	if uniseg.GraphemeClusterCount(text) <= max {
		return text
	}

	graphemes := uniseg.NewGraphemes(text)
	var sb strings.Builder
	count := 0

	for graphemes.Next() && count < max-1 {
		sb.WriteString(graphemes.Str())
		count++
	}

	return strings.TrimRight(sb.String(), " \t\n") + "…"
}
