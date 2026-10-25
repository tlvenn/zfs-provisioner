package config

import (
	"fmt"
	"os"
	"regexp"
	"sort"
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

// Config represents the parsed x-zfs configuration: datasets under one or more parents
type Config struct {
	Parents []ParentConfig `json:"parents"`
}

// ParentConfig is a parent dataset and the datasets to provision under it
type ParentConfig struct {
	Parent   string        `json:"parent"`
	Defaults ZFSProperties `json:"defaults,omitempty"`
	Datasets []Dataset     `json:"datasets"`
}

// AllDatasets returns the datasets of every parent, parent by parent
func (c *Config) AllDatasets() []Dataset {
	var all []Dataset
	for _, p := range c.Parents {
		all = append(all, p.Datasets...)
	}
	return all
}

// ParentNames returns the parent datasets, in order
func (c *Config) ParentNames() []string {
	names := make([]string, 0, len(c.Parents))
	for _, p := range c.Parents {
		names = append(names, p.Parent)
	}
	return names
}

// ProvisionRequest is the HTTP API request for provisioning: the x-zfs
// configuration as JSON, with either a parent or parents
type ProvisionRequest struct {
	Parent   string                     `json:"parent,omitempty"`
	Defaults ZFSProperties              `json:"defaults,omitempty"`
	Datasets map[string]interface{}     `json:"datasets,omitempty"`
	Parents  map[string]ProvisionParent `json:"parents,omitempty"`
}

// ProvisionParent is one parent of a multi-parent request. Its defaults
// override the request's.
type ProvisionParent struct {
	Defaults ZFSProperties          `json:"defaults,omitempty"`
	Datasets map[string]interface{} `json:"datasets,omitempty"`
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
	// Container paths ("project/name:path") where the dataset's mount was
	// swapped for an idmapped one
	Idmapped []string `json:"idmapped,omitempty"`
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

	// Parse defaults, shared by every parent
	var defaults ZFSProperties
	if d, ok := xzfsMap["defaults"].(map[string]interface{}); ok {
		parsed, err := parseProperties(d)
		if err != nil {
			return nil, fmt.Errorf("x-zfs.defaults: %w", err)
		}
		defaults = parsed
	}

	_, hasParent := xzfsMap["parent"]
	parents, hasParents := xzfsMap["parents"]
	if hasParent && hasParents {
		return nil, fmt.Errorf("x-zfs.parent and x-zfs.parents are exclusive: list every parent under parents")
	}

	config := &Config{}

	if !hasParents {
		parent, ok := xzfsMap["parent"].(string)
		if !ok {
			return nil, fmt.Errorf("x-zfs.parent (or x-zfs.parents) is required")
		}
		datasets, _ := xzfsMap["datasets"].(map[string]interface{})
		pc, err := buildParent(parent, defaults, datasets)
		if err != nil {
			return nil, err
		}
		config.Parents = []ParentConfig{pc}
		return config, nil
	}

	// Several parents, each with its own defaults and datasets
	if _, ok := xzfsMap["datasets"]; ok {
		return nil, fmt.Errorf("x-zfs.datasets goes under each parent when x-zfs.parents is set")
	}
	parentsMap, ok := parents.(map[string]interface{})
	if !ok || len(parentsMap) == 0 {
		return nil, fmt.Errorf("x-zfs.parents must map each parent dataset to its datasets")
	}
	for _, name := range sortedKeys(parentsMap) {
		entry, ok := parentsMap[name].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("x-zfs.parents.%s must be a map with datasets (and optional defaults)", name)
		}
		for key := range entry {
			if key != "defaults" && key != "datasets" {
				return nil, fmt.Errorf("x-zfs.parents.%s: unknown key %q (expected defaults or datasets)", name, key)
			}
		}

		parentDefaults := defaults
		if d, ok := entry["defaults"].(map[string]interface{}); ok {
			parsed, err := parseProperties(d)
			if err != nil {
				return nil, fmt.Errorf("x-zfs.parents.%s.defaults: %w", name, err)
			}
			parentDefaults = mergeProperties(defaults, parsed)
		}

		datasets, _ := entry["datasets"].(map[string]interface{})
		pc, err := buildParent(name, parentDefaults, datasets)
		if err != nil {
			return nil, err
		}
		config.Parents = append(config.Parents, pc)
	}

	if err := checkDuplicates(config); err != nil {
		return nil, err
	}
	return config, nil
}

// BuildConfig creates a Config from an HTTP API request, reusing the dataset parser.
// Used by the HTTP server to avoid JSON-to-YAML roundtrip.
func BuildConfig(req ProvisionRequest) (*Config, error) {
	if req.Parent != "" && len(req.Parents) > 0 {
		return nil, fmt.Errorf("parent and parents are exclusive: list every parent under parents")
	}

	cfg := &Config{}

	if len(req.Parents) == 0 {
		if req.Parent == "" {
			return nil, fmt.Errorf("parent is required")
		}
		pc, err := buildParent(req.Parent, req.Defaults, req.Datasets)
		if err != nil {
			return nil, err
		}
		cfg.Parents = []ParentConfig{pc}
		return cfg, nil
	}

	if len(req.Datasets) > 0 {
		return nil, fmt.Errorf("datasets go under each parent when parents is set")
	}
	for _, name := range sortedKeys(req.Parents) {
		p := req.Parents[name]
		pc, err := buildParent(name, mergeProperties(req.Defaults, p.Defaults), p.Datasets)
		if err != nil {
			return nil, err
		}
		cfg.Parents = append(cfg.Parents, pc)
	}

	if err := checkDuplicates(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// buildParent parses the datasets under one parent
func buildParent(parent string, defaults ZFSProperties, datasets map[string]interface{}) (ParentConfig, error) {
	if parent == "" {
		return ParentConfig{}, fmt.Errorf("parent is required")
	}

	pc := ParentConfig{
		Parent:   parent,
		Defaults: defaults,
	}

	if len(datasets) > 0 {
		parsed, err := parseDatasets(parent, defaults, datasets)
		if err != nil {
			return ParentConfig{}, err
		}
		pc.Datasets = parsed
	}

	return pc, nil
}

// checkDuplicates refuses a dataset listed under two parents, which nested
// parents (rpool/apps and rpool/apps/app) make possible
func checkDuplicates(cfg *Config) error {
	seen := make(map[string]string)
	for _, p := range cfg.Parents {
		for _, ds := range p.Datasets {
			if prev, ok := seen[ds.Name]; ok {
				return fmt.Errorf("dataset %s is listed under both %s and %s", ds.Name, prev, p.Parent)
			}
			seen[ds.Name] = p.Parent
		}
	}
	return nil
}

// sortedKeys returns a map's keys in order, so parents are provisioned in a
// stable order
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
