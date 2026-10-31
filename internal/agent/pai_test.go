package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/usage"
)

// paiFixture is app_config.toml as PAI 0.97 writes it: the main config's
// own keys, a provider of the user's with a model of its own, and the
// endpoint the user picked on it.
func paiFixture() string {
	return `hotkey = "Alt+·"
uiLanguage = "zh-CN"
selectedApiConfigId = "provider-a::model-a"

[[apiProviders]]
id = "provider-a"
name = "Provider A"
requestFormat = "openai"
enableText = true
enableTools = true
baseUrl = "https://api.openai.com/v1"
apiKeys = ["sk-test-secret"]

[[apiProviders.models]]
id = "model-a"
model = "gpt-4o-mini"
enableTools = true
contextWindowTokens = 128000
`
}

// paiOf is the pai of All(), in a sandbox home.
func paiOf(t *testing.T) *Agent {
	t.Helper()
	for _, x := range All() {
		if x.ID == "pai" {
			return x
		}
	}
	t.Fatal("no pai in All()")
	return nil
}

// paiFile is what magpie's checks read of the file at path.
type paiFile struct {
	SelectedAPIConfigID string `toml:"selectedApiConfigId"`
	APIProviders        []struct {
		ID            string   `toml:"id"`
		Name          string   `toml:"name"`
		RequestFormat string   `toml:"requestFormat"`
		BaseURL       string   `toml:"baseUrl"`
		APIKeys       []string `toml:"apiKeys"`
		Models        []struct {
			ID                  string `toml:"id"`
			Model               string `toml:"model"`
			DisplayName         string `toml:"displayName"`
			ContextWindowTokens int    `toml:"contextWindowTokens"`
			MaxOutputTokens     int    `toml:"maxOutputTokens"`
			EnableImage         bool   `toml:"enableImage"`
		} `toml:"models"`
	} `toml:"apiProviders"`
}

func readPaiFile(t *testing.T, path string) paiFile {
	t.Helper()
	var f paiFile
	if err := toml.Unmarshal([]byte(readFile(path)), &f); err != nil {
		t.Fatalf("%v:\n%s", err, readFile(path))
	}
	return f
}

func paiProviderOf(f paiFile) (int, bool) {
	for i, p := range f.APIProviders {
		if p.ID == "magpie" {
			return i, true
		}
	}
	return 0, false
}

// PAI is wired in its app_config.toml: magpie as an [[apiProviders]] entry
// (OpenAI, the gateway's /v1, a key naming PAI) with the catalog as its
// models, and selectedApiConfigId on one of them. The user's provider,
// model and pick stay; Disconnect puts the endpoint back and magpie's
// provider out.
func TestPai(t *testing.T) {
	home := syncHome(t)
	a := paiOf(t)
	path := filepath.Join(home, "AppData", "Roaming", "p-ai", "app_config.toml")
	if a.Name != "PAI" || a.Dir != filepath.Join(home, "AppData", "Roaming", "p-ai") || a.Path != path || a.Detected() {
		t.Fatalf("%+v", a)
	}
	writeFile(t, path, paiFixture())
	if !a.Detected() || a.Field("model").Get() != "provider-a::model-a" {
		t.Fatalf("detected %v, model %q", a.Detected(), a.Field("model").Get())
	}
	vals := []string{}
	for _, o := range a.Field("model").Options(map[string]string{"model": a.Field("model").Get()}) {
		vals = append(vals, o.Value)
	}
	if !slices.Contains(vals, "provider-a::model-a") || !slices.Contains(vals, "magpie/relay/glm-4.6") {
		t.Fatalf("options %v", vals)
	}

	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	f := readPaiFile(t, path)
	if f.SelectedAPIConfigID != "magpie::relay/glm-4.6" {
		t.Fatalf("selectedApiConfigId %q:\n%s", f.SelectedAPIConfigID, readFile(path))
	}
	p, ok := paiProviderOf(f)
	if !ok {
		t.Fatalf("no magpie provider:\n%s", readFile(path))
	}
	mine := f.APIProviders[p]
	if mine.BaseURL != gateway.URL()+"/v1" || mine.RequestFormat != "openai" || !slices.Contains(mine.APIKeys, "magpie-pai") {
		t.Fatalf("provider %+v", mine)
	}
	if len(mine.Models) == 0 || mine.Models[0].ID != "relay/glm-4.6" || mine.Models[0].Model != "relay/glm-4.6" || mine.Models[0].ContextWindowTokens != 204800 {
		t.Fatalf("models %v", mine.Models)
	}
	// the user's provider is as it was, pick included
	var theirs bool
	for _, pr := range f.APIProviders {
		theirs = theirs || pr.ID == "provider-a" && pr.BaseURL == "https://api.openai.com/v1" && len(pr.Models) == 1
	}
	if !theirs {
		t.Fatalf("the user's provider changed:\n%s", readFile(path))
	}
	if c := a.Check(); c != "" {
		t.Fatal(c)
	}
	if got := usage.AgentOf("pai"); got != "pai" {
		t.Fatalf("usage: %q", got)
	}

	// Disconnect puts the user's endpoint back and magpie's provider out
	if err := a.Disconnect(); err != nil {
		t.Fatal(err)
	}
	f = readPaiFile(t, path)
	if f.SelectedAPIConfigID != "provider-a::model-a" {
		t.Fatalf("endpoint back: %q:\n%s", f.SelectedAPIConfigID, readFile(path))
	}
	if _, ok := paiProviderOf(f); ok {
		t.Fatalf("magpie's provider left:\n%s", readFile(path))
	}
}

