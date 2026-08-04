package appximo

import (
	"testing"
)

// ENG-33: the App→OpenAPI projection carries exactly the engine-known facts.
func TestCustomRouteDescriptors(t *testing.T) {
	a := &App{routes: []Route{
		{Method: "POST", Path: "/api/checkout", Public: true, Description: "Guest checkout"},
		{Method: "GET", Path: "/api/reportes", RequireRole: "dueno"},
		{Method: "GET", Path: "/api/catalogo-imagen", Public: true, ByteServing: true},
	}}
	ds := a.customRouteDescriptors()
	if len(ds) != 3 {
		t.Fatalf("want 3 descriptors, got %d", len(ds))
	}
	if ds[0].Summary != "Guest checkout" || !ds[0].Public || ds[0].Method != "POST" {
		t.Errorf("descriptor 0 mis-projected: %+v", ds[0])
	}
	if ds[1].RequireRole != "dueno" || ds[1].Public {
		t.Errorf("descriptor 1 mis-projected: %+v", ds[1])
	}
	if !ds[2].ByteServing {
		t.Errorf("descriptor 2 mis-projected: %+v", ds[2])
	}
	if (&App{}).customRouteDescriptors() != nil {
		t.Error("an app with no custom routes must project nil (spec unchanged)")
	}
}

// ENG-32: a Public GET's auth skip covers its HEAD alias; non-GET methods and
// non-public routes contribute nothing extra.
func TestPublicRoutePathsIncludeHEADAlias(t *testing.T) {
	a := &App{routes: []Route{
		{Method: "GET", Path: "/api/catalogo", Public: true},
		{Method: "POST", Path: "/api/checkout", Public: true},
		{Method: "GET", Path: "/api/privado"},
	}}
	paths := a.publicRoutePaths()
	for _, want := range []string{"GET /api/catalogo", "HEAD /api/catalogo", "POST /api/checkout"} {
		if !paths[want] {
			t.Errorf("missing public skip entry %q", want)
		}
	}
	for _, no := range []string{"HEAD /api/checkout", "GET /api/privado", "HEAD /api/privado"} {
		if paths[no] {
			t.Errorf("unexpected public skip entry %q", no)
		}
	}
}
