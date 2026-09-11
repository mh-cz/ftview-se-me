// Package ftviewconverter converts FactoryTalk View graphic display (.gfx)
// XML between the SE (Studio for Site Edition) and ME (Machine Edition)
// schema dialects.
//
// This is a Go port of the original C# FTViewConverter. The tiny in-memory
// DOM, lookup tables, and per-element conversion rules are carried over
// as-is; see xnode.go, tables.go, and msi.go for the supporting pieces.
package ftviewconverter

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// Direction selects which way a conversion runs.
type Direction int

const (
	SeToMe Direction = iota
	MeToSe
)

// Edition identifies which FactoryTalk View dialect a document appears to
// be written in.
type Edition int

const (
	EditionUnknown Edition = iota
	EditionSe
	EditionMe
)

// Converter converts FactoryTalk View .gfx XML between the SE and ME
// dialects. It is not safe for concurrent use by multiple goroutines
// (Warnings is reset and appended to on every Convert call), matching the
// original C# type's single-threaded usage pattern; construct one per
// conversion (or synchronize externally) if you need concurrency.
type Converter struct {
	// Warnings accumulates human-readable (Czech-language, matching the
	// original tool's locale) notes about lossy or manual-follow-up-needed
	// conversions from the most recent Convert call.
	Warnings []string
}

// New returns a ready-to-use Converter.
func New() *Converter {
	return &Converter{Warnings: []string{}}
}

// ConvertSeToMe converts SE-dialect XML to ME-dialect XML.
func (c *Converter) ConvertSeToMe(xmlText string) string {
	return c.convert(xmlText, SeToMe)
}

// ConvertMeToSe converts ME-dialect XML to SE-dialect XML.
func (c *Converter) ConvertMeToSe(xmlText string) string {
	return c.convert(xmlText, MeToSe)
}

// DetectEdition inspects xmlText and reports whether it looks like an SE
// or ME document, or Unknown if neither the schema location nor any
// edition-distinguishing attribute could be found.
func (c *Converter) DetectEdition(xmlText string) Edition {
	var schemaLocation string
	haveSchemaLocation := false
	sawMeOnlyAttr := false
	sawSeOnlyAttr := false

	decoder := xml.NewDecoder(strings.NewReader(xmlText))
	decoder.CharsetReader = passthroughCharsetReader

loop:
	for {
		tok, err := decoder.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			// Malformed XML: matches the C# XmlException catch returning
			// Unknown.
			return EditionUnknown
		}

		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}

		switch elementName(start.Name) {
		case "gfx":
			for _, a := range start.Attr {
				name := attrName(a.Name)
				if name == "xsi:noNamespaceSchemaLocation" || name == "noNamespaceSchemaLocation" ||
					strings.HasSuffix(name, ":noNamespaceSchemaLocation") {
					schemaLocation = a.Value
					haveSchemaLocation = true
					break
				}
			}

		case "displaySettings":
			for _, a := range start.Attr {
				name := attrName(a.Name)
				if name == "displayNumber" {
					sawMeOnlyAttr = true
				}
				if name == "titleBarText" || name == "position" {
					sawSeOnlyAttr = true
				}
			}
			break loop // nothing past here can add more signal
		}
	}

	if haveSchemaLocation {
		lower := strings.ToLower(schemaLocation)
		if strings.HasSuffix(lower, "se12.xsd") {
			return EditionSe
		}
		if strings.HasSuffix(lower, "me12.xsd") {
			return EditionMe
		}
	}

	if sawMeOnlyAttr {
		return EditionMe
	}
	if sawSeOnlyAttr {
		return EditionSe
	}

	return EditionUnknown
}

// passthroughCharsetReader lets the decoder accept any declared charset
// without attempting real transcoding, since these files are UTF-8.
func passthroughCharsetReader(charset string, input io.Reader) (io.Reader, error) {
	return input, nil
}

func (c *Converter) convert(xmlText string, direction Direction) string {
	c.Warnings = c.Warnings[:0]

	root, err := parseToTree(xmlText)
	if err != nil {
		c.Warnings = append(c.Warnings, fmt.Sprintf("Chyba XML: %s", err.Error()))
		return ""
	}

	if root == nil {
		c.Warnings = append(c.Warnings, "Chybí kořenový prvek <gfx>.")
		return ""
	}

	if direction == SeToMe {
		c.convertNodeSeToMe(root, nil)
	} else {
		c.convertNodeMeToSe(root, nil)
	}

	return serialize(root)
}
