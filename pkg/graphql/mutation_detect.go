package graphql

import (
	"encoding/json"

	"github.com/graphql-go/graphql/language/ast"
	"github.com/graphql-go/graphql/language/parser"
	"github.com/graphql-go/graphql/language/source"
)

// IsMutationRequest reports whether a POST /graphql body is a MUTATION — the
// one question the host memory guard needs (ENG-60, DEPLOY-FLOTA-S1). Every
// GraphQL request is a POST to one path, so a verb-keyed write guard refused
// GraphQL READS under memory pressure while the equivalent REST read kept
// flowing (CAOS-S1 D5 measured it). The guard calls this ONLY once it has
// already decided to refuse — a healthy host never parses anything here.
//
// The answer is conservative: a body this function cannot read as a GraphQL
// document (bad JSON, a parse error, no operation) counts as a mutation, so a
// malformed request under pressure is refused like a write (never the other
// way round — a real mutation must never slip past the guard because it was
// dressed as unreadable). `operationName` is honored the way the executor
// honors it; without one, a document is a mutation if ANY of its operations
// is (an anonymous multi-operation document is an error anyway).
func IsMutationRequest(body []byte) bool {
	var params struct {
		Query         string `json:"query"`
		OperationName string `json:"operationName"`
	}
	if err := json.Unmarshal(body, &params); err != nil || params.Query == "" {
		return true
	}
	doc, err := parser.Parse(parser.ParseParams{
		Source: source.NewSource(&source.Source{Body: []byte(params.Query), Name: "GraphQL"}),
	})
	if err != nil {
		return true
	}
	seen := false
	for _, def := range doc.Definitions {
		op, ok := def.(*ast.OperationDefinition)
		if !ok {
			continue
		}
		if params.OperationName != "" {
			if op.Name == nil || op.Name.Value != params.OperationName {
				continue
			}
		}
		seen = true
		if op.Operation == "mutation" {
			return true
		}
	}
	return !seen // no operation matched → unreadable → refuse like a write
}
