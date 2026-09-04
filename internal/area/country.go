package area

import (
	"fmt"
	"strings"

	"airbg.org/internal/config"
)

// normaliseCountryCode upper-cases and validates a code read from a boundary
// file. Case is normalised rather than rejected because GeoJSON exports differ
// on it and a lower-case "bg" is unambiguous; anything else is not, and is
// rejected rather than guessed at.
func normaliseCountryCode(raw string) (string, error) {
	code := strings.ToUpper(strings.TrimSpace(raw))
	if code == "" {
		return "", fmt.Errorf(
			"a %q boundary needs an iso_a2 property (e.g. \"iso_a2\": \"BG\"); without it the boundary is invisible to the configured country allow list and would filter nothing",
			NationalBoundaryKind)
	}
	if !config.IsCountryCode(code) {
		return "", fmt.Errorf(
			"iso_a2 is %q, want a two-letter ISO 3166-1 alpha-2 code such as \"BG\"", raw)
	}
	return code, nil
}
