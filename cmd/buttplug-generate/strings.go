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

func formatIdentifier(name string) string {
	return strcases.PascalToGo(name)
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
		if w1 == w2 {
			parts = slices.Delete(parts, i-1, i)
		}
	}

	// Remove XX overlap.
	for i := 1; i < len(parts); i++ {
		if parts[i-1] == parts[i] {
			parts = slices.Delete(parts, i, i+1)
		}
	}

	return strings.Join(parts, "")
}

var (
	reWords    = regexp.MustCompile(`([A-Z][^A-Z\s]*)`)
	wordMapper = strings.NewReplacer(
		"Cmd", "Command",
	)
)

func splitGoNameParts(name string) []string {
	words := reWords.FindAllString(name, -1)
	for i, word := range words {
		words[i] = wordMapper.Replace(word)
	}
	return words
}
