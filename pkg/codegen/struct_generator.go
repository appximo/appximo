package codegen

import (
	"bytes"
	"fmt"
	"go/format"
	"sort"

	"github.com/appximo/appximo/pkg/schema"
)

// GenerateStructs returns a gofmt'd Go source file declaring one typed Row struct
// per resource in s. packageName is written into the package declaration.
// Each struct always has an ID field; every other field follows the schema definition.
// Fields that are auto-generated or not required become pointer types with omitempty.
func GenerateStructs(s *schema.APISchema, packageName string) ([]byte, error) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "package %s\n\n", packageName)

	if hasTimeField(s) {
		buf.WriteString("import \"time\"\n\n")
	}

	for _, resName := range sortedResourceNames(s) {
		res := s.Resources[resName]
		writeStructDecl(&buf, resName, &res)
	}

	return format.Source(buf.Bytes())
}

func writeStructDecl(buf *bytes.Buffer, resName string, res *schema.ResourceSchema) {
	fmt.Fprintf(buf, "type %sRow struct {\n", toPascalCase(resName))
	// id is always DB-generated (UUID), always present in responses.
	buf.WriteString("\tID string `json:\"id\"`\n")

	fieldNames := make([]string, 0, len(res.Fields))
	for fn := range res.Fields {
		fieldNames = append(fieldNames, fn)
	}
	sort.Strings(fieldNames)

	for _, fn := range fieldNames {
		fd := res.Fields[fn]
		goType, isPtr := fieldGoType(fd)
		jsonTag := fn
		if isPtr {
			jsonTag += ",omitempty"
			fmt.Fprintf(buf, "\t%s *%s `json:\"%s\"`\n", toPascalCase(fn), goType, jsonTag)
		} else {
			fmt.Fprintf(buf, "\t%s %s `json:\"%s\"`\n", toPascalCase(fn), goType, jsonTag)
		}
	}
	buf.WriteString("}\n\n")
}

// fieldGoType maps a FieldDef to a Go base type and whether the field should be a pointer.
// Auto-generated fields and non-required fields become pointers (nullable in JSON responses).
func fieldGoType(fd schema.FieldDef) (goType string, pointer bool) {
	switch fd.Type {
	case "int", "int64":
		goType = "int64"
	case "float64":
		goType = "float64"
	case "bool":
		goType = "bool"
	case "time":
		goType = "time.Time"
	default: // string, uuid, text
		goType = "string"
	}
	pointer = fd.Auto || !fd.Required
	return
}

func hasTimeField(s *schema.APISchema) bool {
	for _, res := range s.Resources {
		for _, fd := range res.Fields {
			if fd.Type == "time" {
				return true
			}
		}
	}
	return false
}

func sortedResourceNames(s *schema.APISchema) []string {
	names := make([]string, 0, len(s.Resources))
	for name := range s.Resources {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
