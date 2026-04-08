// Package jobmsg builds a minimal AgentJobRequestMessage JSON from a workflow file.
package jobmsg

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/github/actions-lockfile/pkg/lockfile"
	"gopkg.in/yaml.v3"
)

// Build creates a job message JSON from a workflow file.
func Build(wfPath string, launchEndpoint string, token string) ([]byte, error) {
	wf, err := lockfile.Load(wfPath)
	if err != nil {
		return nil, fmt.Errorf("loading workflow: %w", err)
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal(wf.Content, &raw); err != nil {
		return nil, err
	}

	jobs, ok := raw["jobs"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("no jobs found in workflow")
	}

	// Take the first job
	var steps []map[string]interface{}
	for _, jobData := range jobs {
		job, ok := jobData.(map[string]interface{})
		if !ok {
			continue
		}
		rawSteps, ok := job["steps"].([]interface{})
		if !ok {
			continue
		}
		for i, s := range rawSteps {
			stepMap, ok := s.(map[string]interface{})
			if !ok {
				continue
			}
			uses, ok := stepMap["uses"].(string)
			if !ok {
				// run: step, skip
				continue
			}
			step := buildStep(uses, i)
			if step != nil {
				steps = append(steps, step)
			}
		}
		break
	}

	msg := map[string]interface{}{
		"messageType":    "PipelineAgentJobRequest",
		"plan":           map[string]interface{}{
			"scopeIdentifier": "00000000-0000-0000-0000-000000000000",
			"planType":        "Build",
			"planId":          "00000000-0000-0000-0000-000000000001",
		},
		"timeline": map[string]interface{}{
			"id": "00000000-0000-0000-0000-000000000002",
		},
		"jobId":          "00000000-0000-0000-0000-000000000003",
		"jobDisplayName": "test-job",
		"requestId":      1,
		"lockedUntil":    time.Now().Add(1 * time.Hour).Format(time.RFC3339),
		"resources": map[string]interface{}{
			"endpoints": []map[string]interface{}{
				{
					"name": "SystemVssConnection",
					"url":  launchEndpoint,
					"authorization": map[string]interface{}{
						"scheme": "OAuth",
						"parameters": map[string]string{
							"AccessToken": token,
						},
					},
					"data": map[string]string{},
				},
			},
		},
		"variables": map[string]interface{}{
			"system.github.launch_endpoint": map[string]string{"value": launchEndpoint},
			"system.github.job":             map[string]string{"value": "test-job"},
			"system.github.workspace":       map[string]string{"value": "/tmp/actions-workspace"},
			"system.github.token":           map[string]string{"value": token, "isSecret": "true"},
			"DistributedTask.NewActionMetadata": map[string]string{"value": "true"},
		},
		"steps": steps,
	}

	return json.MarshalIndent(msg, "", "  ")
}

func buildStep(uses string, index int) map[string]interface{} {
	uses = strings.TrimSpace(uses)

	if strings.HasPrefix(uses, "./") {
		return map[string]interface{}{
			"type": "Action",
			"reference": map[string]interface{}{
				"type":           "repository",
				"repositoryType": "self",
				"path":           strings.TrimPrefix(uses, "./"),
			},
			"id":          fmt.Sprintf("00000000-0000-0000-0000-0000000000%02d", index+10),
			"displayName": uses,
		}
	}

	if strings.HasPrefix(uses, "docker://") {
		return map[string]interface{}{
			"type": "Action",
			"reference": map[string]interface{}{
				"type":  "containerRegistry",
				"image": strings.TrimPrefix(uses, "docker://"),
			},
			"id":          fmt.Sprintf("00000000-0000-0000-0000-0000000000%02d", index+10),
			"displayName": uses,
		}
	}

	atParts := strings.SplitN(uses, "@", 2)
	if len(atParts) != 2 {
		return nil
	}
	ref := atParts[1]
	segments := strings.SplitN(atParts[0], "/", 3)
	if len(segments) < 2 {
		return nil
	}

	name := segments[0] + "/" + segments[1]
	path := ""
	if len(segments) == 3 {
		path = segments[2]
	}

	return map[string]interface{}{
		"type": "Action",
		"reference": map[string]interface{}{
			"type": "repository",
			"name": name,
			"ref":  ref,
			"path": path,
		},
		"id":          fmt.Sprintf("00000000-0000-0000-0000-0000000000%02d", index+10),
		"displayName": uses,
	}
}
