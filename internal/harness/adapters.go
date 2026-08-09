package harness

import (
	"fmt"

	"github.com/lucinate-ai/outfit/internal/catalog"
	"github.com/lucinate-ai/outfit/internal/contextsize"
	"github.com/lucinate-ai/outfit/internal/lucinate"
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

func (opencodeHarness) Apply(p *catalog.Provider, sel outfit.Selection, contextWindow, outputTokens int, resolve func(string) string) (Summary, error) {
	block, defaultModel, err := catalog.BuildProviderBlock(sel.Provider, p, modelKey(sel), sel.BaseURL, resolve)
	if err != nil {
		return Summary{}, err
	}
	// A remote selection renames the provider after its environment and carries a
	// display name to match; opencode's model picker lists providers by that
	// name, so use it in place of the catalogue engine's name to tell the remote
	// provider apart from a local engine of the same kind.
	if sel.DisplayName != "" {
		block["name"] = sel.DisplayName
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
		baseURL, _ := opts["baseURL"].(string)
		if warning := missingKeyWarning(p, baseURL, resolve); warning != "" {
			notes = append(notes, warning)
		} else if _, ok := opts["apiKey"]; ok {
			notes = append(notes, fmt.Sprintf(
				"API key read from %s when opencode runs.", p.APIKeyEnv))
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

func (piHarness) Apply(p *catalog.Provider, sel outfit.Selection, contextWindow, outputTokens int, resolve func(string) string) (Summary, error) {
	prov, defaultModel, err := catalog.BuildPiProvider(sel.Provider, p, modelKey(sel), sel.BaseURL, resolve)
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
	if warning := missingKeyWarning(p, prov.BaseURL, resolve); warning != "" {
		notes = append(notes, warning)
	} else if prov.APIKey != "" {
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

// lucinateHarness configures the lucinate chat client via
// ~/.lucinate/connections.json, writing one managed OpenAI-compatible
// connection per provider.
type lucinateHarness struct{}

func (lucinateHarness) Name() string { return "lucinate" }

func (lucinateHarness) Command() string { return "lucinate" }

func (lucinateHarness) ConfigPath() (string, error) { return lucinate.ConfigPath() }

func (lucinateHarness) Apply(p *catalog.Provider, sel outfit.Selection, contextWindow, outputTokens int, resolve func(string) string) (Summary, error) {
	conn, defaultModel, err := catalog.BuildLucinateConnection(sel.Provider, p, modelKey(sel), sel.BaseURL, resolve)
	if err != nil {
		return Summary{}, err
	}
	// lucinate speaks to a concrete OpenAI-compatible endpoint, so a connection
	// with no URL cannot work — fail rather than write a dead entry.
	if conn.BaseURL == "" {
		return Summary{}, fmt.Errorf("provider %q needs a base URL for the lucinate harness; set BASEURL in the Outfit or pass --base-url", sel.Provider)
	}

	// The connection's display name is the provider's, or the selection's display
	// name for a remote endpoint, so it reads distinctly from a local engine of
	// the same kind — mirroring the opencode adapter.
	name := p.Name
	if name == "" {
		name = sel.Provider
	}
	if sel.DisplayName != "" {
		name = sel.DisplayName
	}

	if err := lucinate.Write(sel.Provider, lucinate.Connection{
		URL:          conn.BaseURL,
		DefaultModel: conn.Model,
		Name:         name,
	}); err != nil {
		return Summary{}, err
	}
	configFile, err := lucinate.ConfigPath()
	if err != nil {
		return Summary{}, err
	}

	var notes []string
	switch {
	case missingKeyWarning(p, conn.BaseURL, resolve) != "":
		notes = append(notes, missingKeyWarning(p, conn.BaseURL, resolve))
	case p.APIKeyEnv != "" && !(p.APIKeyOptional && catalog.IsLocalEndpoint(conn.BaseURL)):
		notes = append(notes, "API key read from LUCINATE_OPENAI_API_KEY when lucinate runs.")
	}
	notes = append(notes, fmt.Sprintf("Base URL: %s", conn.BaseURL))
	notes = append(notes, "Run 'lucinate' to use the configuration.")
	return Summary{ConfigPath: configFile, DefaultModel: defaultModel, Notes: notes}, nil
}

func (lucinateHarness) Remove(providerID string, modelKeys []string) (int, error) {
	return lucinate.Remove(providerID, modelKeys)
}

func (lucinateHarness) State() (map[string]ProviderState, string, error) {
	states, err := lucinate.State()
	if err != nil {
		return nil, "", err
	}
	out := make(map[string]ProviderState, len(states))
	for id, st := range states {
		out[id] = ProviderState{ModelKeys: st.ModelKeys, BaseURL: st.BaseURL, Contexts: st.Contexts, Outputs: st.Outputs}
	}
	// lucinate has no top-level default *model* setting distinct from the
	// connection, so there is none to report.
	return out, "", nil
}

// missingKeyWarning returns a warning when a provider's config is being written
// with no API key while its endpoint is not a local address — a combination
// that writes successfully and then fails on the first request. It happens most
// easily with a provider whose key is optional (the same engine run locally,
// where no key is needed, and remotely, where one is), so nothing errors and
// the config simply cannot authenticate. baseURL is empty when the provider
// declares none.
func missingKeyWarning(p *catalog.Provider, baseURL string, resolve func(string) string) string {
	if p.APIKeyEnv == "" || resolve(p.APIKeyEnv) != "" || catalog.IsLocalEndpoint(baseURL) {
		return ""
	}
	return fmt.Sprintf(
		"Warning: no API key was set, but %s is not a local address, so requests will "+
			"probably be rejected. Set %s and apply again.", baseURL, p.APIKeyEnv)
}
