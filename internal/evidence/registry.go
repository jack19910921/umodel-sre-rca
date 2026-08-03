package evidence

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Registry struct {
	bindings map[string]Binding
}

type registryFile struct {
	Bindings []Binding `yaml:"bindings"`
}

var allowedSelectors = map[string]bool{
	"endpoint_id": true, "probe_task_id": true, "instance_id": true,
	"account_id": true, "region_id": true, "security_group_id": true,
	"resource_id": true, "host": true,
}

var allowedTemplates = map[string]map[string]bool{
	"aliyun.umodel":          {"endpoint_context_v1": true, "endpoint_topology_v1": true},
	"aliyun.synthetic_probe": {"availability_window_v1": true},
	"aliyun.cloudmonitor":    {"ecs_normal_state_window_v1": true},
	"aliyun.sls":             {"nginx_access_by_window_v1": true, "nginx_error_by_window_v1": true},
	"aliyun.actiontrail":     {"security_group_change_window_v1": true},
}

func LoadRegistry(path string) (Registry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Registry{}, err
	}
	var file registryFile
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return Registry{}, fmt.Errorf("parse evidence bindings: %w", err)
	}
	registry := Registry{bindings: make(map[string]Binding, len(file.Bindings))}
	for _, binding := range file.Bindings {
		if err := validateBinding(binding); err != nil {
			return Registry{}, err
		}
		if _, exists := registry.bindings[binding.ID]; exists {
			return Registry{}, fmt.Errorf("duplicate binding id %q", binding.ID)
		}
		registry.bindings[binding.ID] = binding
	}
	if len(registry.bindings) == 0 {
		return Registry{}, fmt.Errorf("at least one binding is required")
	}
	return registry, nil
}

func (r Registry) Binding(id string) (Binding, bool) {
	binding, ok := r.bindings[id]
	return binding, ok
}

func (r Registry) BindingsFor(class EvidenceClass) []Binding {
	bindings := make([]Binding, 0)
	for _, binding := range r.bindings {
		if binding.EvidenceClass == class {
			bindings = append(bindings, binding)
		}
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].ID < bindings[j].ID })
	return bindings
}

func validateBinding(binding Binding) error {
	if binding.QueryURL != "" {
		return fmt.Errorf("binding %q: query_url is not supported", binding.ID)
	}
	if binding.Credentials != nil {
		return fmt.Errorf("binding %q: credentials are not supported", binding.ID)
	}
	if binding.ShellCommand != "" {
		return fmt.Errorf("binding %q: shell_command is not supported", binding.ID)
	}
	if binding.ID == "" || binding.Target == "" || binding.Provider == "" || binding.QueryTemplate == "" {
		return fmt.Errorf("binding id, target, provider, and query_template are required")
	}
	if binding.EvidenceClass != ContextClass && binding.EvidenceClass != MetricsClass && binding.EvidenceClass != LogsClass && binding.EvidenceClass != ChangesClass {
		return fmt.Errorf("binding %q: unsupported evidence_class %q", binding.ID, binding.EvidenceClass)
	}
	if !allowedTemplates[binding.Provider][binding.QueryTemplate] {
		return fmt.Errorf("binding %q: provider/template %s/%s is not allowlisted", binding.ID, binding.Provider, binding.QueryTemplate)
	}
	if len(binding.SelectorMapping) == 0 {
		return fmt.Errorf("binding %q: selector_mapping is required", binding.ID)
	}
	for selector, field := range binding.SelectorMapping {
		if !allowedSelectors[selector] || !allowedSelectors[field] {
			return fmt.Errorf("binding %q: selector %q -> %q is not allowlisted", binding.ID, selector, field)
		}
	}
	if strings.ContainsAny(binding.ConsoleLinkTemplate, "\n\r") {
		return fmt.Errorf("binding %q: console_link_template must be one line", binding.ID)
	}
	return nil
}
