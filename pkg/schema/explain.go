package schema

// Explain renders a schema as PLAIN-LANGUAGE prose for the person who OWNS the
// app — not the person who wrote the JSON (PHASE4-FIRST-MILE-S1). Its job is the
// read-back step of the AI authoring loop: a non-programmer describes an app, an
// LLM produces a schema, `appximo validate` answers "may this run?" — and nothing
// answered "did it model what I asked?". This does, deterministically (no AI in
// the loop: what it prints is derived from the parsed schema, never guessed).
//
// lang is "en" (default) or "es" — the owner reads this, so it speaks the
// owner's language even though the product surface is English-first.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// explainPhrases is the sentence bank for one language. Every %s/%d slot is
// documented at the EN instance; ES mirrors it slot for slot.
type explainPhrases struct {
	title           string // app name
	intro           string // n resources, list
	introOne        string // exactly one resource
	resourceHeader  string // resource name
	renamedFrom     string // old name
	fieldRequired   string
	fieldOptional   string
	fieldUnique     string
	fieldEnum       string // allowed values
	fieldDefault    string // default value
	fieldDefaultNow string
	fieldAuto       string
	fieldAutoUpd    string
	fieldRelation   string // target resource
	fieldFile       string
	fieldRange      []string // min only, max only, both
	fieldLen        []string // minLength only, maxLength only, both
	fieldFormat     map[string]string
	fieldPattern    string
	smIntro         string // field name
	smInitial       string // states list
	smMove          string // from, to-list
	smTerminal      string // states list
	relHasMany      string
	relBelongsTo    string
	relManyToMany   string
	eventsLine      string
	importLine      string // roles list, fields list
	hooksLine       string
	rbacHeader      string
	roleFull        string // role with * / *
	roleLine        string // role, actions, resources
	roleCondSelf    string // resource-less phrasing: rows whose <field> is the signed-in user
	roleCondClient  string
	roleCondLiteral string // field, literal
	roleCondActions string // actions the condition gates
	roleFields      string // allowlist
	roleRoutes      string // custom endpoints
	publicLine      string // resource — anyone, without logging in
	publicOnly      string // rows restriction note for public conditions
	denyDefault     string
	noRBAC          string
	eventNames      map[string]string
	footer          string
	actions         map[string]string
	types           map[string]string
}

