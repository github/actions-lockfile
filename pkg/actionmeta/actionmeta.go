// Package actionmeta parses action.yml files to discover execution type and nested uses: refs.
// Terminology aligns with the runner's ActionExecutionType enum:
//   - ExecNode      = ActionExecutionType.NodeJS
//   - ExecDocker    = ActionExecutionType.Container
//   - ExecComposite = ActionExecutionType.Composite
package actionmeta

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

// ActionMeta is the parsed subset of action.yml we care about.
type ActionMeta struct {
	Name      string        `yaml:"name"`
	Execution ExecutionType // derived from runs.using
	// NestedUses contains the raw uses: strings from composite steps.
	// Only populated when Execution == ExecComposite.
	NestedUses []string
}

// Parse parses an action.yml content string and extracts execution type + nested uses:.
func Parse(content string) (*ActionMeta, error) {
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

	meta := &ActionMeta{
		Name: raw.Name,
	}

	using := strings.ToLower(raw.Runs.Using)
	switch {
	case using == "composite":
		meta.Execution = ExecComposite
		for _, step := range raw.Runs.Steps {
			if step.Uses != "" {
				meta.NestedUses = append(meta.NestedUses, step.Uses)
			}
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
