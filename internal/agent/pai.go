package agent

// PAI (kawayiYokami/P-ai, the Tauri desktop app) keeps its settings in
// app_config.toml beside its data, at ProjectDirs(ai/easycall/p-ai)'s
// config directory on every system (resolve_standard_config_dirs,
// features/core/domain/runtime_state.rs; a portable install keeps them in
// data/ next to the exe, which magpie can't find a home for). Its
// providers are an [[apiProviders]] array of ApiProviderConfig
// (types_config.rs), camelCase keys, each provider's models child
// [[apiProviders.models]] entries of ApiModelConfig; a persona picks one
// of the provider's models by "<provider id>::<model id>"
// (api_endpoint_id, features/config/storage_and_stt.rs), and
// expand_api_configs_from_providers makes an ApiConfig of each pair, whose
// base_url and api_key the request takes from the provider. The config is
// read once at start-up and then again whenever its mtime moves
// (state_read_config_cached_with_detail), so a change needs no restart;
// the app writes the whole file back, every key magpie writes being one it
// knows. A provider the app made itself always carries baseUrl and
// apiKeys, a model its model.
//
// magpie adds a provider of its own, id "magpie", requestFormat "openai"
// (the gateway's /v1/chat/completions), with the catalog as its models,
// and points the main config's selectedApiConfigId at one of them; the
// endpoint the user had is stashed and put back when magpie steps out, and
// the "magpie" provider goes again then. The user's providers, models and
// keys are left as they are. The gateway asks no key of this machine, but
// the app refuses a chat config whose key is empty
// (chat_api_has_required_auth), so magpie's provider carries one naming
// PAI.

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/gateway"
)

// paiConfig is what magpie reads of app_config.toml.
type paiConfig struct {
	SelectedAPIConfigID string `toml:"selectedApiConfigId"`
	APIProviders        []struct {
		ID         string   `toml:"id"`
		Name       string   `toml:"name"`
		Deprecated bool     `toml:"deprecated"`
		BaseURL    string   `toml:"baseUrl"`
		APIKeys    []string `toml:"apiKeys"`
		Models     []struct {
			ID          string `toml:"id"`
			Model       string `toml:"model"`
			Deprecated  bool   `toml:"deprecated"`
			DisplayName string `toml:"displayName"`
		} `toml:"models"`
	} `toml:"apiProviders"`
}

// readPai is the config at path; a missing file is an empty one, and one
// that can't be read or parsed is an error.
func readPai(path string) (paiConfig, error) {
	var c paiConfig
	raw, err := edit.Read(path)
	if err != nil || len(raw) == 0 {
		return c, err
	}
	err = toml.Unmarshal(raw, &c)
	return c, err
}

// paiEndpoint is the endpoint id of a magpie model, as the app spells it
// (api_endpoint_id): "<provider id>::<model id>".
func paiEndpoint(model string) string {
	return magpieID + "::" + model
}

// paiDir is PAI's config directory on this machine: %APPDATA%\p-ai on
// Windows (the directories crate's ProjectDirs::from("ai", "easycall",
// "p-ai"), which names the Roaming folder there), ~/.config/p-ai on
// Linux's XDG layout, ~/Library/Application Support/p-ai on macOS.
func paiDir() string {
	if runtime.GOOS == "windows" {
		if app := appdir.Getenv("APPDATA"); app != "" {
			return filepath.Join(app, "p-ai")
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "AppData", "Roaming", "p-ai")
	}
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "p-ai")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "p-ai")
}