var explainEN = explainPhrases{
	title:           "%s — what this schema builds",
	intro:           "This API manages %d kinds of records: %s.",
	introOne:        "This API manages one kind of record: %s.",
	resourceHeader:  "%s",
	renamedFrom:     " (renamed from %q)",
	fieldRequired:   "required",
	fieldOptional:   "optional",
	fieldUnique:     "unique — no two records can share the same value",
	fieldEnum:       "one of: %s",
	fieldDefault:    "defaults to %v when not provided",
	fieldDefaultNow: "defaults to the moment the record is created",
	fieldAuto:       "set automatically by the engine when the record is created",
	fieldAutoUpd:    "updated automatically by the engine every time the record changes",
	fieldRelation:   "points at a record of %q",
	fieldFile:       "an uploaded file",
	fieldRange:      []string{"at least %v", "at most %v", "between %v and %v"},
	fieldLen:        []string{"at least %d characters", "at most %d characters", "%d to %d characters"},
	fieldFormat: map[string]string{
		"email": "must be a valid email address",
		"uuid":  "must be a valid UUID",
		"url":   "must be a valid URL",
		"date":  "must be a date (YYYY-MM-DD)",
	},
	fieldPattern:    "must match the pattern %s",
	smIntro:         "Lifecycle of %q:",
	smInitial:       "a new record starts as %s",
	smMove:          "from %q it can move to %s",
	smTerminal:      "%s %s final — once there, the status can never change",
	relHasMany:      "each %s can have many %s (%q)",
	relBelongsTo:    "each %s belongs to a %s (%q)",
	relManyToMany:   "%s and %s are linked many-to-many (%q, via %s)",
	eventsLine:      "Emits an event when a record is %s (for external processing).",
	importLine:      "The role(s) %s may IMPORT records: on creation they may supply the engine-managed field(s) %s (for data migration / restores); everyone else, and every update, gets them set by the engine only.",
	hooksLine:       "Has custom logic hooks on: %s.",
	rbacHeader:      "Who can do what (roles)",
	roleFull:        "%s — full access: every action on every resource.",
	roleLine:        "%s can %s on %s",
	roleCondSelf:    "only rows whose %q is the signed-in user",
	roleCondClient:  "only rows whose %q is the caller's client id",
	roleCondLiteral: "only rows whose %q equals %q",
	roleCondActions: " (the restriction applies to %s; the other actions see everything)",
	roleFields:      "sees only the fields: %s",
	roleRoutes:      "%s can also call the custom endpoint(s): %s",
	publicLine:      "ANYONE on the internet, without logging in, can read %s",
	publicOnly:      "but only rows whose %q equals %q",
	denyDefault:     "Anything not listed above is denied by default.",
	noRBAC:          "No roles are declared: every request will be denied (deny by default). Add an \"rbac\" block before deploying.",
	footer: "This is a literal reading of the schema — nothing here is guessed.\n" +
		"If something above is not what you meant, that is what to change in the schema.\n" +
		"Check correctness with: appximo validate <schema>",
	actions: map[string]string{
		"read": "read", "create": "create", "update": "update", "delete": "delete", "*": "do everything",
	},
	eventNames: map[string]string{"create": "created", "update": "updated", "delete": "deleted"},
	types: map[string]string{
		"string": "text", "text": "long text", "int": "whole number", "int64": "whole number",
		"float64": "decimal number", "bool": "yes/no", "time": "date & time", "uuid": "unique id",
		"json": "structured data (JSON, stored as text)", "jsonb": "structured data (JSON, queryable)",
		"file": "attached file",
	},
}

