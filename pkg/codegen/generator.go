package codegen

import (
	"bytes"
	_ "embed"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/appximo/appximo/pkg/schema"
)

//go:embed templates/handler.tmpl
var handlerTmplSrc string

//go:embed templates/router.tmpl
var routerTmplSrc string

// RelationData describes a foreign-key relation on a resource field.
type RelationData struct {
	FieldName   string // e.g. "client_id"
	RelName     string // e.g. "client"  (route segment: FieldName with _id stripped)
	RelResource string // e.g. "clients" (target table)
	RelTitle    string // e.g. "Client"  (PascalCase for method names)
}

// ResourceData is the per-resource context passed to router.tmpl.
type ResourceData struct {
	Name      string
	Title     string
	Relations []RelationData
}

type handlerData struct {
	ResourceName  string
	ResourceTitle string
	ModulePath    string
	Relations     []RelationData
}

type routerData struct {
	Resources  []ResourceData
	ModulePath string
}

// Generate writes handler files and router.go into outputDir/internal/handlers/.
// Returns the relative paths of all generated files.
func Generate(s *schema.APISchema, outputDir string) ([]string, error) {
	handlersDir := filepath.Join(outputDir, "internal", "handlers")
	if err := os.MkdirAll(handlersDir, 0755); err != nil {
		return nil, fmt.Errorf("create handlers dir: %w", err)
	}

	modulePath, err := readModulePath(outputDir)
	if err != nil {
		return nil, err
	}

	hTmpl, err := template.New("handler").Parse(handlerTmplSrc)
	if err != nil {
		return nil, fmt.Errorf("parse handler template: %w", err)
	}
	rTmpl, err := template.New("router").Parse(routerTmplSrc)
	if err != nil {
		return nil, fmt.Errorf("parse router template: %w", err)
	}

	names := make([]string, 0, len(s.Resources))
	for name := range s.Resources {
		names = append(names, name)
	}
	sort.Strings(names)

	resources := make([]ResourceData, 0, len(names))
	var generated []string

	for _, resName := range names {
		title := toPascalCase(resName)
		res := s.Resources[resName]
		rels := buildRelations(&res)

		resources = append(resources, ResourceData{
			Name:      resName,
			Title:     title,
			Relations: rels,
		})

		outPath := filepath.Join(handlersDir, resName+"_handler.go")
		src, err := renderTemplate(hTmpl, handlerData{
			ResourceName:  resName,
			ResourceTitle: title,
			ModulePath:    modulePath,
			Relations:     rels,
		})
		if err != nil {
			return nil, fmt.Errorf("render handler for %q: %w", resName, err)
		}
		if err := os.WriteFile(outPath, src, 0644); err != nil {
			return nil, fmt.Errorf("write %s: %w", outPath, err)
		}
		generated = append(generated, filepath.Join("internal", "handlers", resName+"_handler.go"))
	}

	routerPath := filepath.Join(handlersDir, "router.go")
	src, err := renderTemplate(rTmpl, routerData{Resources: resources, ModulePath: modulePath})
	if err != nil {
		return nil, fmt.Errorf("render router: %w", err)
	}
	if err := os.WriteFile(routerPath, src, 0644); err != nil {
		return nil, fmt.Errorf("write router.go: %w", err)
	}
	generated = append(generated, filepath.Join("internal", "handlers", "router.go"))

	return generated, nil
}

// buildRelations extracts all RelationData entries from a ResourceSchema.
func buildRelations(res *schema.ResourceSchema) []RelationData {
	var rels []RelationData
	// sort field names for determinism
	names := make([]string, 0, len(res.Fields))
	for n := range res.Fields {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, fname := range names {
		fd := res.Fields[fname]
		if fd.Relation == "" {
			continue
		}
		relName := strings.TrimSuffix(fname, "_id")
		rels = append(rels, RelationData{
			FieldName:   fname,
			RelName:     relName,
			RelResource: fd.Relation,
			RelTitle:    toPascalCase(relName),
		})
	}
	return rels
}

func readModulePath(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
		}
	}
	return "", fmt.Errorf("module declaration not found in go.mod")
}

func renderTemplate(tmpl *template.Template, data any) ([]byte, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, err
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format source: %w\n--- raw output ---\n%s", err, buf.String())
	}
	return formatted, nil
}

func toPascalCase(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '_' })
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}
