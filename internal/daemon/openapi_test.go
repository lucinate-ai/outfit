package daemon

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lucinate-ai/outfit/internal/metrics"
	"github.com/lucinate-ai/outfit/internal/remote"
)

// The control API has consumers that are not people — the control-plane
// Lambdas curl it over SSM against hand-written TypeScript mirrors of these
// structs — so docs/openapi.yaml is a contract, and a contract that drifts is
// worse than none. These tests are what make it stay true: they compare the
// spec's routes against Routes(), and each schema's properties against the JSON
// tags of the Go struct it describes.
//
// They compare *names*, not types or descriptions. Names are where drift
// actually happens and where it actually hurts; policing the rest would be a
// code generator with extra steps, and would cost the hand-written prose that
// is the reason the spec is not generated.

const openAPIPath = "../../docs/openapi.yaml"

// schemaFor maps each Go type the API serialises to the schema describing it in
// docs/openapi.yaml.
//
// ADDING A RESPONSE TYPE? Add a line here. This table is the one piece of the
// wiring the tests cannot infer, so a type with no line is a type with no
// coverage.
func schemaFor() map[string]any {
	return map[string]any{
		"StatusResponse": StatusResponse{},
		"Message":        Message{},
		"LogsResponse":   LogsResponse{},
		"Error":          Error{},
		"Stats":          metrics.Stats{},
		"TokenStats":     metrics.TokenStats{},
		"GpuStat":        metrics.GpuStat{},
		"CpuStat":        metrics.CpuStat{},
		"MemoryStat":     metrics.MemoryStat{},
		"DeployConfig":   remote.DeployConfig{},
	}
}

// openAPIDoc is the slice of the spec these tests read.
type openAPIDoc struct {
	Paths      map[string]map[string]yaml.Node `yaml:"paths"`
	Components struct {
		Schemas map[string]struct {
			Properties map[string]yaml.Node `yaml:"properties"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

func loadOpenAPI(t *testing.T) openAPIDoc {
	t.Helper()
	data, err := os.ReadFile(filepath.FromSlash(openAPIPath))
	if err != nil {
		t.Fatalf("reading the OpenAPI spec: %v", err)
	}
	var doc openAPIDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing %s: %v", openAPIPath, err)
	}
	return doc
}

// TestOpenAPICoversEveryRoute fails when the spec and the handler disagree
// about which endpoints exist.
func TestOpenAPICoversEveryRoute(t *testing.T) {
	doc := loadOpenAPI(t)

	described := map[string]bool{}
	for path, operations := range doc.Paths {
		for method := range operations {
			described[strings.ToUpper(method)+" "+path] = true
		}
	}

	served := map[string]bool{}
	for _, route := range Routes() {
		served[route.Pattern] = true
	}

	for pattern := range served {
		if !described[pattern] {
			t.Errorf("route %q is served but not described in %s — add it to the spec", pattern, openAPIPath)
		}
	}
	for pattern := range described {
		if !served[pattern] {
			t.Errorf("route %q is described in %s but not served — remove it, or register it in Routes()", pattern, openAPIPath)
		}
	}
}

// TestOpenAPIRouteResponseSchemasExist checks that every schema Routes() names
// as a success reply is actually defined, so the table stays meaningful.
func TestOpenAPIRouteResponseSchemasExist(t *testing.T) {
	doc := loadOpenAPI(t)
	for _, route := range Routes() {
		if _, ok := doc.Components.Schemas[route.ResponseSchema]; !ok {
			t.Errorf("route %q names response schema %q, which %s does not define",
				route.Pattern, route.ResponseSchema, openAPIPath)
		}
		if _, ok := schemaFor()[route.ResponseSchema]; !ok {
			t.Errorf("route %q names response schema %q, which has no Go type in schemaFor()",
				route.Pattern, route.ResponseSchema)
		}
	}
}

// TestOpenAPISchemasMatchGoTypes fails when a schema's properties and the JSON
// tags of the struct it describes disagree — a field added, removed or renamed
// on one side only.
func TestOpenAPISchemasMatchGoTypes(t *testing.T) {
	doc := loadOpenAPI(t)

	for name, value := range schemaFor() {
		schema, ok := doc.Components.Schemas[name]
		if !ok {
			t.Errorf("Go type %T has no %q schema in %s", value, name, openAPIPath)
			continue
		}
		want := jsonFieldNames(reflect.TypeOf(value))
		for _, field := range want {
			if _, ok := schema.Properties[field]; !ok {
				t.Errorf("%s.%s is serialised by %T but missing from the %q schema in %s",
					name, field, value, name, openAPIPath)
			}
		}
		for field := range schema.Properties {
			if !contains(want, field) {
				t.Errorf("the %q schema in %s describes %q, which %T does not serialise",
					name, openAPIPath, field, value)
			}
		}
	}
}

// TestOpenAPIDescribesEveryDeclaredSchema catches a schema left behind in the
// spec after the Go type it described was deleted.
func TestOpenAPIDescribesEveryDeclaredSchema(t *testing.T) {
	doc := loadOpenAPI(t)
	known := schemaFor()
	var orphans []string
	for name := range doc.Components.Schemas {
		if _, ok := known[name]; !ok {
			orphans = append(orphans, name)
		}
	}
	sort.Strings(orphans)
	for _, name := range orphans {
		t.Errorf("the %q schema in %s has no Go type — delete it, or add it to schemaFor()", name, openAPIPath)
	}
}

// jsonFieldNames lists the JSON names a struct serialises, following embedded
// structs and honouring `json:"-"`.
func jsonFieldNames(t reflect.Type) []string {
	var names []string
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		tag := field.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			if field.Anonymous && field.Type.Kind() == reflect.Struct {
				names = append(names, jsonFieldNames(field.Type)...)
				continue
			}
			name = field.Name
		}
		names = append(names, name)
	}
	return names
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