var explainES = explainPhrases{
	title:           "%s — qué construye este schema",
	intro:           "Esta API maneja %d tipos de registros: %s.",
	introOne:        "Esta API maneja un tipo de registro: %s.",
	resourceHeader:  "%s",
	renamedFrom:     " (renombrado desde %q)",
	fieldRequired:   "obligatorio",
	fieldOptional:   "opcional",
	fieldUnique:     "único — no puede haber dos registros con el mismo valor",
	fieldEnum:       "uno de: %s",
	fieldDefault:    "si no se envía, queda en %v",
	fieldDefaultNow: "si no se envía, queda el momento de creación",
	fieldAuto:       "lo llena el motor automáticamente al crear el registro",
	fieldAutoUpd:    "el motor lo actualiza automáticamente cada vez que el registro cambia",
	fieldRelation:   "apunta a un registro de %q",
	fieldFile:       "un archivo subido",
	fieldRange:      []string{"mínimo %v", "máximo %v", "entre %v y %v"},
	fieldLen:        []string{"mínimo %d caracteres", "máximo %d caracteres", "de %d a %d caracteres"},
	fieldFormat: map[string]string{
		"email": "debe ser un email válido",
		"uuid":  "debe ser un UUID válido",
		"url":   "debe ser una URL válida",
		"date":  "debe ser una fecha (AAAA-MM-DD)",
	},
	fieldPattern:    "debe cumplir el patrón %s",
	smIntro:         "Ciclo de vida de %q:",
	smInitial:       "un registro nuevo empieza en %s",
	smMove:          "de %q puede pasar a %s",
	smTerminal:      "%s %s final — una vez ahí, el estado ya no puede cambiar",
	relHasMany:      "cada %s puede tener muchos %s (%q)",
	relBelongsTo:    "cada %s pertenece a un %s (%q)",
	relManyToMany:   "%s y %s se relacionan muchos-a-muchos (%q, vía %s)",
	eventsLine:      "Emite un evento cuando un registro se %s (para procesamiento externo).",
	importLine:      "El/los rol(es) %s pueden IMPORTAR registros: al crear pueden traer el/los campo(s) que maneja el motor %s (para migración de datos / restauraciones); todos los demás, y toda modificación, los reciben solo del motor.",
	hooksLine:       "Tiene lógica custom (hooks) en: %s.",
	rbacHeader:      "Quién puede hacer qué (roles)",
	roleFull:        "%s — acceso total: toda acción sobre todo recurso.",
	roleLine:        "%s puede %s en %s",
	roleCondSelf:    "solo las filas cuyo %q es el usuario que inició sesión",
	roleCondClient:  "solo las filas cuyo %q es el client id del que llama",
	roleCondLiteral: "solo las filas cuyo %q vale %q",
	roleCondActions: " (la restricción aplica a %s; las demás acciones ven todo)",
	roleFields:      "ve solo los campos: %s",
	roleRoutes:      "%s además puede llamar el/los endpoint(s) custom: %s",
	publicLine:      "CUALQUIERA en internet, sin iniciar sesión, puede leer %s",
	publicOnly:      "pero solo las filas cuyo %q vale %q",
	denyDefault:     "Todo lo que no está listado arriba se niega por defecto.",
	noRBAC:          "No hay roles declarados: toda petición será negada (deny by default). Agregá un bloque \"rbac\" antes de desplegar.",
	footer: "Esto es una lectura literal del schema — nada de lo de arriba es adivinado.\n" +
		"Si algo no es lo que pediste, eso es lo que hay que cambiar en el schema.\n" +
		"Verificá la corrección con: appximo validate <schema>",
	actions: map[string]string{
		"read": "leer", "create": "crear", "update": "modificar", "delete": "borrar", "*": "hacer todo",
	},
	eventNames: map[string]string{"create": "crea", "update": "modifica", "delete": "borra"},
	types: map[string]string{
		"string": "texto", "text": "texto largo", "int": "número entero", "int64": "número entero",
		"float64": "número decimal", "bool": "sí/no", "time": "fecha y hora", "uuid": "id único",
		"json": "datos estructurados (JSON, guardado como texto)", "jsonb": "datos estructurados (JSON, consultable)",
		"file": "archivo adjunto",
	},
}

