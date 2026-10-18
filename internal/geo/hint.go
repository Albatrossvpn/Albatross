package geo

import (
	"regexp"
	"strings"
	"unicode"
)

var codeTokenRe = regexp.MustCompile(`(?:^|[^A-Za-z])([A-Z]{2})(?:[^A-Za-z]|$)`)

// Tokens that look like country codes but almost never mean one in names.
var codeStopwords = map[string]bool{
	"VL": true, "VM": true, "SS": true, "TR": false, "WS": true, "XX": true, "NA": true, "OK": true,
	"TG": true, "ID": true, "IP": true, "HY": true, "TU": true, "RE": true, "DO": true, "ME": true,
	"IN": true, "IT": true, "TO": true, "BY": true, "IS": true, "AT": true, "AS": true, "OR": true,
	"NO": true, "AM": true, "PM": true, "GO": true, "HI": true, "SO": true, "UP": true, "MY": true,
}

// Hint guesses a server's exit country from its display name. It trusts,
// in order: flag emoji, full country or city names, then bare ISO codes.
// The result is only a hint for test ordering; the real exit country comes
// from testing through the server.
func Hint(name string) string {
	if name == "" {
		return ""
	}
	if c := flagCode(name); c != "" {
		return c
	}
	// Look up runs of up to three words, longest first, so "united states"
	// wins over "states" and "south korea" over "korea".
	words := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !isLetter(r) && r != '\u200c'
	})
	for size := 3; size >= 1; size-- {
		for i := 0; i+size <= len(words); i++ {
			if code, ok := nameToCode[strings.Join(words[i:i+size], " ")]; ok {
				return code
			}
		}
	}
	for _, m := range codeTokenRe.FindAllStringSubmatch(name, -1) {
		code := m[1]
		if codeStopwords[code] {
			continue
		}
		if _, ok := byCode[code]; ok {
			if code == "UK" {
				return "GB"
			}
			return code
		}
		if code == "UK" {
			return "GB"
		}
	}
	return ""
}

func flagCode(s string) string {
	rs := []rune(s)
	for i := 0; i+1 < len(rs); i++ {
		a, b := rs[i], rs[i+1]
		if a >= 0x1F1E6 && a <= 0x1F1FF && b >= 0x1F1E6 && b <= 0x1F1FF {
			code := string([]rune{'A' + (a - 0x1F1E6), 'A' + (b - 0x1F1E6)})
			if code == "UK" {
				code = "GB"
			}
			return code
		}
	}
	return ""
}

func isLetter(r rune) bool { return unicode.IsLetter(r) }
