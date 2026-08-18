package config

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

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
	"key":          "an environment variable",
	"api_key":      "an environment variable",
	"secret":       "an environment variable",
	"token":        "an environment variable",
}

// "url" is legal under upstream but not under database, so it is checked by
// full path rather than by name.
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

// applyEnv overlays AIRBG_* variables onto the decoded schema. The variable name
// is derived from the same yaml tag the file uses, so the documented rule
// ("AIRBG_" + key path, uppercased, dots to underscores") is true by
// construction rather than by a hand-maintained table that can drift.
//
// series.periods is not overridable: there is no sane environment-variable name
// for "the third list entry's window", and a table belongs in the file.
func applyEnv(v reflect.Value, prefix string) error {
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
		if f.Kind() != reflect.Ptr {
			continue
		}
		elem := f.Type().Elem()
		if elem.Kind() == reflect.Struct && elem != reflect.TypeOf(Duration(0)) {
			// A group. Allocate it if absent so an environment-only override of a
			// leaf inside an omitted group still lands.
			if f.IsNil() {
				f.Set(reflect.New(elem))
			}
			if err := applyEnv(f.Elem(), path); err != nil {
				return err
			}
			continue
		}
		val, ok := os.LookupEnv(envName(path))
		if !ok {
			continue
		}
		if f.IsNil() {
			f.Set(reflect.New(elem))
		}
		if err := assignScalar(f.Elem(), val); err != nil {
			return fmt.Errorf("config: %s=%q: %w", envName(path), val, err)
		}
	}
	return nil
}

func assignScalar(dst reflect.Value, val string) error {
	if dst.Type() == reflect.TypeOf(Duration(0)) {
		d, err := time.ParseDuration(val)
		if err != nil {
			return fmt.Errorf("not a duration such as \"5m\": %w", err)
		}
		dst.SetInt(int64(d))
		return nil
	}
	switch dst.Kind() {
	case reflect.String:
		dst.SetString(val)
	case reflect.Bool:
		b, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("not a boolean: %w", err)
		}
		dst.SetBool(b)
	case reflect.Int, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(val, 10, dst.Type().Bits())
		if err != nil {
			return fmt.Errorf("not an integer: %w", err)
		}
		dst.SetInt(n)
	case reflect.Float64:
		x, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return fmt.Errorf("not a number: %w", err)
		}
		dst.SetFloat(x)
	case reflect.Slice:
		if dst.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("cannot be set from the environment")
		}
		parts := strings.Split(val, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		dst.Set(reflect.ValueOf(out))
	default:
		return fmt.Errorf("cannot be set from the environment")
	}
	return nil
}

const (
	// DatabaseURLEnv is env-only by design: it is a credential, and
	// airbg.yaml is committed. It is the project's only secret — the basemap
	// is self-hosted and has no vendor to authenticate to.
	DatabaseURLEnv = "AIRBG_DATABASE_URL"

	// DatabaseURLFileEnv names a file containing the credential instead of
	// carrying it directly. It exists for ofelia's one-shot `collect` job:
	// job-run starts a fresh container through the Docker API and inherits no
	// env/mounts from compose's env_file, so AIRBG_DATABASE_URL can't reach it
	// the way it reaches the long-lived `app` service. This mirrors the
	// PGPASSFILE pattern the backup job already uses — a root-owned, chmod
	// 600, read-only host bind mount — except pg_dump reads PGPASSFILE itself
	// while this binary has no equivalent client-library support, so it reads
	// the file here instead. AIRBG_DATABASE_URL wins if both are set.
	DatabaseURLFileEnv = "AIRBG_DATABASE_URL_FILE"
)

// databaseURLFromEnv resolves the database credential from the environment:
// the literal value if set, otherwise the contents of the file named by
// DatabaseURLFileEnv, otherwise empty (Validate reports that as a startup
// error). A file that cannot be read, or that is empty, is a hard error
// rather than a silent empty credential — the same reasoning as PGPASSFILE's
// permission requirement in deploy/ofelia.ini: a misconfigured secret must
// fail loudly, not connect as nobody. The file must hold the DSN on a single
// line; only surrounding whitespace is trimmed, so a stray second line ends up
// inside the DSN.
func databaseURLFromEnv() (string, error) {
	if v := os.Getenv(DatabaseURLEnv); v != "" {
		return v, nil
	}
	path := os.Getenv(DatabaseURLFileEnv)
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("config: cannot read %s (naming %s): %w", DatabaseURLFileEnv, path, err)
	}
	url := strings.TrimSpace(string(data))
	// An empty file is its own failure, reported here rather than left to
	// Validate. Falling through with "" would make Validate say the credential
	// "is not set in the environment", which is the opposite of what happened:
	// it was set, it named a file, and the file was blank. An operator who
	// created the file but never wrote the DSN into it would go looking for a
	// missing variable instead of at the file they just made.
	if url == "" {
		return "", fmt.Errorf("config: %s names %s, but that file is empty; it must contain the database DSN on a single line", DatabaseURLFileEnv, path)
	}
	return url, nil
}

// Load reads the configuration named by AIRBG_CONFIG. There is no fallback
// path: guessing one would be a default, and this project keeps none.
func Load() (Config, error) {
	path := os.Getenv(PathEnv)
	if path == "" {
		return Config{}, fmt.Errorf("config: %s is not set; it must name the airbg.yaml to load", PathEnv)
	}
	return LoadFile(path)
}

func LoadFile(path string) (Config, error) {
	r, err := readRaw(path)
	if err != nil {
		return Config{}, err
	}
	cfg := resolve(r)
	dbURL, err := databaseURLFromEnv()
	if err != nil {
		return Config{}, err
	}
	cfg.Database.URL = dbURL
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
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
	// Environment second: the two layers are file then environment, and either
	// may be the sole source of a value.
	if err := applyEnv(reflect.ValueOf(&r).Elem(), ""); err != nil {
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
