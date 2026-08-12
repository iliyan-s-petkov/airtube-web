package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration wraps time.Duration because yaml.v3 has no special case for it.
// time.Duration is an int64 alias, so a bare time.Duration field silently
// accepts a nanosecond count and rejects "5m" — meaning `poll_interval: 300`
// would decode to 300 nanoseconds and poll upstream in a hot loop. Every
// duration in airbg.yaml is written the way an operator would write it, and
// this type is what makes that legal and makes the alternative an error.
type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf(`must be a quoted duration string such as "5m" or "150s", not %s`, node.Value)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("%q is not a valid duration: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// Std converts back for the call sites, all of which want a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }
