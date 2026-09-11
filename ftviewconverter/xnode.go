package ftviewconverter

import (
	"encoding/xml"
	"io"
	"strconv"
	"strings"
)

// ------------------------------------------------------------------
// Minimal in-memory XML tree. We still build our own tiny DOM (rather
// than using encoding/xml's native tree types directly) because
// FactoryTalk Studio's importer cares about exact attribute order and
// a specific self-closing / CRLF / character-reference serialization
// style -- see Serialize().
// ------------------------------------------------------------------

// XNode is a single element in the tiny DOM.
type XNode struct {
	Tag       string
	Attrs     map[string]string
	AttrOrder []string // preserves attribute order for nicer diffs
	Children  []*XNode
	Text      string // any direct text content (rare in these files)
}

// newXNode creates an XNode with its attribute map initialized.
func newXNode(tag string) *XNode {
	return &XNode{Tag: tag, Attrs: map[string]string{}}
}

// SetAttr sets (or overwrites) an attribute, preserving first-seen order.
func (n *XNode) SetAttr(key, value string) {
	if _, ok := n.Attrs[key]; !ok {
		n.AttrOrder = append(n.AttrOrder, key)
	}
	n.Attrs[key] = value
}

// RemoveAttr removes an attribute if present.
func (n *XNode) RemoveAttr(key string) {
	if _, ok := n.Attrs[key]; ok {
		delete(n.Attrs, key)
		for i, k := range n.AttrOrder {
			if k == key {
				n.AttrOrder = append(n.AttrOrder[:i], n.AttrOrder[i+1:]...)
				break
			}
		}
	}
}

// RenameAttr renames an attribute in place, preserving its value; a no-op
// if oldKey isn't present.
func (n *XNode) RenameAttr(oldKey, newKey string) {
	if v, ok := n.Attrs[oldKey]; ok {
		n.RemoveAttr(oldKey)
		n.SetAttr(newKey, v)
	}
}

// Has reports whether the attribute is present.
func (n *XNode) Has(key string) bool {
	_, ok := n.Attrs[key]
	return ok
}

// GetAttr returns the attribute's value, or defaultValue if absent.
func (n *XNode) GetAttr(key string, defaultValue ...string) string {
	if v, ok := n.Attrs[key]; ok {
		return v
	}
	if len(defaultValue) > 0 {
		return defaultValue[0]
	}
	return ""
}

// ClearAttrs removes all attributes and resets the order slice, mirroring
// the C# `Attrs.Clear(); AttrOrder.Clear();` pattern used throughout the
// converter when a node is rebuilt from scratch.
func (n *XNode) ClearAttrs() {
	n.Attrs = map[string]string{}
	n.AttrOrder = nil
}

// ClearChildren removes all children.
func (n *XNode) ClearChildren() {
	n.Children = nil
}

// findChild returns the first child with the given tag, or nil.
func (n *XNode) findChild(tag string) *XNode {
	for _, c := range n.Children {
		if c.Tag == tag {
			return c
		}
	}
	return nil
}

// parseToTree parses xmlText into the tiny DOM. It deliberately ignores
// CDATA/comments/PI/DOCTYPE, matching the original converter's scope for
// this format. Returns nil (with no error) if the document has no root
// element at all.
func parseToTree(xmlText string) (*XNode, error) {
	decoder := xml.NewDecoder(strings.NewReader(xmlText))
	// FactoryTalk files are UTF-8; be permissive about charset decls we
	// can't resolve rather than failing the whole parse over them.
	decoder.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		return input, nil
	}

	var stack []*XNode
	var root *XNode

	for {
		tok, err := decoder.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			node := newXNode(elementName(t.Name))
			for _, a := range t.Attr {
				node.SetAttr(attrName(a.Name), a.Value)
			}

			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, node)
			} else if root == nil {
				root = node
			}
			stack = append(stack, node)

		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}

		case xml.CharData:
			if len(stack) > 0 {
				text := string(t)
				if strings.TrimSpace(text) != "" {
					top := stack[len(stack)-1]
					top.Text += text
				}
			}

			// xml.Comment, xml.ProcInst, xml.Directive are deliberately
			// ignored, matching the original converter's scope.
		}
	}

	return root, nil
}