// Explain renders the schema as plain-language prose. lang: "en" | "es"
// (anything else falls back to "en").
func Explain(s *APISchema, lang string) string {
	p := explainEN
	esList := " y "
	if strings.EqualFold(lang, "es") {
		p = explainES
	} else {
		esList = " and "
	}

	var b strings.Builder
	name := s.Name
	if name == "" {
		name = "api"
	}
	title := fmt.Sprintf(p.title, name)
	b.WriteString(title + "\n" + strings.Repeat("=", len([]rune(title))) + "\n\n")

	resNames := sortedResourceNames(s)
	if len(resNames) == 1 {
		b.WriteString(fmt.Sprintf(p.introOne, resNames[0]) + "\n\n")
	} else {
		b.WriteString(fmt.Sprintf(p.intro, len(resNames), joinList(resNames, esList)) + "\n\n")
	}

	for _, rn := range resNames {
		r := s.Resources[rn]
		b.WriteString("■ " + fmt.Sprintf(p.resourceHeader, rn))
		if r.RenamedFrom != "" {
			b.WriteString(fmt.Sprintf(p.renamedFrom, r.RenamedFrom))
		}
		b.WriteString("\n")
		explainFields(&b, r, p)
		explainStateMachines(&b, r, p, esList)
		explainRelations(&b, rn, r, p)
		if r.Import != nil {
			b.WriteString("  " + fmt.Sprintf(p.importLine, joinList(quoteAll(r.Import.Roles), esList), joinList(quoteAll(r.ImportDeclaredFields()), esList)) + "\n")
		}
		if len(r.Events) > 0 {
			ev := make([]string, 0, len(r.Events))
			for _, a := range r.Events {
				if t, ok := p.eventNames[a]; ok {
					ev = append(ev, t)
				} else {
					ev = append(ev, a)
				}
			}
			b.WriteString("  " + fmt.Sprintf(p.eventsLine, joinList(ev, esList)) + "\n")
		}
		if len(r.Hooks) > 0 {
			hooks := make([]string, 0, len(r.Hooks))
			for h := range r.Hooks {
				hooks = append(hooks, h)
			}
			sort.Strings(hooks)
			b.WriteString("  " + fmt.Sprintf(p.hooksLine, strings.Join(hooks, ", ")) + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString(p.rbacHeader + "\n" + strings.Repeat("-", len([]rune(p.rbacHeader))) + "\n")
	explainRBAC(&b, s, p, esList)
	b.WriteString("\n" + p.footer + "\n")
	return b.String()
}

func sortedResourceNames(s *APISchema) []string {
	names := make([]string, 0, len(s.Resources))
	for n := range s.Resources {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func joinList(items []string, and string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + and + items[len(items)-1]
}

func joinActionList(actions []string, p explainPhrases, and string) string {
	out := make([]string, 0, len(actions))
	for _, a := range actions {
		if t, ok := p.actions[a]; ok {
			out = append(out, t)
		} else {
			out = append(out, a)
		}
	}
	return joinList(out, and)
}

func explainFields(b *strings.Builder, r ResourceSchema, p explainPhrases) {
	names := make([]string, 0, len(r.Fields))
	for n := range r.Fields {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, fn := range names {
		f := r.Fields[fn]
		parts := []string{}
		if t, ok := p.types[f.Type]; ok {
			parts = append(parts, t)
		} else {
			parts = append(parts, f.Type)
		}
		if f.Auto.Enabled() {
			if f.Auto.RefreshesOnUpdate(fn) {
				parts = append(parts, p.fieldAutoUpd)
			} else {
				parts = append(parts, p.fieldAuto)
			}
		} else if f.Required {
			parts = append(parts, p.fieldRequired)
		} else {
			parts = append(parts, p.fieldOptional)
		}
		if f.Unique {
			parts = append(parts, p.fieldUnique)
		}
		if len(f.Enum) > 0 {
			parts = append(parts, fmt.Sprintf(p.fieldEnum, quoteJoin(f.Enum)))
		}
		if f.Relation != "" {
			parts = append(parts, fmt.Sprintf(p.fieldRelation, f.Relation))
		}
		if f.Default != nil {
			if f.Type == "time" && f.Default == "now" {
				parts = append(parts, p.fieldDefaultNow)
			} else {
				parts = append(parts, fmt.Sprintf(p.fieldDefault, f.Default))
			}
		}
		switch {
		case f.Min != nil && f.Max != nil:
			parts = append(parts, fmt.Sprintf(p.fieldRange[2], trimFloat(*f.Min), trimFloat(*f.Max)))
		case f.Min != nil:
			parts = append(parts, fmt.Sprintf(p.fieldRange[0], trimFloat(*f.Min)))
		case f.Max != nil:
			parts = append(parts, fmt.Sprintf(p.fieldRange[1], trimFloat(*f.Max)))
		}
		switch {
		case f.MinLength != nil && f.MaxLength != nil:
			parts = append(parts, fmt.Sprintf(p.fieldLen[2], *f.MinLength, *f.MaxLength))
		case f.MinLength != nil:
			parts = append(parts, fmt.Sprintf(p.fieldLen[0], *f.MinLength))
		case f.MaxLength != nil:
			parts = append(parts, fmt.Sprintf(p.fieldLen[1], *f.MaxLength))
		}
		if f.Format != "" {
			if t, ok := p.fieldFormat[f.Format]; ok {
				parts = append(parts, t)
			}
		}
		if f.Pattern != "" {
			parts = append(parts, fmt.Sprintf(p.fieldPattern, f.Pattern))
		}
		fmt.Fprintf(b, "  • %s — %s\n", fn, strings.Join(parts, ", "))
	}
}

// flowOrder walks a state machine breadth-first from its initial states, so a
// lifecycle prints the way it runs. Unreachable transition sources follow,
// alphabetically (deterministic).
func flowOrder(sm *StateMachine) []string {
	seen := map[string]bool{}
	var order []string
	queue := append([]string{}, sm.Initial...)
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		if seen[s] {
			continue
		}
		seen[s] = true
		order = append(order, s)
		queue = append(queue, sm.Transitions[s]...)
	}
	rest := make([]string, 0)
	for f := range sm.Transitions {
		if !seen[f] {
			rest = append(rest, f)
		}
	}
	sort.Strings(rest)
	return append(order, rest...)
}

func explainStateMachines(b *strings.Builder, r ResourceSchema, p explainPhrases, and string) {
	names := make([]string, 0, len(r.Fields))
	for n := range r.Fields {
		if r.Fields[n].StateMachine != nil {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, fn := range names {
		sm := r.Fields[fn].StateMachine
		b.WriteString("  " + fmt.Sprintf(p.smIntro, fn) + "\n")
		b.WriteString("    - " + fmt.Sprintf(p.smInitial, quoteJoin(sm.Initial)) + "\n")
		// S3 (FIELD-FEEDBACK-S1): FLOW order, not alphabetical — the command
		// exists so a non-programmer can review a lifecycle, and the natural
		// reading is breadth-first from the initial states (the evaluator's
		// 5-state machine printed its initial state third). States unreachable
		// from initial (legal but odd) follow alphabetically; terminals keep
		// their grouped line at the end.
		froms := flowOrder(sm)
		var terminal []string
		for _, from := range froms {
			tos := sm.Transitions[from]
			if len(tos) == 0 {
				terminal = append(terminal, from)
				continue
			}
			b.WriteString("    - " + fmt.Sprintf(p.smMove, from, quoteJoin(tos)) + "\n")
		}
		if len(terminal) > 0 {
			// EN "is/are", ES "es/son" — the second %s slot of smTerminal.
			isAre := "is"
			if p.smIntro == explainES.smIntro {
				isAre = "es"
				if len(terminal) > 1 {
					isAre = "son"
				}
			} else if len(terminal) > 1 {
				isAre = "are"
			}
			b.WriteString("    - " + fmt.Sprintf(p.smTerminal, joinList(quoteEach(terminal), and), isAre) + "\n")
		}
	}
}

func explainRelations(b *strings.Builder, rn string, r ResourceSchema, p explainPhrases) {
	names := make([]string, 0, len(r.Relations))
	for n := range r.Relations {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, relName := range names {
		rel := r.Relations[relName]
		var line string
		switch rel.Type {
		case RelationHasMany:
			line = fmt.Sprintf(p.relHasMany, rn, rel.Target, relName)
		case RelationBelongsTo:
			line = fmt.Sprintf(p.relBelongsTo, rn, rel.Target, relName)
		case RelationManyToMany:
			line = fmt.Sprintf(p.relManyToMany, rn, rel.Target, relName, rel.Through)
		default:
			continue
		}
		b.WriteString("  ↔ " + line + "\n")
	}
}

func explainRBAC(b *strings.Builder, s *APISchema, p explainPhrases, and string) {
	if len(s.RBAC.Roles) == 0 && len(s.RBAC.Public) == 0 {
		b.WriteString(p.noRBAC + "\n")
		return
	}
	// ENG-40: the public block FIRST — the owner reviewing an AI-written
	// schema most needs to hear what the whole internet can read, before any
	// role detail. Worded as a warning-grade fact, never softened.
	if len(s.RBAC.Public) > 0 {
		pubs := make([]string, 0, len(s.RBAC.Public))
		for res := range s.RBAC.Public {
			pubs = append(pubs, res)
		}
		sort.Strings(pubs)
		for _, res := range pubs {
			g := s.RBAC.Public[res]
			line := "• " + fmt.Sprintf(p.publicLine, res)
			if g.Conditions != nil {
				line += " — " + fmt.Sprintf(p.publicOnly, g.Conditions.Field, g.Conditions.Val)
			}
			if len(g.Fields) > 0 {
				line += "; " + fmt.Sprintf(p.roleFields, strings.Join(g.Fields, ", "))
			}
			b.WriteString(line + "\n")
		}
	}
	roles := make([]string, 0, len(s.RBAC.Roles))
	for r := range s.RBAC.Roles {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	for _, roleName := range roles {
		role := s.RBAC.Roles[roleName]
		if len(role.Permissions) > 0 {
			perms := make([]string, 0, len(role.Permissions))
			for res := range role.Permissions {
				perms = append(perms, res)
			}
			sort.Strings(perms)
			for _, res := range perms {
				g := role.Permissions[res]
				line := "• " + fmt.Sprintf(p.roleLine, roleName, joinActionList(g.Actions, p, and), res)
				if cl := describeCondition(g.Conditions, p); cl != "" {
					line += " — " + cl
					if len(g.ConditionActions) > 0 {
						line += fmt.Sprintf(p.roleCondActions, joinActionList(g.ConditionActions, p, and))
					}
				}
				if len(g.Fields) > 0 {
					line += "; " + fmt.Sprintf(p.roleFields, strings.Join(g.Fields, ", "))
				}
				b.WriteString(line + "\n")
			}
		} else {
			resources := applicableResources(role.Resources, s)
			sort.Strings(resources)
			wildcardRes := isWildcardResources(role.Resources)
			wildcardAct := len(role.Actions) == 1 && role.Actions[0] == "*"
			if wildcardRes && wildcardAct && role.Conditions == nil && len(role.Fields) == 0 {
				b.WriteString("• " + fmt.Sprintf(p.roleFull, roleName) + "\n")
			} else if wildcardRes || len(resources) > 0 {
				target := joinList(resources, and)
				if wildcardRes {
					target = "*"
				}
				line := "• " + fmt.Sprintf(p.roleLine, roleName, joinActionList(role.Actions, p, and), target)
				if cl := describeCondition(role.Conditions, p); cl != "" {
					line += " — " + cl
				}
				if len(role.Fields) > 0 {
					line += "; " + fmt.Sprintf(p.roleFields, strings.Join(role.Fields, ", "))
				}
				b.WriteString(line + "\n")
			}
		}
		if len(role.Routes) > 0 {
			segs := make([]string, 0, len(role.Routes))
			for seg := range role.Routes {
				segs = append(segs, "/api/"+seg)
			}
			sort.Strings(segs)
			b.WriteString("  " + fmt.Sprintf(p.roleRoutes, roleName, strings.Join(segs, ", ")) + "\n")
		}
	}
	b.WriteString(p.denyDefault + "\n")
}

func describeCondition(c *Condition, p explainPhrases) string {
	if c == nil {
		return ""
	}
	switch c.Val {
	case "$user_id":
		return fmt.Sprintf(p.roleCondSelf, c.Field)
	case "$external_client_id":
		return fmt.Sprintf(p.roleCondClient, c.Field)
	default:
		return fmt.Sprintf(p.roleCondLiteral, c.Field, c.Val)
	}
}

func isWildcardResources(raw json.RawMessage) bool {
	var s string
	return json.Unmarshal(raw, &s) == nil && s == "*"
}

func quoteJoin(items []string) string { return strings.Join(quoteEach(items), ", ") }

func quoteEach(items []string) []string {
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

func trimFloat(f float64) any {
	if f == float64(int64(f)) {
		return int64(f)
	}
	return f
}

// quoteAll wraps each entry in double quotes for prose lists.
func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}
