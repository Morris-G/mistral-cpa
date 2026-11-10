package main

import (
	"fmt"
	"strings"
)

const (
	defaultBaseURL  = "https://api.mistral.ai/v1"
	pluginConfigKey = "mistral"
)

type settings struct {
	BaseURL           string
	ConfigPath        string
	LogRetention      int
	StreamIdleSeconds int
	Failover          bool
	IgnoreProxy       bool
	CooldownSeconds   int
	Credentials       []credential
	Models            []modelAlias
}

type credential struct {
	ID       string
	Label    string
	APIKey   string
	Disabled bool
	Priority int
}

type modelAlias struct {
	Client    string
	Upstream  string
	Reasoning string
}

func (s settings) upstream(client string) (string, string) {
	for _, item := range s.Models {
		if item.Client == client {
			upstream := item.Upstream
			if upstream == "" {
				upstream = item.Client
			}
			return upstream, item.Reasoning
		}
	}
	return client, ""
}

func (s settings) clientNames() []string {
	names := make([]string, 0, len(s.Models))
	for _, item := range s.Models {
		if item.Client != "" {
			names = append(names, item.Client)
		}
	}
	return names
}

func (s settings) pick() (credential, bool) {
	items := s.candidates()
	if len(items) == 0 {
		return credential{}, false
	}
	return items[0], true
}

func (s settings) configured() bool {
	_, ok := s.pick()
	return ok && len(s.Models) > 0
}

func defaultSettings() settings {
	return settings{
		BaseURL:         defaultBaseURL,
		LogRetention:    200,
		Failover:        true,
		CooldownSeconds: 60,
		Models: []modelAlias{
			{Client: "deepseek-v4.1-flash", Upstream: "deepseek-v4-1-flash-1017"},
			{Client: "glm-5.3", Upstream: "zai-glm-5-3"},
			{Client: "glm-5.3-flash", Upstream: "zai-glm-5-3-flash"},
			{Client: "mistral-vibe-cli-latest", Upstream: "mistral-vibe-cli-latest"},
			{Client: "mistral-vibe-cli-with-tools", Upstream: "mistral-vibe-cli-with-tools"},
			{Client: "mistral-vibe-cli-fast", Upstream: "mistral-vibe-cli-fast"},
		},
	}
}

func parseSettings(configYAML []byte) settings {
	current := defaultSettings()
	text := strings.TrimSpace(string(configYAML))
	if text == "" {
		return current
	}
	parsed := parseSettingsText(text)
	if parsed.BaseURL != "" {
		current.BaseURL = strings.TrimRight(parsed.BaseURL, "/")
	}
	if parsed.LogRetention > 0 {
		current.LogRetention = parsed.LogRetention
	}
	current.StreamIdleSeconds = parsed.StreamIdleSeconds
	current.ConfigPath = parsed.ConfigPath
	if parsed.Failover != nil {
		current.Failover = *parsed.Failover
	}
	if parsed.IgnoreProxy != nil {
		current.IgnoreProxy = *parsed.IgnoreProxy
	}
	if parsed.CooldownSeconds != nil {
		current.CooldownSeconds = clampInt(*parsed.CooldownSeconds, 0, 3600)
	}
	if len(parsed.Models) > 0 {
		current.Models = parsed.Models
	}
	if len(parsed.Credentials) > 0 {
		current.Credentials = parsed.Credentials
	} else if parsed.APIKey != "" {
		current.Credentials = []credential{{ID: "primary", Label: "主凭据", APIKey: parsed.APIKey, Priority: 10}}
	}
	return current
}

type parsedSettings struct {
	BaseURL           string
	ConfigPath        string
	APIKey            string
	LogRetention      int
	StreamIdleSeconds int
	Failover          *bool
	IgnoreProxy       *bool
	CooldownSeconds   *int
	Credentials       []credential
	Models            []modelAlias
}

