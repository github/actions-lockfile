package lockfile

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ExecutionType describes how an action runs.
type ExecutionType string

const (
	ExecNode      ExecutionType = "node"
	ExecDocker    ExecutionType = "docker"
	ExecComposite ExecutionType = "composite"
	ExecUnknown   ExecutionType = "unknown"
)

// ActionMeta is the parsed subset of `action.yml` (or `action.yaml`) relevant
// to dependency resolution: the action's name, how it executes, and composite
// action nested `uses:` strings.
type ActionMeta struct {
	Name       string
	Execution  ExecutionType
	NestedUses []string
}

// MaxActionMetaSize is the maximum byte length ParseActionMeta will accept.
// action.yml files in the wild are well under 64 KB; this limit prevents
// memory-exhaustion from oversized or yaml-bomb documents before any YAML
// parsing takes place.
const MaxActionMetaSize = 64 * 1024 // 64 KiB

// MaxNestedUses is the maximum number of composite-action step `uses:` entries
// ParseActionMeta will collect. Real composite actions rarely exceed a dozen
// steps; this cap prevents a crafted action.yml from inflating the NestedUses
// slice into a large allocation.
const MaxNestedUses = 500

// ParseActionMeta parses the contents of an action.yml file into an
// ActionMeta. Composite actions emit their nested step `uses:` strings
// in NestedUses; non-composite actions return an empty NestedUses.
//
// Returns an error only on malformed YAML — unknown `runs.using` values
// resolve to ExecUnknown rather than failing.
func ParseActionMeta(content string) (*ActionMeta, error) {
	if len(content) > MaxActionMetaSize {
		return nil, fmt.Errorf("action.yml too large: %d bytes (max %d)", len(content), MaxActionMetaSize)
	}

	var raw struct {
		Name string `yaml:"name"`
		Runs struct {
			Using string `yaml:"using"`
			Steps []struct {
				Uses string `yaml:"uses"`
			} `yaml:"steps"`
		} `yaml:"runs"`
	}

	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		return nil, fmt.Errorf("parsing action.yml: %w", err)
	}

	meta := &ActionMeta{Name: raw.Name}

	using := strings.ToLower(raw.Runs.Using)
	switch {
	case using == "composite":
		meta.Execution = ExecComposite
		for _, step := range raw.Runs.Steps {
			if step.Uses == "" {
				continue
			}
			if len(meta.NestedUses) >= MaxNestedUses {
				return nil, fmt.Errorf("action.yml has too many composite steps with uses: (max %d)", MaxNestedUses)
			}
			meta.NestedUses = append(meta.NestedUses, step.Uses)
		}
	case using == "docker":
		meta.Execution = ExecDocker
	case strings.HasPrefix(using, "node"):
		meta.Execution = ExecNode
	default:
		meta.Execution = ExecUnknown
	}

	return meta, nil
}
