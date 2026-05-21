package tokfm

import (
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

func resolveReferenceURL(baseURL, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}
	parsedRef, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	if parsedRef.IsAbs() {
		return parsedRef.String(), nil
	}
	parsedBase, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	return parsedBase.ResolveReference(parsedRef).String(), nil
}

func walkHTML(n *html.Node, visit func(*html.Node) bool) bool {
	if n == nil {
		return true
	}
	if !visit(n) {
		return false
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if !walkHTML(child, visit) {
			return false
		}
	}
	return true
}

func htmlAttr(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func htmlClassContains(n *html.Node, className string) bool {
	for _, field := range strings.Fields(htmlAttr(n, "class")) {
		if field == className {
			return true
		}
	}
	return false
}

func firstElement(n *html.Node, match func(*html.Node) bool) *html.Node {
	var found *html.Node
	walkHTML(n, func(node *html.Node) bool {
		if node.Type == html.ElementNode && match(node) {
			found = node
			return false
		}
		return true
	})
	return found
}

func elementsMatching(n *html.Node, match func(*html.Node) bool) []*html.Node {
	var out []*html.Node
	walkHTML(n, func(node *html.Node) bool {
		if node.Type == html.ElementNode && match(node) {
			out = append(out, node)
		}
		return true
	})
	return out
}

func firstTextMatching(n *html.Node, re *regexp.Regexp) string {
	var found string
	walkHTML(n, func(node *html.Node) bool {
		if node.Type != html.TextNode {
			return true
		}
		text := cleanText(node.Data)
		if re.MatchString(text) {
			found = text
			return false
		}
		return true
	})
	return found
}

func textContent(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	walkHTML(n, func(node *html.Node) bool {
		if node.Type == html.TextNode {
			b.WriteString(node.Data)
			b.WriteByte(' ')
		}
		return true
	})
	return b.String()
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}
