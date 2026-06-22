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
// action.yml files in the wild are well under 1 MiB.
const MaxActionMetaSize = 1 << 20 // 1 MiB

// ParseActionMeta parses the contents of an action.yml or action.yaml file
// into an [ActionMeta]. Pass the raw file bytes as a string; the file name
// itself is not needed. Composite actions emit their nested step `uses:`
// strings in NestedUses; non-composite actions return an empty NestedUses.
//
// Returns an error only on malformed YAML. Unknown `runs.using` values
// (e.g. a future executor type) resolve to [ExecUnknown] rather than
// failing, so callers can handle them gracefully.
func ParseActionMeta(content string) (*ActionMeta, error) {
	if len(content) > MaxActionMetaSize {
		return nil, fmt.Errorf("action metadata too large: %d bytes (max %d)", len(content), MaxActionMetaSize)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return nil, fmt.Errorf("parsing action metadata: %w", err)
	}

	if err := rejectYAMLAnchors(&doc); err != nil {
		return nil, err
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

	if err := doc.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parsing action metadata: %w", err)
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

// rejectYAMLAnchors walks a yaml.Node tree and returns an error if any anchor
// definition or alias reference is found. action.yml does not use YAML anchors,
// so their presence is either a mistake or an attempted exploit.
func rejectYAMLAnchors(n *yaml.Node) error {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.AliasNode {
		return fmt.Errorf("action.yml: YAML anchors and aliases are not supported (line %d)", n.Line)
	}
	if n.Anchor != "" {
		return fmt.Errorf("action.yml: YAML anchors and aliases are not supported (line %d)", n.Line)
	}
	for _, child := range n.Content {
		if err := rejectYAMLAnchors(child); err != nil {
			return err
		}
	}
	return nil
}
