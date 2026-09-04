package wind

import "airbg.org/internal/config"

// RequestURLForTesting exposes the URL builder to the external test package.
func RequestURLForTesting(cfg config.Wind, points []Point) string {
	return New(cfg).requestURL(points)
}
