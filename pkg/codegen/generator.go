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

	"github.com/miguelangel/appitools/pkg/schema"
)

//go:embed templates/handler.tmpl
var handlerTmplSrc string

//go:embed templates/router.tmpl
var routerTmplSrc string

type ResourceData struct {
	Name  string
	Title string
}

type handlerData struct {
	ResourceName  string
	ResourceTitle string
}

type routerData struct {
	Resources []ResourceData
}

// Generate writes handler files and router.go into outputDir/internal/handlers/.
// Returns the relative paths of all generated files.
func Generate(s *schema.APISchema, outputDir string) ([]string, error) {
	handlersDir := filepath.Join(outputDir, "internal", "handlers")
	if err := os.MkdirAll(handlersDir, 0755); err != nil {
		return nil, fmt.Errorf("create handlers dir: %w", err)
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
		resources = append(resources, ResourceData{Name: resName, Title: title})

		outPath := filepath.Join(handlersDir, resName+"_handler.go")
		src, err := renderTemplate(hTmpl, handlerData{
			ResourceName:  resName,
			ResourceTitle: title,
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
	src, err := renderTemplate(rTmpl, routerData{Resources: resources})
	if err != nil {
		return nil, fmt.Errorf("render router: %w", err)
	}
	if err := os.WriteFile(routerPath, src, 0644); err != nil {
		return nil, fmt.Errorf("write router.go: %w", err)
	}
	generated = append(generated, filepath.Join("internal", "handlers", "router.go"))

	return generated, nil
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
