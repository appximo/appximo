package schema

import (
	"bytes"
	_ "embed"
	"errors"
	"sort"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// AI-F0-S1: the formal JSON Schema (Draft 2020-12) meta-schema for an Appximo
// project schema. It is the deterministic STRUCTURAL net for the AI generation
// stage: a document that passes it is structurally valid (types, enums, name
// patterns, allowed keys, required fields, the two RBAC forms) without booting the
// engine. The Go Validate (validator.go) remains the authority for SEMANTIC
// cross-reference checks JSON Schema cannot express (a relation/FK target must
// EXIST and be unique on the target; a condition/allowlist field must exist on the
// resource; renamed_from must not still be a declared name; …). The meta-schema is
// kept in lockstep with the validator and the parity is enforced by
// metaschema_test.go.
//
//go:embed appximo.schema.json
var metaSchemaJSON []byte

const metaSchemaURL = "https://appximo.com/meta/appximo.schema.json"

var (
	metaSchemaOnce sync.Once
	metaSchema     *jsonschema.Schema
	metaSchemaErr  error
)

// MetaSchemaJSON returns the raw bytes of the embedded meta-schema (for the
// `appximo meta-schema` command, IDE integration, and external tooling).
func MetaSchemaJSON() []byte {
	out := make([]byte, len(metaSchemaJSON))
	copy(out, metaSchemaJSON)
	return out
}

// compiledMetaSchema compiles the embedded meta-schema ONCE (it never changes at
// runtime). A compile error here is a build-time bug in the meta-schema, surfaced
// the first time it is used.
func compiledMetaSchema() (*jsonschema.Schema, error) {
	metaSchemaOnce.Do(func() {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(metaSchemaJSON))
		if err != nil {
			metaSchemaErr = err
			return
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource(metaSchemaURL, doc); err != nil {
			metaSchemaErr = err
			return
		}
		metaSchema, metaSchemaErr = c.Compile(metaSchemaURL)
	})
	return metaSchema, metaSchemaErr
}

// ValidateAgainstMetaSchema validates raw schema bytes against the embedded JSON
// Schema meta-schema and returns one ValidationError per STRUCTURAL violation
// (Field is the JSON-pointer instance location, Message the reason). It is ADDITIVE
// to Validate, never a replacement: it catches the structural errors JSON Schema can
// express (early, precise, engine-free), while Validate stays the semantic
// authority. Returns nil when the document is structurally valid.
func ValidateAgainstMetaSchema(raw []byte) []ValidationError {
	sch, err := compiledMetaSchema()
	if err != nil {
		return []ValidationError{{Field: "$", Message: "meta-schema compile error: " + err.Error()}}
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return []ValidationError{{Field: "$", Message: "schema is not valid JSON: " + err.Error()}}
	}
	verr := sch.Validate(inst)
	if verr == nil {
		return nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(verr, &ve) {
		return []ValidationError{{Field: "$", Message: verr.Error()}}
	}

	var errs []ValidationError
	collectMetaErrors(ve.DetailedOutput(), &errs)
	if len(errs) == 0 {
		// Fall back to the full message if no leaf carried a message.
		errs = append(errs, ValidationError{Field: instancePath(""), Message: ve.Error()})
	}
	sort.Slice(errs, func(i, j int) bool {
		if errs[i].Field != errs[j].Field {
			return errs[i].Field < errs[j].Field
		}
		return errs[i].Message < errs[j].Message
	})
	return errs
}

// collectMetaErrors walks the DetailedOutput tree and appends one ValidationError
// per LEAF failure — a unit that carries a message and has no child errors. The
// leaves hold the specific reasons ("value must be one of …", "additional property
// … not allowed", "missing property …"); intermediate nodes (oneOf/allOf/$ref
// wrappers) carry only a generic message and are skipped in favor of their
// children, so the AI gets the precise, located cause.
func collectMetaErrors(u *jsonschema.OutputUnit, out *[]ValidationError) {
	if u == nil {
		return
	}
	if len(u.Errors) == 0 {
		if u.Error != nil {
			if msg := u.Error.String(); msg != "" {
				*out = append(*out, ValidationError{Field: instancePath(u.InstanceLocation), Message: msg})
			}
		}
		return
	}
	for i := range u.Errors {
		collectMetaErrors(&u.Errors[i], out)
	}
}

// instancePath turns a JSON-pointer instance location ("/resources/tasks/fields")
// into the dotted path style the rest of the validator uses ("resources.tasks.
// fields"); the document root ("") becomes "$".
func instancePath(loc string) string {
	if loc == "" || loc == "/" {
		return "$"
	}
	p := loc
	if len(p) > 0 && p[0] == '/' {
		p = p[1:]
	}
	// JSON Pointer escapes: ~1 → "/", ~0 → "~". Unescape, then join segments by '.'.
	out := make([]byte, 0, len(p))
	for i := 0; i < len(p); i++ {
		switch {
		case p[i] == '/':
			out = append(out, '.')
		case p[i] == '~' && i+1 < len(p) && p[i+1] == '1':
			out = append(out, '/')
			i++
		case p[i] == '~' && i+1 < len(p) && p[i+1] == '0':
			out = append(out, '~')
			i++
		default:
			out = append(out, p[i])
		}
	}
	return string(out)
}
