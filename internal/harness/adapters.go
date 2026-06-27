package harness

import (
	"fmt"

	"github.com/lucinate-ai/outfit/internal/catalog"
	"github.com/lucinate-ai/outfit/internal/contextsize"
	"github.com/lucinate-ai/outfit/internal/opencode"
	"github.com/lucinate-ai/outfit/internal/outfit"
	"github.com/lucinate-ai/outfit/internal/pi"
)

// modelKey returns the model identifier a harness keys a selection by: the
// friendly ALIAS when given, otherwise the provider-native MODEL. For a single-
// model llama.cpp server the key is only a label, so an ALIAS keeps it readable;
// for an API provider, leaving ALIAS unset keeps the real model id.
func modelKey(sel outfit.Selection) string {
	if sel.Alias != "" {
		return sel.Alias
	}
	return sel.Model
}

// opencodeHarness configures opencode via ~/.config/opencode/opencode.json.
type opencodeHarness struct{}

func (opencodeHarness) Name() string { return "opencode" }

func (opencodeHarness) Command() string { return "opencode" }

func (opencodeHarness) ConfigPath() (string, error) { return opencode.ResolveConfigFile() }

func (opencodeHarness) Apply(p *catalog.Provider, sel outfit.Selection, contextWindow, outputTokens int) (Summary, error) {
	block, defaultModel, err := catalog.BuildProviderBlock(sel.Provider, p, sel.Family, modelKey(sel), sel.BaseURL, opencode.ResolveEnv)
	if err != nil {
		return Summary{}, err
	}
	if contextWindow > 0 {
		if models, ok := block["models"].(map[string]any); ok {
			contextsize.Apply(models, contextWindow, outputTokens)
		}
	}

	configFile, err := opencode.ResolveConfigFile()
	if err != nil {
		return Summary{}, err
	}
	if err := opencode.WriteConfig(configFile, sel.Provider, block, defaultModel); err != nil {
		return Summary{}, err
	}

	var notes []string
	if opts, ok := block["options"].(map[string]any); ok {
		if _, ok := opts["apiKey"]; ok {
			notes = append(notes, fmt.Sprintf("API key injected from %s.", p.APIKeyEnv))
		}
		if b, ok := opts["baseURL"]; ok {
			notes = append(notes, fmt.Sprintf("Base URL: %v", b))
		}
	}
	notes = append(notes, "Run 'opencode' from any directory to use the configuration.")
	return Summary{ConfigPath: configFile, DefaultModel: defaultModel, Notes: notes}, nil
}

func (opencodeHarness) Remove(providerID string, modelKeys []string) (int, error) {
	configFile, err := opencode.ResolveConfigFile()
	if err != nil {
		return 0, err
	}
	return opencode.RemoveConfig(configFile, providerID, modelKeys)
}

func (opencodeHarness) State() (map[string]ProviderState, string, error) {
	configFile, err := opencode.ResolveConfigFile()
	if err != nil {
		return nil, "", err
	}
	states, defaultModel, err := opencode.LoadConfigState(configFile)
	if err != nil {
		return nil, "", err
	}
	out := make(map[string]ProviderState, len(states))
	for id, st := range states {
		out[id] = ProviderState{ModelKeys: st.ModelKeys, BaseURL: st.BaseURL, Contexts: st.Contexts, Outputs: st.Outputs}
	}
	return out, defaultModel, nil
}

// piHarness configures the Pi coding agent via ~/.pi/agent/models.json.
type piHarness struct{}

func (piHarness) Name() string { return "pi" }

func (piHarness) Command() string { return "pi" }

func (piHarness) ConfigPath() (string, error) { return pi.ConfigPath() }

func (piHarness) Apply(p *catalog.Provider, sel outfit.Selection, contextWindow, outputTokens int) (Summary, error) {
	prov, defaultModel, err := catalog.BuildPiProvider(sel.Provider, p, sel.Family, modelKey(sel), sel.BaseURL, opencode.ResolveEnv)
	if err != nil {
		return Summary{}, err
	}
	if err := pi.Write(sel.Provider, prov, contextWindow, outputTokens); err != nil {
		return Summary{}, err
	}
	configFile, err := pi.ConfigPath()
	if err != nil {
		return Summary{}, err
	}

	var notes []string
	if prov.APIKey != "" {
		notes = append(notes, fmt.Sprintf("API key referenced as %s (set it in your environment or Pi auth.json).", prov.APIKey))
	}
	if prov.BaseURL != "" {
		notes = append(notes, fmt.Sprintf("Base URL: %s", prov.BaseURL))
	}
	if defaultModel != "" {
		notes = append(notes, fmt.Sprintf("Pi has no default-model setting; select %q in pi with /model.", defaultModel))
	}
	notes = append(notes, "Run 'pi' to use the configuration.")
	// Pi has no persisted default model, so leave Summary.DefaultModel empty and
	// convey the chosen model through the note above.
	return Summary{ConfigPath: configFile, Notes: notes}, nil
}

func (piHarness) Remove(providerID string, modelKeys []string) (int, error) {
	return pi.Remove(providerID, modelKeys)
}

func (piHarness) State() (map[string]ProviderState, string, error) {
	states, err := pi.State()
	if err != nil {
		return nil, "", err
	}
	out := make(map[string]ProviderState, len(states))
	for id, st := range states {
		out[id] = ProviderState{ModelKeys: st.ModelKeys, BaseURL: st.BaseURL, Contexts: st.Contexts, Outputs: st.Outputs}
	}
	return out, "", nil
}
