package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ZFSProperties represents the configurable ZFS dataset properties
type ZFSProperties struct {
	Quota       string `yaml:"quota,omitempty"`
	Compression string `yaml:"compression,omitempty"`
	Recordsize  string `yaml:"recordsize,omitempty"`
	Reservation string `yaml:"reservation,omitempty"`
	UID         string `yaml:"uid,omitempty"`
	GID         string `yaml:"gid,omitempty"`
}

// Dataset represents a ZFS dataset to be provisioned
type Dataset struct {
	Name       string
	Properties ZFSProperties
}

// Config represents the parsed x-zfs configuration
type Config struct {
	Parent   string
	Defaults ZFSProperties
	Datasets []Dataset
}

// knownProperties is the set of ZFS property keys we recognize
var knownProperties = map[string]bool{
	"quota":       true,
	"compression": true,
	"recordsize":  true,
	"reservation": true,
	"uid":         true,
	"gid":         true,
}

// ParseFile reads a docker-compose file and extracts the x-zfs configuration
func ParseFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return Parse(data)
}

// ParseEnv parses x-zfs configuration from an environment variable value
// The value should be the x-zfs content directly (not wrapped in x-zfs key)
func ParseEnv(value string) (*Config, error) {
	// Wrap the value in x-zfs key to reuse the existing parser
	wrapped := "x-zfs:\n"
	for _, line := range splitLines(value) {
		wrapped += "  " + line + "\n"
	}

	return Parse([]byte(wrapped))
}

// splitLines splits a string into lines
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// Parse parses the x-zfs configuration from docker-compose YAML content
func Parse(data []byte) (*Config, error) {
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	xzfs, ok := raw["x-zfs"]
	if !ok {
		return nil, fmt.Errorf("no x-zfs configuration found")
	}

	xzfsMap, ok := xzfs.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("x-zfs must be a map")
	}

	config := &Config{}

	// Parse parent
	if parent, ok := xzfsMap["parent"].(string); ok {
		config.Parent = parent
	} else {
		return nil, fmt.Errorf("x-zfs.parent is required")
	}

	// Parse defaults
	if defaults, ok := xzfsMap["defaults"].(map[string]interface{}); ok {
		config.Defaults = parseProperties(defaults)
	}

	// Parse datasets
	if datasets, ok := xzfsMap["datasets"].(map[string]interface{}); ok {
		parsed, err := parseDatasets(config.Parent, config.Defaults, datasets)
		if err != nil {
			return nil, err
		}
		config.Datasets = parsed
	}

	return config, nil
}

// parseDatasets recursively parses dataset definitions, supporting both simple and nested forms
func parseDatasets(parentPath string, defaults ZFSProperties, datasets map[string]interface{}) ([]Dataset, error) {
	var result []Dataset

	for name, value := range datasets {
		valueMap, ok := value.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("dataset %q must be a map", name)
		}

		if isSimpleForm(valueMap) {
			// Simple form: properties at this level
			props := mergeProperties(defaults, parseProperties(valueMap))
			result = append(result, Dataset{
				Name:       parentPath + "/" + name,
				Properties: props,
			})
		} else {
			// Nested form: recurse into children
			nested, err := parseDatasets(parentPath+"/"+name, defaults, valueMap)
			if err != nil {
				return nil, err
			}
			result = append(result, nested...)
		}
	}

	return result, nil
}

// isSimpleForm checks if a map contains ZFS properties (simple form) vs nested datasets
// An empty map is treated as simple form (dataset with no custom properties)
func isSimpleForm(m map[string]interface{}) bool {
	// Empty map = simple form with defaults only
	if len(m) == 0 {
		return true
	}

	for key := range m {
		if knownProperties[key] {
			return true
		}
	}
	return false
}

// parseProperties extracts ZFS properties from a map
func parseProperties(m map[string]interface{}) ZFSProperties {
	props := ZFSProperties{}

	if v, ok := m["quota"].(string); ok {
		props.Quota = v
	}
	if v, ok := m["compression"].(string); ok {
		props.Compression = v
	}
	if v, ok := m["recordsize"].(string); ok {
		props.Recordsize = v
	}
	if v, ok := m["reservation"].(string); ok {
		props.Reservation = v
	}
	if v, ok := m["uid"].(string); ok {
		props.UID = v
	}
	if v, ok := m["gid"].(string); ok {
		props.GID = v
	}

	return props
}

// mergeProperties merges defaults with overrides, overrides take precedence
func mergeProperties(defaults, overrides ZFSProperties) ZFSProperties {
	result := defaults

	if overrides.Quota != "" {
		result.Quota = overrides.Quota
	}
	if overrides.Compression != "" {
		result.Compression = overrides.Compression
	}
	if overrides.Recordsize != "" {
		result.Recordsize = overrides.Recordsize
	}
	if overrides.Reservation != "" {
		result.Reservation = overrides.Reservation
	}
	if overrides.UID != "" {
		result.UID = overrides.UID
	}
	if overrides.GID != "" {
		result.GID = overrides.GID
	}

	return result
}
