package splice

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
)

func TestPipelineRunConfigClassifiesEveryAgentOption(t *testing.T) {
	consumed := pipelineConsumedAgentOptionNames()
	for name := range consumed {
		if reason, ignored := pipelineIgnoredAgentOptionReasons[name]; ignored {
			t.Errorf("agent.Options.%s is both consumed by PipelineConfigFromAgentOptions and listed in pipelineIgnoredAgentOptionReasons (%q). Keep it in exactly one place.", name, reason)
		}
	}

	optsType := reflect.TypeOf(agent.Options{})
	for i := 0; i < optsType.NumField(); i++ {
		field := optsType.Field(i)
		name := field.Name
		_, consumedOK := consumed[name]
		reason, ignoredOK := pipelineIgnoredAgentOptionReasons[name]
		switch {
		case consumedOK && ignoredOK:
			// already reported above
		case consumedOK:
			continue
		case ignoredOK:
			if strings.TrimSpace(reason) == "" {
				t.Errorf("agent.Options.%s is listed as ignored but has an empty reason. Write a non-empty reason in pipelineIgnoredAgentOptionReasons.", name)
			}
		default:
			t.Errorf("unclassified agent.Options.%s. Either copy it in PipelineConfigFromAgentOptions (consumed) or add it to pipelineIgnoredAgentOptionReasons with a non-empty reason. A new field must be classified before CI can pass.", name)
		}
	}

	for name := range pipelineIgnoredAgentOptionReasons {
		if _, ok := optsType.FieldByName(name); !ok {
			t.Errorf("pipelineIgnoredAgentOptionReasons lists %s, but agent.Options has no such field. Remove the stale entry.", name)
		}
	}
	for name := range consumed {
		if _, ok := optsType.FieldByName(name); !ok {
			t.Errorf("PipelineConfigFromAgentOptions copies %s, but agent.Options has no such field. Remove the stale copy.", name)
		}
	}
}

func pipelineConsumedAgentOptionNames() map[string]struct{} {
	src := reflect.TypeOf(agent.Options{})
	dst := reflect.TypeOf(PipelineRunConfig{})
	consumed := make(map[string]struct{})
	for i := 0; i < dst.NumField(); i++ {
		name := dst.Field(i).Name
		if _, ok := src.FieldByName(name); ok {
			consumed[name] = struct{}{}
		}
	}
	return consumed
}
