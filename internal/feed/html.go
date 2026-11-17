package feed

import (
	"html"
	"regexp"
	"strings"

	xhtml "golang.org/x/net/html"
)

var (
	horizontalSpace = regexp.MustCompile(`[ \t\f\v]+`)
	manyNewlines    = regexp.MustCompile(`\n{3,}`)
)

func cleanHTML(raw string) (string, []string) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}

	doc, err := xhtml.Parse(strings.NewReader(raw))
	if err != nil {
		return normalizeText(html.UnescapeString(raw)), nil
	}

	var (
		text   strings.Builder
		images []string
	)

	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode {
			switch node.Data {
			case "script", "style", "noscript", "iframe", "svg", "video", "audio":
				return
			case "img":
				if src := attr(node, "src"); src != "" {
					images = append(images, src)
				}
				return
			case "br":
				text.WriteString("\n")
				return
			case "span":
				if strings.Contains(nodeText(node), "Feed generated with") {
					return
				}
			}
		}

		if node.Type == xhtml.TextNode {
			text.WriteString(node.Data)
		}

		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}

		if node.Type == xhtml.ElementNode && isBlockElement(node.Data) {
			text.WriteString("\n")
		}
	}

	walk(doc)

	return normalizeText(text.String()), images
}

func attr(node *xhtml.Node, name string) string {
	for _, attribute := range node.Attr {
		if attribute.Key == name {
			return strings.TrimSpace(attribute.Val)
		}
	}
	return ""
}

func nodeText(node *xhtml.Node) string {
	var sb strings.Builder

	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.TextNode {
			sb.WriteString(n.Data)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}

	walk(node)
	return sb.String()
}

func isBlockElement(name string) bool {
	switch name {
	case "address", "article", "aside", "blockquote", "div", "dl", "dd", "dt",
		"fieldset", "figcaption", "figure", "footer", "form", "h1", "h2", "h3",
		"h4", "h5", "h6", "header", "hr", "li", "main", "nav", "ol", "p", "pre",
		"section", "table", "tr", "ul":
		return true
	default:
		return false
	}
}

func normalizeText(raw string) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	raw = strings.ReplaceAll(raw, "\u00a0", " ")

	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		line = horizontalSpace.ReplaceAllString(line, " ")
		lines[i] = strings.TrimSpace(line)
	}

	text := strings.Join(lines, "\n")
	text = manyNewlines.ReplaceAllString(text, "\n\n")

	return strings.TrimSpace(text)
}
