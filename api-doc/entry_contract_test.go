package apidoc_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// localYAMLLoader deliberately has no network fallback and confines references to api-doc.
type localYAMLLoader struct{ root string }

func (loader localYAMLLoader) Load(location string) (any, error) {
	u, err := url.Parse(location)
	if err != nil {
		return nil, fmt.Errorf("parse schema location: %w", err)
	}
	if u.Scheme != "file" || u.Host != "" || u.RawQuery != "" {
		return nil, fmt.Errorf("only local schema files are allowed: %s", location)
	}
	path, err := filepath.EvalSymlinks(filepath.FromSlash(u.Path))
	if err != nil {
		return nil, fmt.Errorf("resolve schema file: %w", err)
	}
	rel, err := filepath.Rel(loader.root, path)
	if err != nil || !filepath.IsLocal(rel) {
		return nil, fmt.Errorf("schema file is outside api-doc: %s", location)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read schema file: %w", err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("decode schema YAML: %w", err)
	}
	// Normalize YAML numbers and timestamps to the validator's JSON value model.
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode schema JSON: %w", err)
	}
	return jsonschema.UnmarshalJSON(strings.NewReader(string(encoded)))
}

func entryCompiler(t *testing.T) (*jsonschema.Compiler, localYAMLLoader) {
	t.Helper()
	root, err := filepath.Abs(".")
	require.NoError(t, err)
	root, err = filepath.EvalSymlinks(root)
	require.NoError(t, err)
	loader := localYAMLLoader{root: root}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	compiler.UseLoader(loader)
	return compiler, loader
}

func (loader localYAMLLoader) location(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(loader.root, path))}).String()
}

func (loader localYAMLLoader) value(t *testing.T, location string) any {
	t.Helper()
	u, err := url.Parse(location)
	require.NoError(t, err)
	value, err := loader.Load(location)
	require.NoError(t, err)
	for _, part := range strings.Split(strings.TrimPrefix(u.Fragment, "/"), "/") {
		object, ok := value.(map[string]any)
		require.True(t, ok, "JSON pointer parent must be an object")
		key := strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		value, ok = object[key]
		require.True(t, ok, "missing JSON pointer member %s", key)
	}
	return value
}

func TestEntryResponseExamples(t *testing.T) {
	compiler, loader := entryCompiler(t)
	for _, test := range []struct {
		file, pointer string
		names         []string
	}{
		{"paths/auth.yaml", "/sessionDiscovery/get/responses/200/content/application~1vnd.api+json",
			[]string{"anonymous", "mfa", "passwordChange", "authenticated"}},
		{"paths/users.yaml", "/capabilities/get/responses/200/content/application~1vnd.api+json",
			[]string{"singleRole", "multiRole", "emptyAssignments"}},
	} {
		t.Run(test.pointer, func(t *testing.T) {
			location := loader.location(test.file) + "#" + test.pointer
			schema, err := compiler.Compile(location + "/schema")
			require.NoError(t, err, "response schema must compile before validating examples")
			examples := object(t, loader.value(t, location+"/examples"))
			assert.Len(t, examples, len(test.names))
			for _, name := range test.names {
				t.Run(name, func(t *testing.T) {
					example := object(t, loader.value(t, location+"/examples/"+name))
					require.Contains(t, example, "value")
					assert.NoError(t, schema.Validate(example["value"]))
					if test.file == "paths/users.yaml" {
						assertCapabilityCatalog(t, loader, example["value"])
					}
				})
			}
		})
	}
}

func assertCapabilityCatalog(t *testing.T, loader localYAMLLoader, document any) {
	t.Helper()
	attributes := object(t, object(t, object(t, document)["data"])["attributes"])
	capabilities, ok := attributes["capabilities"].([]any)
	require.True(t, ok, "capabilities must be an array")
	var actions []any
	for _, item := range capabilities {
		actions = append(actions, object(t, item)["action"])
	}
	want := loader.value(t, loader.location("schemas/resources/resources.yaml")+"#/AccountCapability/properties/action/enum")
	assert.ElementsMatch(t, want, actions, "examples must contain every action exactly once")
}

