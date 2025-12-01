package main

import (
	"regexp"
	"slices"
	"strings"

	"github.com/diamondburned/gotk4/gir/girgen/strcases"
)

func init() {
	strcases.AddPascalSpecials([]string{
		"Rssi",
		"Led",
	})
}

var knownBadSuffixes = map[string]string{
	"Devices":   "Device",
	"Vectors":   "Vector",
	"Scalars":   "Scalar",
	"Rotations": "Rotation",
	// at least for v3, cmds are actually used in server-retured info, while
	// non-cmd forms are used in actual commands sent to servers. use this to
	// flip it around.
	"LinearCmd": "LinearCmdInfo",
	"RotateCmd": "RotateCmdInfo",
	"ScalarCmd": "ScalarCmdInfo",
}

// endsWithBadSuffix checks if the given name ends with a known bad suffix, and
// if so, returns the corrected name and true. Otherwise, it returns false.
func endsWithBadSuffix(name string) (string, bool) {
	for plural, singular := range knownBadSuffixes {
		if strings.HasSuffix(name, plural) {
			return strings.TrimSuffix(name, plural) + singular, true
		}
	}
	return "", false
}

func formatIdentifier(name string) string {
	return strcases.PascalToGo(name)
}

var reWords = regexp.MustCompile(`([A-Z][^A-Z\s]*)`)

// splitGoNameParts splits a Go-cased name into its component words.
func splitGoNameParts(name string) []string {
	return reWords.FindAllString(name, -1)
}

// wordMap maps known abbreviations to their full forms for comparison.
var wordMap = map[string]string{
	"Cmd": "Command",
}

// sameWord checks if two words are the same, considering known abbreviations.
func sameWord(a, b string) bool {
	return a == b || wordMap[a] == b || wordMap[b] == a
}

// concatStringsNoOverlap joins two Go-cased strings, detecting any overlapping
// word parts to avoid duplication.
func concatStringsNoOverlap(a, b string) string {
	aParts := splitGoNameParts(a)
	bParts := splitGoNameParts(b)

	parts := slices.Concat(aParts, bParts)

	// Remove XYX stuttering.
	for i := 2; i < len(parts); i++ {
		w1 := parts[i-2]
		w2 := parts[i]
		if sameWord(w1, w2) {
			parts = slices.Delete(parts, i-1, i)
		}
	}

	// Remove XX overlap.
	for i := 1; i < len(parts); i++ {
		if sameWord(parts[i-1], parts[i]) {
			parts = slices.Delete(parts, i, i+1)
		}
	}

	return strings.Join(parts, "")
}
