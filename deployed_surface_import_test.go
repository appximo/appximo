package appximo

import (
	"testing"

	"github.com/appximo/appximo/pkg/schema"
)

// WRITE-ASYMMETRY-S1: the deployed surface carries the DEPLOYED resource's
// `import` declaration — including its absence — so a grant deploys (and
// un-deploys) hot, like a field's rules. It used to inherit the BOOT
// resource's, which silently ignored a hot-deployed grant (the documented
// fixture-restore flow — migrate the schema, POST with the id — would have
// 422'd until a restart).
func TestMergeWritableFields_ImportFollowsDeployed(t *testing.T) {
	boot := schema.ResourceSchema{
		Fields: map[string]schema.FieldDef{"title": {Type: "string"}},
	}
	dep := schema.ResourceSchema{
		Fields: map[string]schema.FieldDef{"title": {Type: "string"}},
		Import: &schema.ImportConfig{Roles: []string{"admin"}},
	}

	if got := mergeWritableFields(boot, dep); got.Import == nil || got.Import.Roles[0] != "admin" {
		t.Fatalf("deployed import grant must win the merge, got %+v", got.Import)
	}

	// Removal deploys hot too: boot has a grant, deployed does not → gone.
	boot.Import = &schema.ImportConfig{Roles: []string{"admin"}}
	dep.Import = nil
	if got := mergeWritableFields(boot, dep); got.Import != nil {
		t.Fatalf("a deployed schema WITHOUT the grant must remove it, got %+v", got.Import)
	}
}