func TestEntryStageSchemaConstraints(t *testing.T) {
	compiler, loader := entryCompiler(t)
	for _, schemaName := range []string{"AuthSessionDocument", "SessionDiscoveryDocument"} {
		t.Run(schemaName, func(t *testing.T) {
			schema, err := compiler.Compile(loader.location("schemas/resources/resources.yaml") + "#/" + schemaName)
			require.NoError(t, err, "schema compilation failure is not expected instance invalidity")
			for _, stage := range []string{"mfa", "password-change", "authenticated"} {
				for _, challenge := range []struct {
					name    string
					value   any
					present bool
				}{
					{"missing", nil, false}, {"opaque", "arbitrary opaque identity", true},
					{"empty", "", true}, {"null", nil, true}, {"number", 42, true},
					{"boolean", true, true}, {"array", []any{"id"}, true}, {"object", map[string]any{}, true},
				} {
					t.Run(stage+"/"+challenge.name, func(t *testing.T) {
						attrs := map[string]any{"stage": stage, "idle_expires_at": "2026-09-27T10:30:00.000000Z",
							"absolute_expires_at": "2026-09-27T22:00:00.000000Z", "extension": true}
						if challenge.present {
							attrs["mfa_challenge_id"] = challenge.value
						}
						document := map[string]any{"data": map[string]any{
							"type": "auth-sessions", "id": "example-account", "attributes": attrs}}
						valid := stage == "mfa" && challenge.name == "opaque" || stage != "mfa" && !challenge.present
						assertInstanceValidity(t, schema, document, valid)
					})
				}
			}
			anonymous := loader.value(t, loader.location("paths/auth.yaml")+
				"#/sessionDiscovery/get/responses/200/content/application~1vnd.api+json/examples/anonymous/value")
			assertInstanceValidity(t, schema, anonymous, schemaName == "SessionDiscoveryDocument")
			badDate := loader.value(t, loader.location("paths/auth.yaml")+
				"#/sessionDiscovery/get/responses/200/content/application~1vnd.api+json/examples/authenticated/value")
			object(t, object(t, object(t, badDate)["data"])["attributes"])["idle_expires_at"] = "not-a-date"
			assertInstanceValidity(t, schema, badDate, false)
		})
	}
}

func object(t *testing.T, value any) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	require.True(t, ok, "expected a JSON object")
	return result
}

func assertInstanceValidity(t *testing.T, schema *jsonschema.Schema, document any, valid bool) {
	t.Helper()
	err := schema.Validate(document)
	if valid {
		require.NoError(t, err)
	} else {
		var validationError *jsonschema.ValidationError
		require.ErrorAs(t, err, &validationError, "expected instance validation failure")
	}
}

func TestEntrySchemaLoaderRejectsNonlocalReferences(t *testing.T) {
	compiler, _ := entryCompiler(t)
	for _, location := range []string{"https://example.test/schema.json", "http://example.test/schema.json",
		"file://remote/schema.yaml"} {
		_, err := compiler.Compile(location)
		require.ErrorContains(t, err, "only local schema files are allowed:")
	}

	// Both fixtures are valid schemas: rejection must come from the path policy,
	// not from YAML decoding or schema compilation. TempDir owns all cleanup.
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	fixtureLoader := localYAMLLoader{root: root}
	outside := filepath.Join(root, "outside.yaml")
	require.NoError(t, os.WriteFile(outside, []byte("type: string\n"), 0o600))
	allowed := filepath.Join(root, "allowed")
	require.NoError(t, os.Mkdir(allowed, 0o700))
	require.NoError(t, os.Symlink(outside, filepath.Join(allowed, "escape.yaml")))
	fixtureCompiler := jsonschema.NewCompiler()
	fixtureCompiler.DefaultDraft(jsonschema.Draft2020)
	fixtureCompiler.UseLoader(fixtureLoader)
	_, err = fixtureCompiler.Compile(fixtureLoader.location("outside.yaml"))
	require.NoError(t, err, "outside fixture must be a valid schema when permitted")
	for _, path := range []string{"../outside.yaml", "escape.yaml"} {
		t.Run(path, func(t *testing.T) {
			confined := localYAMLLoader{root: allowed}
			compiler := jsonschema.NewCompiler()
			compiler.UseLoader(confined)
			_, err := compiler.Compile(confined.location(path))
			require.ErrorContains(t, err, "schema file is outside api-doc:")
		})
	}
}
