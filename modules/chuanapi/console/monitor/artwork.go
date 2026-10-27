package monitor

import (
	"bytes"
	"encoding/xml"
	"io"
	"strings"
)

// Preserve native SVG/CSS animation, but reject executable/foreign documents
// and external links. CSP on the response is an additional network boundary.
func validateArtwork(body []byte, contentType string) error {
	if !strings.HasPrefix(contentType, "image/svg+xml") || sourceNamePattern.Match(body) {
		return errUnavailable
	}
	decoder := xml.NewDecoder(bytes.NewReader(body))
	root, depth := false, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errUnavailable
		}
		switch node := token.(type) {
		case xml.StartElement:
			name := strings.ToLower(node.Name.Local)
			if depth == 0 {
				if root || name != "svg" || node.Name.Space != "http://www.w3.org/2000/svg" {
					return errUnavailable
				}
				root = true
			}
			depth++
			if node.Name.Space != "http://www.w3.org/2000/svg" {
				return errUnavailable
			}
			switch name {
			case "script", "foreignobject", "a", "iframe", "object", "embed", "handler":
				return errUnavailable
			}
			for _, attribute := range node.Attr {
				key := strings.ToLower(attribute.Name.Local)
				value := strings.TrimSpace(attribute.Value)
				if strings.HasPrefix(key, "on") || key == "base" {
					return errUnavailable
				}
				if (key == "href" || key == "src") && !strings.HasPrefix(value, "#") {
					return errUnavailable
				}
				if key == "attributename" && (strings.EqualFold(value, "href") || strings.HasPrefix(strings.ToLower(value), "on")) {
					return errUnavailable
				}
			}
		case xml.EndElement:
			depth--
		case xml.Directive:
			return errUnavailable
		case xml.ProcInst:
			if node.Target != "xml" {
				return errUnavailable
			}
		}
	}
	if !root || depth != 0 {
		return errUnavailable
	}
	return nil
}
