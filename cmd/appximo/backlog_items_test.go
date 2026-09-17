package main

// The open-item register has two halves (CENTRO-MANDO-S2): docs/BACKLOG.md is
// the narrative, docs/backlog/items.json is the structured form the command
// center orders by. This test is what keeps them from drifting back into
// prose: an item that exists in one half and not the other, or a JSON row
// missing a field the panel needs (what it is, why it matters, what unblocks
// it, cost, damage, priority, who decides), FAILS the unit lane. A row can
// still be WRONG in prose — but it can no longer be absent or shapeless.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type backlogItem struct {
	ID            string   `json:"id"`
	Titulo        string   `json:"titulo"`
	Frente        string   `json:"frente"`
	QueEs         string   `json:"que_es"`
	PorQueImporta string   `json:"por_que_importa"`
	QueLoDestraba string   `json:"que_lo_destraba"`
	Costo         string   `json:"costo"`
	Dano          string   `json:"dano"`
	Prioridad     string   `json:"prioridad"`
	Decide        string   `json:"decide"`
	DependeDe     []string `json:"depende_de"`
	Bloquea       []string `json:"bloquea"`
	Origen        string   `json:"origen"`
	Estado        string   `json:"estado"`
}

type backlogFile struct {
	Actualizado string `json:"actualizado"`
	Publicacion struct {
		Estado string `json:"estado"`
		Porque string `json:"porque"`
	} `json:"publicacion"`
	Items []backlogItem `json:"items"`
}

var (
	backlogFrentes    = map[string]bool{"motor": true, "flota": true, "automatizacion": true, "comercial": true, "medicion": true, "producto": true, "docs": true}
	backlogCostos     = map[string]bool{"chico": true, "medio": true, "grande": true, "decision": true}
	backlogDanos      = map[string]bool{"alto": true, "medio": true, "bajo": true}
	backlogPrioridad  = map[string]bool{"P1": true, "P2": true, "P3": true}
	backlogDecide     = map[string]bool{"miguel": true, "agente": true}
	backlogItemHeadRe = regexp.MustCompile(`(?m)^### ((?:ENG|SCHEMA|RBAC|OPS|DOC|COMMERCE|SEC|MIG|AUTO|DEC)-[0-9A-Za-z]+|MIG-FRONT) — `)
)

func TestBacklogItemsStayStructured(t *testing.T) {
	root := repoRootForDocs(t)

	raw, err := os.ReadFile(filepath.Join(root, "docs", "backlog", "items.json"))
	if err != nil {
		t.Fatalf("docs/backlog/items.json must exist beside docs/BACKLOG.md: %v", err)
	}
	var f backlogFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("items.json is not valid JSON: %v", err)
	}
	if len(f.Items) == 0 {
		t.Fatal("items.json has zero items — the register cannot be empty while BACKLOG.md has an OPEN section")
	}
	if f.Publicacion.Estado == "" || f.Publicacion.Porque == "" {
		t.Error("items.json: the `publicacion` block must state the publication estado and its porque (the deliberate-pause record)")
	}

	// Every item carries every field the panel orders by, with closed vocabularies.
	seen := map[string]bool{}
	for _, it := range f.Items {
		if it.ID == "" {
			t.Fatalf("an item has no id (titulo=%q)", it.Titulo)
		}
		if seen[it.ID] {
			t.Errorf("%s: duplicated id", it.ID)
		}
		seen[it.ID] = true
		req := map[string]string{
			"titulo": it.Titulo, "que_es": it.QueEs, "por_que_importa": it.PorQueImporta,
			"que_lo_destraba": it.QueLoDestraba, "origen": it.Origen,
		}
		for k, v := range req {
			if strings.TrimSpace(v) == "" {
				t.Errorf("%s: field %q is empty — a pendiente that cannot explain itself goes back to being prose", it.ID, k)
			}
		}
		if !backlogFrentes[it.Frente] {
			t.Errorf("%s: frente %q is not one of the declared fronts", it.ID, it.Frente)
		}
		if !backlogCostos[it.Costo] {
			t.Errorf("%s: costo %q invalid (chico|medio|grande|decision)", it.ID, it.Costo)
		}
		if !backlogDanos[it.Dano] {
			t.Errorf("%s: dano %q invalid (alto|medio|bajo)", it.ID, it.Dano)
		}
		if !backlogPrioridad[it.Prioridad] {
			t.Errorf("%s: prioridad %q invalid (P1|P2|P3)", it.ID, it.Prioridad)
		}
		if !backlogDecide[it.Decide] {
			t.Errorf("%s: decide %q invalid (miguel|agente)", it.ID, it.Decide)
		}
		if it.Estado != "abierto" {
			t.Errorf("%s: estado %q — items.json holds only open items; closed/done move to BACKLOG_ARCHIVO.md and their row is deleted", it.ID, it.Estado)
		}
		if it.DependeDe == nil || it.Bloquea == nil {
			t.Errorf("%s: depende_de and bloquea must be present (empty arrays are fine)", it.ID)
		}
	}
	// References point at live items.
	for _, it := range f.Items {
		for _, ref := range append(append([]string{}, it.DependeDe...), it.Bloquea...) {
			if !seen[ref] {
				t.Errorf("%s: references %q, which is not an open item", it.ID, ref)
			}
		}
	}

	// Both halves hold the same set: every ### <ID> in BACKLOG.md's OPEN
	// section has a JSON row, and every JSON row has its ### in the narrative.
	md, err := os.ReadFile(filepath.Join(root, "docs", "BACKLOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(md)
	if i := strings.Index(body, "\n## OPEN"); i >= 0 {
		body = body[i:]
	} else {
		t.Fatal("BACKLOG.md has no ## OPEN section")
	}
	mdIDs := map[string]bool{}
	for _, m := range backlogItemHeadRe.FindAllStringSubmatch(body, -1) {
		mdIDs[m[1]] = true
	}
	for id := range mdIDs {
		if !seen[id] {
			t.Errorf("BACKLOG.md OPEN has ### %s with no row in items.json — add the structured row (or archive the item)", id)
		}
	}
	for id := range seen {
		if !mdIDs[id] {
			t.Errorf("items.json has %s with no ### heading in BACKLOG.md's OPEN section — the narrative half is missing", id)
		}
	}
}