// A config magpie started holds nothing but magpie's wiring once it is
// taken out, and the file goes again.
func TestPaiRoundTripNoFile(t *testing.T) {
	syncHome(t)
	a := paiOf(t)
	path := a.Path
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	f := readPaiFile(t, path)
	if f.SelectedAPIConfigID == "" {
		t.Fatalf("nothing set:\n%s", readFile(path))
	}
	if err := a.Field("model").Set(""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the file magpie made is left: %v", err)
	}
}

// What the user had before magpie comes back when they pick a model of
// their own, magpie's provider out with it.
func TestPaiBackToTheirOwn(t *testing.T) {
	syncHome(t)
	a := paiOf(t)
	path := a.Path
	writeFile(t, path, paiFixture())
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	if err := a.Field("model").Set("provider-a::model-a"); err != nil {
		t.Fatal(err)
	}
	f := readPaiFile(t, path)
	if f.SelectedAPIConfigID != "provider-a::model-a" {
		t.Fatalf("their endpoint: %q:\n%s", f.SelectedAPIConfigID, readFile(path))
	}
	if _, ok := paiProviderOf(f); ok {
		t.Fatalf("magpie's provider left:\n%s", readFile(path))
	}
	// and again on magpie's, the first pick's stash not leaking
	if err := a.Field("model").Set("magpie/relay/glm-4.6"); err != nil {
		t.Fatal(err)
	}
	if err := a.Disconnect(); err != nil {
		t.Fatal(err)
	}
	f = readPaiFile(t, path)
	if f.SelectedAPIConfigID != "provider-a::model-a" {
		t.Fatalf("endpoint back: %q:\n%s", f.SelectedAPIConfigID, readFile(path))
	}
	if _, ok := paiProviderOf(f); ok {
		t.Fatalf("magpie's provider left:\n%s", readFile(path))
	}
}

// The endpoint magpie set is checked while it holds: magpie's provider
// gone is drift, and an endpoint of another provider is none of magpie's.
func TestPaiDrift(t *testing.T) {
	syncHome(t)
	a := paiOf(t)
	path := a.Path
	writeFile(t, path, paiFixture())
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	if c := a.Check(); c != "" {
		t.Fatalf("wired: %q", a.Check())
	}
	// the model the endpoint names gone off the provider is drift
	if err := edit.SetTOMLTop(path, edit.KV{Path: "selectedApiConfigId", Value: "magpie::gone"}); err != nil {
		t.Fatal(err)
	}
	if c := a.Check(); c == "" {
		t.Fatal("a model off the provider read as wired")
	}
	// back to magpie's, then to the user's own provider: no drift
	if err := a.Field("model").Set("magpie/relay/glm-4.6"); err != nil {
		t.Fatal(err)
	}
	if c := a.Check(); c != "" {
		t.Fatalf("wired again: %q", c)
	}
	if err := a.Field("model").Set("provider-a::model-a"); err != nil {
		t.Fatal(err)
	}
	if c := a.Check(); c != "" {
		t.Fatalf("the user's own: %q", c)
	}
	// a uiLanguage the app wrote beside magpie's wiring is theirs, never
	// drift: only the endpoint naming a model of magpie's is checked
	if err := a.Field("model").Set("magpie/relay/glm-4.6"); err != nil {
		t.Fatal(err)
	}
	if err := edit.SetTOMLTop(path, edit.KV{Path: "uiLanguage", Value: "en-US"}); err != nil {
		t.Fatal(err)
	}
	if c := a.Check(); c != "" {
		t.Fatalf("the app's own keys: %q", c)
	}
}

// The gateway's port moving rewrites PAI's baseUrl: the old address reads
// as wiring off (Drift kind unwired), and setting the model again writes
// the one it has now.
func TestPaiPortMoves(t *testing.T) {
	syncHome(t)
	a := paiOf(t)
	path := a.Path
	writeFile(t, path, paiFixture())
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(path), gateway.URL()) {
		t.Fatalf("the gateway's URL isn't there:\n%s", readFile(path))
	}
	setPort(t, 3597)
	if a.Check() == "" {
		t.Fatal("the old port reads as wired")
	}
	if d := a.Drift(); d == nil || d.Kind != "unwired" {
		t.Fatalf("drift: %+v", d)
	}
	if err := a.Reapply(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(path), gateway.URL()) {
		t.Fatalf("the URL didn't move:\n%s", readFile(path))
	}
	if strings.Contains(readFile(path), ":3597") == false {
		t.Fatal("the new port isn't in the file")
	}
	if c := a.Check(); c != "" {
		t.Fatalf("wired after the move: %q", c)
	}
	if !a.Wired() {
		t.Fatal("not connected after the move")
	}
}

// A config that isn't TOML is an error, never "empty": a model picked onto
// it would be a pick the app can't read.
func TestPaiUnreadable(t *testing.T) {
	syncHome(t)
	a := paiOf(t)
	writeFile(t, a.Path, "not toml ][")
	if a.Field("model").Get() != "" {
		t.Fatal("an unreadable file read as empty")
	}
	if err := a.Connect(); err == nil {
		t.Fatal("connected onto an unreadable file")
	}
}
