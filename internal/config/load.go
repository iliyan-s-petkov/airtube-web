package config

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// PathEnv names the environment variable holding the config file path. There is
// deliberately no default: guessing a path is a default in disguise, and this
// project keeps no defaults in code.
const PathEnv = "AIRBG_CONFIG"

// secretKeys are key names that must never appear in the committed file, at any
// depth. Rejecting them is not tidiness: an ignored credential in a committed
// file is a credential in git that looks like it is in use.
var secretKeys = map[string]string{
	"database_url": "AIRBG_DATABASE_URL",
	"dsn":          "AIRBG_DATABASE_URL",
	"password":     "AIRBG_DATABASE_URL",
	"basemap_key":  "AIRBG_BASEMAP_KEY",
	"key":          "AIRBG_BASEMAP_KEY",
	"api_key":      "AIRBG_BASEMAP_KEY",
	"secret":       "an environment variable",
	"token":        "an environment variable",
}

// "url" is legal under upstream and basemap but not under database, so it is
// checked by full path rather than by name.
var secretPaths = map[string]string{
	"database.url": "AIRBG_DATABASE_URL",
}

func envName(path string) string {
	return "AIRBG_" + strings.ToUpper(strings.ReplaceAll(path, ".", "_"))
}

func decodeStrict(data []byte, r *raw) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	// Without this, a typo'd key is silently ignored and the value it was meant
	// to set stays absent — which, with no defaults, becomes a missing-key error
	// pointing at the wrong thing.
	dec.KnownFields(true)
	if err := dec.Decode(r); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return nil
}

func rejectSecrets(data []byte) error {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	var found []string
	walkKeys(&root, "", func(path, key string) {
		if env, ok := secretPaths[path]; ok {
			found = append(found, fmt.Sprintf("%s (set %s in the environment instead)", path, env))
			return
		}
		if env, ok := secretKeys[strings.ToLower(key)]; ok {
			found = append(found, fmt.Sprintf("%s (set %s in the environment instead)", path, env))
		}
	})
	if len(found) > 0 {
		sort.Strings(found)
		return fmt.Errorf("config: secrets must never be written to the config file; remove:\n  %s",
			strings.Join(found, "\n  "))
	}
	return nil
}

// walkKeys visits every mapping key in the document with its dotted path.
func walkKeys(n *yaml.Node, prefix string, fn func(path, key string)) {
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			walkKeys(c, prefix, fn)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i].Value
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			fn(path, key)
			walkKeys(n.Content[i+1], path, fn)
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			walkKeys(c, prefix, fn)
		}
	}
}

// missingKeys returns the dotted paths of every unset field, depth first. It is
// the completeness check that replaces defaults: the schema is the list of
// things that must be configured, and anything absent is named here.
func missingKeys(v reflect.Value, prefix string) []string {
	var missing []string
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		f := v.Field(i)
		switch f.Kind() {
		case reflect.Ptr:
			if f.IsNil() {
				// If the pointed-to type is a struct (not a scalar like Duration),
				// recurse into it to report all missing nested fields, not just
				// the group name. This ensures an operator sees every key they
				// must add, not one-at-a-time.
				elemType := f.Type().Elem()
				if elemType.Kind() == reflect.Struct && elemType != reflect.TypeOf(Duration(0)) {
					zeroVal := reflect.Zero(elemType)
					missing = append(missing, missingKeys(zeroVal, path)...)
				} else {
					missing = append(missing, path)
				}
				continue
			}
			if f.Elem().Kind() == reflect.Struct && f.Type().Elem() != reflect.TypeOf(Duration(0)) {
				missing = append(missing, missingKeys(f.Elem(), path)...)
			}
		case reflect.Slice:
			if f.Len() == 0 {
				missing = append(missing, path)
				continue
			}
			for j := 0; j < f.Len(); j++ {
				if f.Index(j).Kind() == reflect.Struct {
					missing = append(missing, missingKeys(f.Index(j), fmt.Sprintf("%s[%d]", path, j))...)
				}
			}
		}
	}
	return missing
}

func readRaw(path string) (*raw, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: cannot read %s: %w", path, err)
	}
	if err := rejectSecrets(data); err != nil {
		return nil, err
	}
	var r raw
	if err := decodeStrict(data, &r); err != nil {
		return nil, err
	}
	// Every missing key at once: an operator fixing a 40-key file one restart at
	// a time is a loader bug.
	if missing := missingKeys(reflect.ValueOf(&r).Elem(), ""); len(missing) > 0 {
		return nil, fmt.Errorf("config: %s is missing %d required keys:\n  %s",
			path, len(missing), strings.Join(missing, "\n  "))
	}
	return &r, nil
}
