package fleet

import "testing"

func TestValidDBName(t *testing.T) {
	ok := []string{"app_optica", "a", "db1", "app_x_y_z"}
	bad := []string{"", "1app", "App", "app-optica", "app optica", "app;drop", "app.optica"}
	for _, s := range ok {
		if !ValidDBName(s) {
			t.Errorf("ValidDBName(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if ValidDBName(s) {
			t.Errorf("ValidDBName(%q) = true, want false", s)
		}
	}
}

func TestSuggestDBName(t *testing.T) {
	cases := map[string]string{
		"optica":      "app_optica",
		"punto-gafas": "app_punto_gafas",
		"CRM":         "app_crm",
		"  shop  ":    "app_shop",
	}
	for in, want := range cases {
		if got := SuggestDBName(in); got != want {
			t.Errorf("SuggestDBName(%q) = %q, want %q", in, got, want)
		}
	}
	// The suggested name is always a valid database name.
	for _, app := range []string{"optica", "punto-gafas", "a"} {
		if !ValidDBName(SuggestDBName(app)) {
			t.Errorf("SuggestDBName(%q) is not a valid db name: %q", app, SuggestDBName(app))
		}
	}
}

func TestDeriveDSN(t *testing.T) {
	base := "postgres://appuser:secret@localhost:5432/postgres?sslmode=disable"
	got, err := DeriveDSN(base, "app_optica")
	if err != nil {
		t.Fatal(err)
	}
	want := "postgres://appuser:secret@localhost:5432/app_optica?sslmode=disable"
	if got != want {
		t.Fatalf("DeriveDSN = %q, want %q", got, want)
	}
	if DBNameOf(got) != "app_optica" {
		t.Fatalf("DBNameOf = %q", DBNameOf(got))
	}
	if _, err := DeriveDSN("://bad", "x"); err == nil {
		t.Error("expected error on unparseable base DSN")
	}
}