func parseSettingsText(text string) parsedSettings {
	var out parsedSettings
	section := ""
	var cred credential
	var model modelAlias
	inCred := false
	inModel := false
	flushCred := func() {
		if cred.ID != "" || cred.APIKey != "" {
			if cred.ID == "" {
				cred.ID = "key"
			}
			if cred.Label == "" {
				cred.Label = cred.ID
			}
			out.Credentials = append(out.Credentials, cred)
		}
		cred = credential{}
	}
	flushModel := func() {
		if model.Client != "" {
			if model.Upstream == "" {
				model.Upstream = model.Client
			}
			out.Models = append(out.Models, model)
		}
		model = modelAlias{}
	}
	for _, line := range strings.Split(text, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		// Only column-zero lines are top-level keys. CPA re-serializes the
		// plugin block with yaml.v3 before handing it to plugin.register, so a
		// nested "api-key" under a credential arrives indented; treating it as
		// the legacy top-level api-key would flush the credential before its
		// key is read and silently drop the key.
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent == 0 {
			if inCred {
				flushCred()
				inCred = false
			}
			if inModel && !strings.HasPrefix(trim, "-") && !strings.Contains(trim, "upstream-id") && !strings.Contains(trim, "reasoning") && !strings.HasPrefix(trim, "id:") {
				flushModel()
				inModel = false
			}
		}
		if trim == "credentials:" {
			section = "credentials"
			continue
		}
		if trim == "models:" {
			if inModel {
				flushModel()
			}
			section = "models"
			inModel = false
			continue
		}
		name, value, ok := splitYAML(trim)
		if !ok && strings.HasPrefix(trim, "-") {
			item := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trim, "-")), `"'`)
			if section == "models" && !strings.Contains(item, ":") {
				if alias, good := parseModelAlias(item); good {
					out.Models = append(out.Models, alias)
				}
			}
			if section == "credentials" {
				flushCred()
				inCred = true
			}
			if section == "models" && strings.Contains(item, "id:") {
				flushModel()
				inModel = true
				_, value, _ = splitYAML(strings.TrimPrefix(item, "-"))
				model.Client = unquote(strings.TrimSpace(value))
			}
			continue
		}
		if !ok {
			continue
		}
		switch {
		case section == "credentials" && name == "id":
			if inCred && cred.ID != "" {
				flushCred()
			}
			inCred = true
			cred.ID = value
		case section == "credentials" && name == "label":
			cred.Label = value
		case section == "credentials" && (name == "api-key" || name == "api_key"):
			cred.APIKey = value
		case section == "credentials" && name == "disabled":
			cred.Disabled = value == "true"
		case section == "credentials" && name == "priority":
			fmt.Sscanf(value, "%d", &cred.Priority)
		case section == "models" && name == "id":
			if inModel && model.Client != "" {
				flushModel()
			}
			inModel = true
			model.Client = value
		case section == "models" && (name == "upstream-id" || name == "upstream_id"):
			model.Upstream = value
		case section == "models" && name == "reasoning":
			model.Reasoning = value
		case name == "config-path" || name == "config_path":
			out.ConfigPath = strings.TrimSpace(value)
		case name == "base-url" || name == "base_url":
			out.BaseURL = value
		case name == "api-key" || name == "api_key":
			out.APIKey = value
		case name == "log-retention":
			fmt.Sscanf(value, "%d", &out.LogRetention)
		case name == "stream-idle-seconds":
			fmt.Sscanf(value, "%d", &out.StreamIdleSeconds)
		case name == "failover":
			enabled := value == "true" || value == "1" || value == "yes" || value == "on"
			out.Failover = &enabled
		case name == "ignore-proxy" || name == "ignore_proxy":
			enabled := value == "true" || value == "1" || value == "yes" || value == "on"
			out.IgnoreProxy = &enabled
		case name == "cooldown-seconds":
			var seconds int
			fmt.Sscanf(value, "%d", &seconds)
			out.CooldownSeconds = &seconds
		}
	}
	if inCred {
		flushCred()
	}
	if inModel {
		flushModel()
	}
	return out
}

func parseModelAlias(raw string) (modelAlias, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, " \t") {
		return modelAlias{}, false
	}
	client, upstream, ok := strings.Cut(raw, "=")
	client = strings.TrimSpace(client)
	upstream = strings.TrimSpace(upstream)
	if client == "" {
		return modelAlias{}, false
	}
	if !ok || upstream == "" {
		upstream = client
	}
	return modelAlias{Client: client, Upstream: upstream}, true
}

func splitYAML(line string) (string, string, bool) {
	name, value, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(name, "-")), unquote(strings.TrimSpace(value)), true
}

func unquote(value string) string {
	if len(value) >= 2 {
		if value[0] == '"' && value[len(value)-1] == '"' {
			return unescapeDoubleQuoted(value[1 : len(value)-1])
		}
		if value[0] == '\'' && value[len(value)-1] == '\'' {
			return value[1 : len(value)-1]
		}
	}
	return value
}