// elementName renders an xml.Name back into the raw "prefix:local" form
// (or just "local" if there's no prefix), matching XmlReader.Name's
// behavior in the C# original. encoding/xml resolves namespace URIs into
// Name.Space, but this format doesn't use real namespace-aware attribute
// names anywhere that matters, so we reconstruct the literal token text
// from Space (used here only as a prefix carrier) and Local.
func elementName(n xml.Name) string {
	if n.Space != "" {
		return n.Space + ":" + n.Local
	}
	return n.Local
}

func attrName(n xml.Name) string {
	if n.Space != "" {
		// encoding/xml resolves "xsi" to the full namespace URI; the
		// original C# used XmlReader's raw, un-resolved attribute name
		// (e.g. "xsi:noNamespaceSchemaLocation"). Special-case the one
		// namespace this format actually uses.
		if n.Space == "http://www.w3.org/2001/XMLSchema-instance" {
			return "xsi:" + n.Local
		}
		return n.Space + ":" + n.Local
	}
	return n.Local
}

// serialize renders the tree back to text, matching FactoryTalk Studio's
// expected import format exactly (CRLF line endings, 4-space indents, a
// leading XML declaration, and specific character-reference escaping).
func serialize(root *XNode) string {
	var sb strings.Builder
	sb.Grow(4096)
	sb.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\r\n")
	serializeNode(root, 0, &sb)
	return sb.String()
}

// indentCache caches "    ", "        ", ... indent strings so deep trees
// don't reallocate an indent string per node.
var indentCache = []string{""}

func indent(depth int) string {
	for len(indentCache) <= depth {
		indentCache = append(indentCache, indentCache[len(indentCache)-1]+"    ")
	}
	return indentCache[depth]
}

func serializeNode(node *XNode, depth int, sb *strings.Builder) {
	ind := indent(depth)
	sb.WriteString(ind)
	sb.WriteByte('<')
	sb.WriteString(node.Tag)

	for _, key := range node.AttrOrder {
		sb.WriteByte(' ')
		sb.WriteString(key)
		sb.WriteString("=\"")
		appendEscapedAttr(sb, node.Attrs[key])
		sb.WriteByte('"')
	}

	if len(node.Children) == 0 && len(node.Text) == 0 {
		sb.WriteString("/>\r\n")
		return
	}

	sb.WriteByte('>')
	if len(node.Text) > 0 {
		appendEscapedText(sb, node.Text)
	}
	if len(node.Children) > 0 {
		sb.WriteString("\r\n")
		for _, child := range node.Children {
			serializeNode(child, depth+1, sb)
		}
		sb.WriteString(ind)
	}
	sb.WriteString("</")
	sb.WriteString(node.Tag)
	sb.WriteString(">\r\n")
}

// appendEscapedAttr escapes an attribute value.
//
// Per the XML spec, a conformant parser normalizes literal newline,
// carriage-return, and tab characters found *inside* an attribute value
// down to a single space during attribute-value normalization. SE/ME
// caption text frequently embeds real newlines (line breaks within a
// button/text caption) that decoded from &#xA; on the way in; if they're
// written back out as raw bytes instead of character references,
// FactoryTalk View Studio's importer collapses them and the line break is
// lost. Escaping them as character references preserves them exactly, the
// same way the source file encoded them.
func appendEscapedAttr(sb *strings.Builder, s string) {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\r\n", "&#xA;")
	s = strings.ReplaceAll(s, "\n", "&#xA;")
	s = strings.ReplaceAll(s, "\r", "&#xD;")
	s = strings.ReplaceAll(s, "\t", "&#x9;")
	sb.WriteString(s)
}

func appendEscapedText(sb *strings.Builder, s string) {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	sb.WriteString(s)
}

// parseInvariantDouble parses s as a culture-invariant float, returning
// fallback if it isn't a valid number.
func parseInvariantDouble(s string, fallback float64) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return fallback
	}
	return v
}
