package rbac_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/appximo/appximo/pkg/rbac"
)

// loadLogisticsPolicy parses the RBAC section from testdata/logistics/schema.json.
func loadLogisticsPolicy(t *testing.T) *rbac.Policy {
	t.Helper()

	data, err := os.ReadFile("../../testdata/logistics/schema.json")
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}

	var wrapper struct {
		RBAC rbac.Policy `json:"rbac"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		t.Fatalf("parse policy: %v", err)
	}
	return &wrapper.RBAC
}

func TestAllows_SuperAdminDeleteGuides(t *testing.T) {
	p := loadLogisticsPolicy(t)
	if !p.Allows("super_admin", "guides", "delete") {
		t.Error("expected super_admin to be allowed to delete guides")
	}
}

func TestAllows_OperarioCreateGuides(t *testing.T) {
	p := loadLogisticsPolicy(t)
	if !p.Allows("operario", "guides", "create") {
		t.Error("expected operario to be allowed to create guides")
	}
}

func TestAllows_OperarioDeleteGuides_Forbidden(t *testing.T) {
	p := loadLogisticsPolicy(t)
	if p.Allows("operario", "guides", "delete") {
		t.Error("expected operario to be forbidden from deleting guides")
	}
}

func TestAllows_TerceroReadGuides(t *testing.T) {
	p := loadLogisticsPolicy(t)
	if !p.Allows("tercero", "guides", "read") {
		t.Error("expected tercero to be allowed to read guides")
	}
}

func TestAllows_PublicReadGuides(t *testing.T) {
	p := loadLogisticsPolicy(t)
	if !p.Allows("public", "guides", "read") {
		t.Error("expected public to be allowed to read guides")
	}
}

func TestAllows_UnknownRole_Forbidden(t *testing.T) {
	p := loadLogisticsPolicy(t)
	if p.Allows("ghost", "guides", "read") {
		t.Error("expected unknown role to be forbidden")
	}
}

func TestEvaluate_OperarioConditionResolvesUserID(t *testing.T) {
	p := loadLogisticsPolicy(t)

	const testUserID = "user-abc-123"
	result := p.Evaluate(rbac.EvalContext{
		Role:   "operario",
		UserID: testUserID,
	}, "guides", "create")

	if !result.Allowed {
		t.Fatal("expected operario/create/guides to be allowed")
	}
	if result.Condition == nil {
		t.Fatal("expected a Condition in the result for operario")
	}
	if result.Condition.Value != testUserID {
		t.Errorf("expected Condition.Value %q, got %q", testUserID, result.Condition.Value)
	}
	if result.Condition.Field != "operator_id" {
		t.Errorf("expected Condition.Field 'operator_id', got %q", result.Condition.Field)
	}
	if result.Condition.Op != "eq" {
		t.Errorf("expected Condition.Op 'eq', got %q", result.Condition.Op)
	}
}