// unescapeDoubleQuoted reverses quoteYAML: a backslash followed by a
// backslash or a double quote stands for that character.
func unescapeDoubleQuoted(value string) string {
	if !strings.Contains(value, `\`) {
		return value
	}
	var buf strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == '\\' && i+1 < len(value) && (value[i+1] == '\\' || value[i+1] == '"') {
			i++
		}
		buf.WriteByte(value[i])
	}
	return buf.String()
}

// quoteYAML writes a YAML double-quoted scalar. Backslashes must be escaped,
// otherwise a Windows path such as C:\Users would be read as a \U escape and
// break CPA's whole config file.
func quoteYAML(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func renderPluginBlock(s settings) string {
	var buf strings.Builder
	fmt.Fprintf(&buf, "    %s:\n", pluginConfigKey)
	buf.WriteString("      enabled: true\n")
	buf.WriteString("      priority: 100\n")
	if s.ConfigPath != "" {
		fmt.Fprintf(&buf, "      config-path: %s\n", quoteYAML(s.ConfigPath))
	}
	fmt.Fprintf(&buf, "      base-url: %s\n", quoteYAML(s.BaseURL))
	fmt.Fprintf(&buf, "      log-retention: %d\n", s.LogRetention)
	fmt.Fprintf(&buf, "      stream-idle-seconds: %d\n", s.StreamIdleSeconds)
	fmt.Fprintf(&buf, "      failover: %t\n", s.Failover)
	fmt.Fprintf(&buf, "      ignore-proxy: %t\n", s.IgnoreProxy)
	fmt.Fprintf(&buf, "      cooldown-seconds: %d\n", s.CooldownSeconds)
	buf.WriteString("      credentials:\n")
	for _, item := range s.Credentials {
		fmt.Fprintf(&buf, "        - id: %s\n", quoteYAML(item.ID))
		fmt.Fprintf(&buf, "          label: %s\n", quoteYAML(item.Label))
		fmt.Fprintf(&buf, "          api-key: %s\n", quoteYAML(item.APIKey))
		fmt.Fprintf(&buf, "          disabled: %t\n", item.Disabled)
		fmt.Fprintf(&buf, "          priority: %d\n", item.Priority)
	}
	buf.WriteString("      models:\n")
	for _, item := range s.Models {
		fmt.Fprintf(&buf, "        - id: %s\n", quoteYAML(item.Client))
		fmt.Fprintf(&buf, "          upstream-id: %s\n", quoteYAML(item.Upstream))
		fmt.Fprintf(&buf, "          reasoning: %s\n", quoteYAML(item.Reasoning))
	}
	return buf.String()
}

// yamlLine is the little we need to know about one line of CPA's config to
// walk the plugins → configs → mistral path.
type yamlLine struct {
	indent  int
	key     string
	value   string
	content bool // false for blank and comment-only lines
}

func scanYAMLLine(line string) yamlLine {
	trim := strings.TrimSpace(line)
	if trim == "" || strings.HasPrefix(trim, "#") {
		return yamlLine{}
	}
	out := yamlLine{indent: len(line) - len(strings.TrimLeft(line, " ")), content: true}
	idx := strings.Index(trim, ":")
	if idx <= 0 || strings.HasPrefix(trim, "- ") || (idx+1 < len(trim) && trim[idx+1] != ' ' && trim[idx+1] != '\t') {
		return out
	}
	out.key = unquote(strings.TrimSpace(trim[:idx]))
	rest := trim[idx+1:]
	if i := strings.Index(rest, " #"); i >= 0 {
		rest = rest[:i]
	}
	out.value = strings.TrimSpace(rest)
	return out
}

// replacePluginBlock writes block (rendered by renderPluginBlock at a 4-space
// base with 2-space steps) into config as plugins.configs.mistral. It follows
// the file's own indentation width, keeps CRLF line endings, and creates any
// of plugins / configs / mistral that are missing.
func replacePluginBlock(config, block string) (string, error) {
	crlf := strings.Contains(config, "\r\n")
	text := strings.ReplaceAll(config, "\r\n", "\n")
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if text == "" {
		lines = nil
	}
	scanned := make([]yamlLine, len(lines))
	for i, line := range lines {
		scanned[i] = scanYAMLLine(line)
	}

	// regionEnd is the first line after start that is not nested under it.
	regionEnd := func(start int) int {
		for j := start + 1; j < len(lines); j++ {
			if scanned[j].content && scanned[j].indent <= scanned[start].indent {
				return j
			}
		}
		return len(lines)
	}
	// lastContent is the last real line of a region, so trailing blank lines
	// and comments stay where they are.
	lastContent := func(start, end int) int {
		last := start
		for j := start + 1; j < end; j++ {
			if scanned[j].content {
				last = j
			}
		}
		return last
	}
	// childIndent is the indent of the first nested line, or -1.
	childIndent := func(start, end int) int {
		for j := start + 1; j < end; j++ {
			if scanned[j].content {
				return scanned[j].indent
			}
		}
		return -1
	}
	findKey := func(from, to, indent int, name string) int {
		for j := from; j < to; j++ {
			if scanned[j].content && scanned[j].indent == indent && scanned[j].key == name {
				return j
			}
		}
		return -1
	}
	// openMapping turns "key: {}" into "key:" so children can be added.
	openMapping := func(i int) error {
		switch scanned[i].value {
		case "":
			return nil
		case "{}", "~", "null":
			lines[i] = strings.Repeat(" ", scanned[i].indent) + scanned[i].key + ":"
			scanned[i].value = ""
			return nil
		}
		return fmt.Errorf("%s in the CPA config is not a block mapping; rewrite it as indented YAML", scanned[i].key)
	}
	splice := func(from, to int, insert []string) []string {
		out := make([]string, 0, len(lines)+len(insert))
		out = append(out, lines[:from]...)
		out = append(out, insert...)
		return append(out, lines[to:]...)
	}

	var result []string
	unit := 2
	plugins := findKey(0, len(lines), 0, "plugins")
	if plugins < 0 {
		insert := append([]string{"plugins:", "  configs:"}, reindentBlock(block, 4, 2)...)
		result = append(append([]string{}, lines...), insert...)
	} else {
		if err := openMapping(plugins); err != nil {
			return "", err
		}
		pluginsEnd := regionEnd(plugins)
		if indent := childIndent(plugins, pluginsEnd); indent > 0 {
			unit = indent
		}
		configs := findKey(plugins+1, pluginsEnd, unit, "configs")
		if configs < 0 {
			insert := append([]string{strings.Repeat(" ", unit) + "configs:"}, reindentBlock(block, 2*unit, unit)...)
			at := lastContent(plugins, pluginsEnd) + 1
			result = splice(at, at, insert)
		} else {
			if err := openMapping(configs); err != nil {
				return "", err
			}
			configsEnd := regionEnd(configs)
			entryIndent := childIndent(configs, configsEnd)
			if entryIndent < 0 {
				entryIndent = scanned[configs].indent + unit
			}
			mistral := findKey(configs+1, configsEnd, entryIndent, pluginConfigKey)
			if mistral < 0 {
				at := lastContent(configs, configsEnd) + 1
				result = splice(at, at, reindentBlock(block, entryIndent, unit))
			} else {
				end := lastContent(mistral, regionEnd(mistral)) + 1
				result = splice(mistral, end, reindentBlock(block, entryIndent, unit))
			}
		}
	}
	out := strings.Join(result, "\n") + "\n"
	if crlf {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	return out, nil
}

// reindentBlock moves a block rendered at a 4-space base with 2-space steps to
// the given base indent and step width.
// Keys that continue a "- " list item stay aligned with the item's first key,
// whatever the step width.
func reindentBlock(block string, base, unit int) []string {
	var out []string
	itemOld, itemNew := -1, -1
	for _, line := range strings.Split(strings.TrimRight(block, "\n"), "\n") {
		body := strings.TrimLeft(line, " ")
		if body == "" {
			continue
		}
		n := len(line) - len(body)
		var indent int
		if itemOld >= 0 && n == itemOld+2 && !strings.HasPrefix(body, "- ") {
			indent = itemNew + 2
		} else {
			level := (n - 4) / 2
			if level < 0 {
				level = 0
			}
			indent = base + level*unit
			itemOld, itemNew = -1, -1
			if strings.HasPrefix(body, "- ") {
				itemOld, itemNew = n, indent
			}
		}
		out = append(out, strings.Repeat(" ", indent)+body)
	}
	return out
}

func clampInt(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func maskKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if len(key) <= 4 {
		return "****"
	}
	return "****" + key[len(key)-4:]
}
