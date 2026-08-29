package main

type manifest struct {
	SchemaVersion int    `yaml:"schema_version"`
	ID            string `yaml:"id"`
	Name          string `yaml:"name"`
	Version       string `yaml:"version"`
	Description   string `yaml:"description"`
	TokenHub      struct {
		PluginAPI string `yaml:"plugin_api"`
	} `yaml:"tokenhub"`
	Kinds     []string `yaml:"kinds"`
	Placement []string `yaml:"placement"`
	Entry     struct {
		Backend *struct {
			Protocol string `yaml:"protocol"`
			Command  string `yaml:"command"`
		} `yaml:"backend"`
	} `yaml:"entry"`
	Capabilities struct {
		ProviderTypes         []string         `yaml:"provider_types"`
		ProviderResourceTypes []string         `yaml:"provider_resource_types"`
		Provider              map[string]any   `yaml:"provider"`
		Actions               []manifestAction `yaml:"actions"`
		Hooks                 []manifestHook   `yaml:"hooks"`
		Gateway               []string         `yaml:"gateway"`
	} `yaml:"capabilities"`
	Permissions struct {
		Data struct {
			Read []string `yaml:"read"`
		} `yaml:"data"`
	} `yaml:"permissions"`
	Distribution map[string]any `yaml:"distribution"`
}

type manifestAction struct {
	ID           string            `yaml:"id"`
	Kind         string            `yaml:"kind"`
	Title        string            `yaml:"title"`
	Capability   string            `yaml:"capability"`
	Subject      string            `yaml:"subject"`
	Metadata     map[string]string `yaml:"metadata"`
	InputSchema  map[string]any    `yaml:"input_schema"`
	OutputSchema map[string]any    `yaml:"output_schema"`
}

type manifestHook struct {
	ID            string   `yaml:"id"`
	Stage         string   `yaml:"stage"`
	Priority      int      `yaml:"priority"`
	FailurePolicy string   `yaml:"failure_policy"`
	Reads         []string `yaml:"reads"`
	Writes        []string `yaml:"writes"`
}
