package config

// IsCountryCode reports whether s is an ISO 3166-1 alpha-2 code in the exact
// form the area and sensor tables store: two uppercase ASCII letters.
//
// It lives in config, the package that imports nothing else internal, because
// both ends of the country allow list need the same notion of a valid code:
// Validate rejects a malformed list at startup, and area.Import rejects a
// boundary whose iso_a2 would fail the database CHECK. Two definitions would
// drift, and the symptom would be a config that validates and then fails every
// insert. Putting it in area instead would close the cycle config -> area ->
// upstream -> config.
func IsCountryCode(s string) bool {
	if len(s) != 2 {
		return false
	}
	for i := 0; i < 2; i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return true
}