// pai is PAI on this machine; the app has no CLI a distro would hold, so
// there is no paiIn.
func pai(home string) *Agent {
	_ = home
	dir := paiDir()
	path := filepath.Join(dir, "app_config.toml")
	stashKey := "pai:" + path + ":selected"
	// keyNew marks a file magpie made, which goes again once magpie's
	// provider and endpoint are taken out of it and nothing is left
	keyNew := "pai:" + path + ":new"
	key := agentKeyAt("pai", gateway.URL())
	selected := func() string {
		c, err := readPai(path)
		if err != nil {
			return ""
		}
		return c.SelectedAPIConfigID
	}
	// on: magpie's — the endpoint is one of the magpie provider's
	on := func() bool {
		p, m, ok := strings.Cut(selected(), "::")
		return ok && p == magpieID && m != ""
	}
	// ours is magpie's provider entry, false where there is none
	ours := func() bool {
		c, err := readPai(path)
		if err != nil {
			return false
		}
		for _, p := range c.APIProviders {
			if p.ID == magpieID {
				return true
			}
		}
		return false
	}
	// put sets the endpoint the main config starts on, or takes it out for ""
	put := func(v string) error {
		if v == "" {
			return edit.DelTOMLTop(path, "selectedApiConfigId")
		}
		return edit.SetTOMLTop(path, edit.KV{Path: "selectedApiConfigId", Value: v})
	}
	// writeProvider is magpie's [[apiProviders]] entry, the catalog as its
	// models, spelled as the app's own writer spells one (TOML with
	// camelCase keys, the models child array tables after the provider's
	// own keys)
	writeProvider := func() error {
		var b strings.Builder
		line := func(k, v string) { b.WriteString(k + " = " + v + "\n") }
		b.WriteString("[[apiProviders]]\n")
		line("id", paiString(magpieID))
		line("name", paiString("Magpie"))
		line("requestFormat", paiString("openai"))
		line("enableText", "true")
		line("enableTools", "true")
		line("baseUrl", paiString(gateway.URL()+"/v1"))
		line("apiKeys", "["+paiString(key)+"]")
		for _, m := range magpieModels("pai") {
			b.WriteString("\n[[apiProviders.models]]\n")
			line("id", paiString(m.ID))
			line("model", paiString(m.ID))
			if m.Name != "" && m.Name != m.ID {
				line("displayName", paiString(m.Name))
			}
			line("enableTools", "true")
			if m.Images {
				line("enableImage", "true")
			}
			if m.Context > 0 {
				line("contextWindowTokens", strconv.Itoa(m.Context))
			}
			if m.Output > 0 {
				line("maxOutputTokens", strconv.Itoa(m.Output))
			}
		}
		return edit.SetTOMLArrayTable(path, "apiProviders", "id", magpieID, strings.TrimSuffix(b.String(), "\n"))
	}
	return atomic(&Agent{
		ID: "pai", Name: "PAI", Icon: "pai", Bin: "p-ai",
		Aliases: []string{"p-ai", "pai-desktop"},
		Dir:     dir, Path: path, Spelled: prefixed,
		Check: func() string {
			if !on() {
				return ""
			}
			if !ours() {
				return "PAI's magpie provider (app_config.toml) is gone, so it no longer reaches magpie"
			}
			c, err := readPai(path)
			if err != nil {
				return err.Error()
			}
			sel := c.SelectedAPIConfigID
			_, m, _ := strings.Cut(sel, "::")
			found := false
			for _, p := range c.APIProviders {
				if p.ID != magpieID {
					continue
				}
				for _, model := range p.Models {
					found = found || model.ID == m && !model.Deprecated
				}
				if !found {
					continue
				}
				// the model is there; the provider's wiring is checked as
				// one: a baseUrl at a port magpie no longer holds (the
				// gateway moved) is off, and a key that isn't one of the
				// gateway's any more too
				wantBase, wantKey := gateway.URL()+"/v1", agentKeyAt("pai", gateway.URL())
				return wiringOff("PAI", path, func(k string) (string, bool) {
					switch k {
					case "baseUrl":
						return p.BaseURL, p.BaseURL != ""
					case "apiKeys":
						return strings.Join(p.APIKeys, " "), len(p.APIKeys) > 0
					}
					return "", false
				}, "baseUrl", wantBase, "apiKeys", wantKey)
			}
			return "PAI's model " + m + " (app_config.toml) is gone, so it no longer reaches magpie"
		},
		Fields: []Field{{
			Key: "model", Label: "model",
			Get: func() string {
				sel := selected()
				if !on() {
					return sel
				}
				_, m, _ := strings.Cut(sel, "::")
				return magpieID + "/" + m
			},
			Set: func(v string) error {
				if ref, ok := strings.CutPrefix(v, magpieID+"/"); ok && isMagpie(ref) {
					// what the user had, unless magpie stashed it already
					if !on() {
						forget(stashKey, keyNew)
						stash(map[string]string{stashKey: selected()})
						if !isFile(path) {
							stash(map[string]string{keyNew: "1"})
						}
					}
					if err := writeProvider(); err != nil {
						return err
					}
					return put(paiEndpoint(ref))
				}
				made := false
				if on() || ours() {
					made = unstash(keyNew) != ""
					// back to what the user had
					if v == "" {
						v = unstash(stashKey)
					} else {
						forget(stashKey)
					}
					if err := edit.DelTOMLArrayTable(path, "apiProviders", "id", magpieID); err != nil {
						return err
					}
				}
				if err := put(v); err != nil {
					return err
				}
				if made {
					if raw, err := os.ReadFile(path); err == nil {
						var now map[string]any
						if toml.Unmarshal(raw, &now) == nil && len(now) == 0 {
							if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
								return err
							}
						}
					}
				}
				return nil
			},
			Options: func(cur map[string]string) []Option {
				var own []Option
				if c := cur["model"]; c != "" && !usesMagpie(c) {
					own = append(own, Option{Value: c, Icon: modelIcon("", c)})
				}
				return append(group("PAI", own), viaMagpie("pai", magpieID+"/")...)
			},
		}},
	}, path)
}

// paiString is s as a TOML basic string: JSON's escapes are all TOML's.
func paiString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
