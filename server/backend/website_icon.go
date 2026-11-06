package main

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/http"
	"regexp"
	"strings"
)

var svgGeometry = regexp.MustCompile(`^[0-9eE+.,%[:space:]-]+$`)
var svgPath = regexp.MustCompile(`^[MmZzLlHhVvCcSsQqTtAa0-9eE+.,[:space:]-]+$`)
var svgColor = regexp.MustCompile(`^(#[0-9a-fA-F]{3,8}|[A-Za-z]{1,24})$`)

// Accept simple, self-contained geometric icons. SVG can also be an active
// document, so reject unsupported markup instead of forwarding arbitrary XML.
func staticSiteSVG(body []byte) bool {
	if len(body) > 64*1024 {
		return false
	}
	dec := xml.NewDecoder(bytes.NewReader(body))
	const namespace = "http://www.w3.org/2000/svg"
	depth, shapes := 0, 0
	seenRoot := false
	for {
		token, err := dec.Token()
		if err == io.EOF {
			return seenRoot && shapes > 0 && depth == 0
		}
		if err != nil {
			return false
		}
		switch node := token.(type) {
		case xml.StartElement:
			if depth >= 32 || node.Name.Space != namespace {
				return false
			}
			if depth == 0 {
				if seenRoot || node.Name.Local != "svg" {
					return false
				}
				seenRoot = true
			} else {
				switch node.Name.Local {
				case "g":
				case "path", "rect", "circle", "ellipse", "line", "polygon", "polyline":
					shapes++
				default:
					return false
				}
			}
			for _, attr := range node.Attr {
				if attr.Name.Space != "" {
					return false
				}
				switch attr.Name.Local {
				case "xmlns":
					if depth != 0 || attr.Value != namespace {
						return false
					}
				case "d":
					if !svgPath.MatchString(attr.Value) {
						return false
					}
				case "fill", "stroke":
					if !svgColor.MatchString(attr.Value) {
						return false
					}
				case "viewBox", "width", "height", "x", "y", "x1", "x2", "y1", "y2", "cx", "cy", "r", "rx", "ry", "stroke-width", "opacity", "points":
					if !svgGeometry.MatchString(attr.Value) {
						return false
					}
				default:
					return false
				}
			}
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			if len(bytes.TrimSpace(node)) != 0 {
				return false
			}
		default:
			return false
		}
	}
}

func siteIconType(body []byte, declared string) string {
	media := strings.ToLower(strings.TrimSpace(strings.SplitN(declared, ";", 2)[0]))
	if len(body) == 0 || !strings.HasPrefix(media, "image/") {
		return ""
	}
	if detected := http.DetectContentType(body); strings.HasPrefix(detected, "image/") {
		return detected
	}
	if bytes.HasPrefix(body, []byte{0, 0, 1, 0}) {
		return "image/x-icon"
	}
	if media == "image/svg+xml" && staticSiteSVG(body) {
		return media
	}
	return ""
}
