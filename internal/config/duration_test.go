package config

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestDurationUnmarshalYAML(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want time.Duration
	}{
		{"minutes", `"5m"`, 5 * time.Minute},
		{"seconds", `"150s"`, 150 * time.Second},
		{"hours", `"2h"`, 2 * time.Hour},
		{"compound", `"10m"`, 10 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d Duration
			if err := yaml.Unmarshal([]byte(tt.in), &d); err != nil {
				t.Fatalf("Unmarshal(%s) error = %v, want nil", tt.in, err)
			}
			if d.Std() != tt.want {
				t.Errorf("Unmarshal(%s) = %v, want %v", tt.in, d.Std(), tt.want)
			}
		})
	}
}

// A bare integer is the failure mode this type exists to prevent: yaml.v3 would
// decode 300000000000 into a plain time.Duration field without complaint, and an
// operator writing `poll_interval: 300` would silently get 300 nanoseconds.
func TestDurationRejectsBareNumber(t *testing.T) {
	var d Duration
	err := yaml.Unmarshal([]byte(`300`), &d)
	if err == nil {
		t.Fatalf("Unmarshal(300) error = nil, want an error; got duration %v", d.Std())
	}
}

func TestDurationRejectsGarbage(t *testing.T) {
	var d Duration
	err := yaml.Unmarshal([]byte(`"5 fortnights"`), &d)
	if err == nil {
		t.Fatalf("Unmarshal(\"5 fortnights\") error = nil, want an error")
	}
}
