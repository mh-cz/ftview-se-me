package ftviewconverter

import (
	"strconv"
	"strings"
)

func trimSpace(s string) string {
	return strings.TrimSpace(s)
}

func containsRune(s string, r rune) bool {
	return strings.ContainsRune(s, r)
}

func hasPrefix(s, prefix string) bool {
	return strings.HasPrefix(s, prefix)
}

// formatInvariantDouble renders a float64 the way C#'s
// double.ToString(CultureInfo.InvariantCulture) would for these use
// cases: a plain decimal, no thousands separators, no forced trailing
// zeros, and no exponent notation for the magnitudes this converter
// ever produces (font sizes, pixel/character counts).
func formatInvariantDouble(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// formatInvariantInt renders an int using invariant-culture formatting
// (equivalent to int.ToString(CultureInfo.InvariantCulture): plain
// decimal digits, no grouping).
func formatInvariantInt(v int) string {
	return strconv.Itoa(v)
}
