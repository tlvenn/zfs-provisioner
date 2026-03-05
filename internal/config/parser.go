package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ZFSProperties represents the configurable ZFS dataset properties
type ZFSProperties struct {
	Quota       string `yaml:"quota,omitempty" json:"quota,omitempty"`
	Compression string `yaml:"compression,omitempty" json:"compression,omitempty"`
	Recordsize  string `yaml:"recordsize,omitempty" json:"recordsize,omitempty"`
	Reservation string `yaml:"reservation,omitempty" json:"reservation,omitempty"`
	UID         string `yaml:"uid,omitempty" json:"uid,omitempty"`
	GID         string `yaml:"gid,omitempty" json:"gid,omitempty"`
}

// Dataset represents a ZFS dataset to be provisioned
type Dataset struct {
	Name       string        `json:"name"`
	Properties ZFSProperties `json:"properties"`
}

// Config represents the parsed x-zfs configuration
type Config struct {
	Parent   string        `json:"parent"`
	Defaults ZFSProperties `json:"defaults,omitempty"`
	Datasets []Dataset     `json:"datasets"`
}

// ProvisionRequest is the HTTP API request for provisioning
type ProvisionRequest struct {
	Parent   string                 `json:"parent"`
	Defaults ZFSProperties          `json:"defaults,omitempty"`
	Datasets map[string]interface{} `json:"datasets"`
}

// ProvisionResponse is the HTTP API response for provisioning requests
type ProvisionResponse struct {
	Results []DatasetResult `json:"results"`
}

// DatasetResult represents the outcome of provisioning a single dataset
type DatasetResult struct {
	Name    string   `json:"name"`
	Action  string   `json:"action"` // created, updated, unchanged, error
	Changes []string `json:"changes,omitempty"`
	Error   string   `json:"error,omitempty"`
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

// validDatasetName matches alphanumeric start, then alphanumeric/underscore/dot/hyphen
var validDatasetName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

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
	for _, line := range strings.Split(value, "\n") {
		wrapped += "  " + line + "\n"
	}

	return Parse([]byte(wrapped))
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
		parsed, err := parseProperties(defaults)
		if err != nil {
			return nil, fmt.Errorf("x-zfs.defaults: %w", err)
		}
		config.Defaults = parsed
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

// BuildConfig creates a Config from structured inputs, reusing the dataset parser.
// Used by the HTTP server to avoid JSON-to-YAML roundtrip.
func BuildConfig(parent string, defaults ZFSProperties, datasets map[string]interface{}) (*Config, error) {
	if parent == "" {
		return nil, fmt.Errorf("parent is required")
	}

	cfg := &Config{
		Parent:   parent,
		Defaults: defaults,
	}

	if len(datasets) > 0 {
		parsed, err := parseDatasets(parent, defaults, datasets)
		if err != nil {
			return nil, err
		}
		cfg.Datasets = parsed
	}

	return cfg, nil
}

// parseDatasets recursively parses dataset definitions, supporting both simple and nested forms
func parseDatasets(parentPath string, defaults ZFSProperties, datasets map[string]interface{}) ([]Dataset, error) {
	var result []Dataset

	for name, value := range datasets {
		if err := validateDatasetName(name); err != nil {
			return nil, err
		}

		valueMap, ok := value.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("dataset %q must be a map", name)
		}

		if isSimpleForm(valueMap) {
			// Simple form: properties at this level
			parsed, err := parseProperties(valueMap)
			if err != nil {
				return nil, fmt.Errorf("dataset %q: %w", name, err)
			}
			props := mergeProperties(defaults, parsed)
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

// validateDatasetName checks that a dataset name segment is safe
func validateDatasetName(name string) error {
	if !validDatasetName.MatchString(name) {
		return fmt.Errorf("invalid dataset name %q: must start with alphanumeric and contain only [a-zA-Z0-9_.-]", name)
	}
	return nil
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
func parseProperties(m map[string]interface{}) (ZFSProperties, error) {
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

	uid, err := parseNumericID(m, "uid")
	if err != nil {
		return props, err
	}
	props.UID = uid

	gid, err := parseNumericID(m, "gid")
	if err != nil {
		return props, err
	}
	props.GID = gid

	return props, nil
}

// parseNumericID extracts a numeric ID field (uid/gid) from a map,
// accepting both string and int YAML values, and validates it is numeric.
func parseNumericID(m map[string]interface{}, key string) (string, error) {
	v, ok := m[key]
	if !ok {
		return "", nil
	}

	switch val := v.(type) {
	case string:
		if val == "" {
			return "", nil
		}
		if _, err := strconv.Atoi(val); err != nil {
			return "", fmt.Errorf("invalid %s %q: must be a numeric ID", key, val)
		}
		return val, nil
	case int:
		return strconv.Itoa(val), nil
	case float64:
		return strconv.Itoa(int(val)), nil
	default:
		return "", fmt.Errorf("invalid %s: must be a numeric ID", key)
	}
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
