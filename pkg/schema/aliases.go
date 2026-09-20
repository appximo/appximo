package schema

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Aliases (VOZ-AHORRO-S2, ADR-038): how PEOPLE name the things a developer
// named. «pedidos» and «ventas» are `ordenes`; «mascotas» is `pets`; «sin
// pagar» is `estado = pendiente_pago`. The engine wires no word of any
// domain — the schema DECLARES them, and the voice parser (pkg/ask) reads
// them like any other schema word: if the schema does not declare it, the
// parser does not know it, and that is correct.
//
// This file owns the ONE normalization and the ONE set of name forms both
// the validator and the parser use, so "unique at load" and "recognized at
// runtime" are the same predicate: an alias the validator accepted is an
// alias the parser resolves to exactly one thing.

// NormalizeText lowercases, strips accents (ñ → n, as dictation writes both),
// turns punctuation into spaces and collapses whitespace. The parser's word
// normalization; a schema alias is compared in this form.
func NormalizeText(s string) string {
	out, _, err := transform.String(accentStripper, strings.ToLower(s))
	if err != nil {
		out = strings.ToLower(s)
	}
	var b strings.Builder
	space := true
	for _, r := range out {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			space = false
		default:
			if !space {
				b.WriteByte(' ')
				space = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

var accentStripper = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

// SingularES strips a Spanish plural suffix from a normalized word
// ("ordenes" → "orden", "clientes" → "cliente", "pets" → "pet").
func SingularES(w string) string {
	switch {
	case strings.HasSuffix(w, "ones"), strings.HasSuffix(w, "enes"), strings.HasSuffix(w, "ores"), strings.HasSuffix(w, "ales"), strings.HasSuffix(w, "iles"), strings.HasSuffix(w, "ades"):
		return strings.TrimSuffix(w, "es")
	case strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && len(w) > 3:
		return strings.TrimSuffix(w, "s")
	}
	return w
}

// NameForms derives the forms a resource (or a resource alias) is named by:
// the normalized name with underscores as spaces, its singular, and its
// plurals. A multi-word name is inflected on its LAST word only ("orden
// lineas" → "orden linea"); the parser matches these forms whole.
func NameForms(name string) []string {
	base := strings.ReplaceAll(NormalizeText(name), "_", " ")
	if base == "" {
		return nil
	}
	forms := []string{base}
	add := func(s string) {
		if s != "" && !containsString(forms, s) {
			forms = append(forms, s)
		}
	}
	add(SingularES(base))
	add(base + "s")
	add(base + "es")
	return forms
}

// ValueForms derives the forms an enum value (or a value alias) is said in:
// the normalized value with underscores as spaces, its plurals, and — for a
// multi-word value — the "de" variant («pendiente de pago» ≡ pendiente_pago).
func ValueForms(val string) []string {
	form := strings.ReplaceAll(NormalizeText(val), "_", " ")
	if form == "" {
		return nil
	}
	if strings.Contains(form, " ") {
		de := strings.ReplaceAll(form, " ", " de ")
		return uniqueStrings([]string{form, form + "s", de, de + "s"})
	}
	forms := []string{form, form + "s", form + "es"}
	// Spanish gender agreement: «pedidos cancelados» names the state
	// `cancelada` — a single-word form ending in -a/-o also matches the other
	// gender's singular and plural.
	switch {
	case strings.HasSuffix(form, "a") && len(form) > 2:
		o := strings.TrimSuffix(form, "a") + "o"
		forms = append(forms, o, o+"s")
	case strings.HasSuffix(form, "o") && len(form) > 2:
		a := strings.TrimSuffix(form, "o") + "a"
		forms = append(forms, a, a+"s")
	}
	return uniqueStrings(forms)
}

func uniqueStrings(in []string) []string {
	var out []string
	for _, s := range in {
		if !containsString(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func containsString(set []string, s string) bool {
	for _, x := range set {
		if x == s {
			return true
		}
	}
	return false
}

// MaxAliasLength bounds one alias (runes, as declared).
const MaxAliasLength = 40

// aliasOwner records who already claims a normalized form, for the
// uniqueness verdicts.
type aliasOwner struct {
	kind  string // "resource" | "resource alias" | "value" | "value alias"
	where string // "ordenes" | "ordenes.estado = pendiente_pago"
}

// validateAliases enforces the alias contract at load. The rule in one
// sentence: every alias must mean exactly ONE thing in the whole schema. A
// resource alias must not be any resource's name or plural, another
// resource's alias, or any declared value / value alias anywhere; a value
// alias must name a declared member of its field's enum, and must not be a
// declared value or another alias of the SAME resource, nor a resource name
// or resource alias. Value aliases MAY repeat across resources («sin pagar»
// for both orders and invoices is a state of each): the parser scopes them
// by the resource the sentence names.
func validateAliases(s *APISchema) []ValidationError {
	var errs []ValidationError
	resNames := make([]string, 0, len(s.Resources))
	for n := range s.Resources {
		resNames = append(resNames, n)
	}
	sort.Strings(resNames)

	// The global claims: resource name forms, resource alias forms, and every
	// value form / value alias form (with its resource, for the cross-resource
	// exception).
	global := map[string][]aliasOwner{}
	claim := func(form string, o aliasOwner) { global[form] = append(global[form], o) }
	for _, rn := range resNames {
		for _, f := range NameForms(rn) {
			claim(f, aliasOwner{kind: "resource", where: rn})
		}
		r := s.Resources[rn]
		for _, fn := range sortedFieldNames(r) {
			fd := r.Fields[fn]
			for _, val := range fd.Enum {
				for _, f := range ValueForms(val) {
					claim(f, aliasOwner{kind: "value", where: rn + "." + fn + " = " + val})
				}
			}
		}
	}

	// Resource aliases.
	for _, rn := range resNames {
		r := s.Resources[rn]
		if r.Aliases == nil {
			continue
		}
		prefix := "resources." + rn + ".aliases"
		if len(r.Aliases) == 0 {
			errs = append(errs, ValidationError{Field: prefix, Rule: "alias_empty_list",
				Message: fmt.Sprintf("%q declares an empty aliases list (dead config)", rn),
				Fix:     "list the words people use for this resource, or remove the key"})
			continue
		}
		seen := map[string]string{}
		for _, a := range r.Aliases {
			n, verr := checkAliasText(prefix, a)
			if verr != nil {
				errs = append(errs, *verr)
				continue
			}
			if verr := resourceAliasCollision(prefix, rn, a, n, seen, global); verr != nil {
				errs = append(errs, *verr)
				continue
			}
			for _, f := range NameForms(n) {
				seen[f] = n
				claim(f, aliasOwner{kind: "resource alias", where: rn})
			}
		}
	}

	// Value aliases.
	for _, rn := range resNames {
		r := s.Resources[rn]
		// Per-resource claims: every value form of every enum field of this
		// resource, then the aliases as they are accepted.
		local := map[string]string{}
		for _, fn := range sortedFieldNames(r) {
			for _, val := range r.Fields[fn].Enum {
				for _, f := range ValueForms(val) {
					local[f] = fn + " = " + val
				}
			}
		}
		for _, fn := range sortedFieldNames(r) {
			fd := r.Fields[fn]
			if fd.Aliases == nil {
				continue
			}
			prefix := "resources." + rn + ".fields." + fn + ".aliases"
			if len(fd.Enum) == 0 {
				errs = append(errs, ValidationError{Field: prefix, Rule: "alias_needs_enum",
					Message: fmt.Sprintf("%s.%s declares value aliases but has no enum — aliases name declared values", rn, fn),
					Fix:     "declare the enum first, or remove the aliases"})
				continue
			}
			if len(fd.Aliases) == 0 {
				errs = append(errs, ValidationError{Field: prefix, Rule: "alias_empty_list",
					Message: fmt.Sprintf("%s.%s declares an empty aliases map (dead config)", rn, fn),
					Fix:     "map each value to the words people say for it, or remove the key"})
				continue
			}
			keys := make([]string, 0, len(fd.Aliases))
			for k := range fd.Aliases {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, val := range keys {
				if !containsString(fd.Enum, val) {
					errs = append(errs, ValidationError{Field: prefix + "." + val, Rule: "alias_unknown_value", Got: val, Expected: fd.Enum,
						Message: fmt.Sprintf("%s.%s has no value %q (its values are: %s) — an alias must name a declared value", rn, fn, val, joinQuoted(fd.Enum)),
						Fix:     "use one of the declared enum values as the key"})
					continue
				}
				if len(fd.Aliases[val]) == 0 {
					errs = append(errs, ValidationError{Field: prefix + "." + val, Rule: "alias_empty_list",
						Message: fmt.Sprintf("%s.%s value %q has an empty aliases list (dead config)", rn, fn, val),
						Fix:     "list the words people say for this value, or remove the key"})
					continue
				}
				for _, a := range fd.Aliases[val] {
					n, verr := checkAliasText(prefix+"."+val, a)
					if verr != nil {
						errs = append(errs, *verr)
						continue
					}
					if verr := valueAliasCollision(prefix+"."+val, rn, fn, val, a, n, local, global); verr != nil {
						errs = append(errs, *verr)
						continue
					}
					for _, f := range ValueForms(n) {
						local[f] = fn + " = " + val + " (alias " + a + ")"
						claim(f, aliasOwner{kind: "value alias", where: rn + "." + fn + " = " + val})
					}
				}
			}
		}
	}
	return errs
}

// resourceAliasCollision is the uniqueness verdict for one resource alias.
func resourceAliasCollision(prefix, rn, a, n string, seen map[string]string, global map[string][]aliasOwner) *ValidationError {
	for _, f := range NameForms(n) {
		if prev, dup := seen[f]; dup && prev != n {
			return &ValidationError{Field: prefix, Rule: "alias_duplicate", Got: a,
				Message: fmt.Sprintf("alias %q of %q repeats %q (same word after normalization)", a, rn, prev),
				Fix:     "declare each word once"}
		}
	}
	for _, f := range NameForms(n) {
		for _, o := range global[f] {
			switch o.kind {
			case "resource":
				return &ValidationError{Field: prefix, Rule: "alias_is_resource_name", Got: a,
					Message: fmt.Sprintf("alias %q of %q is already how the resource %q is named (a schema name, singular or plural, needs no alias)", a, rn, o.where),
					Fix:     "remove the alias, or rename one of the two resources"}
			case "resource alias":
				if o.where != rn {
					return &ValidationError{Field: prefix, Rule: "alias_ambiguous", Got: a,
						Message: fmt.Sprintf("alias %q is declared by BOTH %q and %q — the voice parser could not tell which resource is meant, so it would always fall back to the model", a, rn, o.where),
						Fix:     "keep the alias on the one resource people mean by it"}
				}
			case "value", "value alias":
				return &ValidationError{Field: prefix, Rule: "alias_is_value", Got: a,
					Message: fmt.Sprintf("alias %q of %q is also a declared value (%s) — a word cannot mean a resource and a state at once", a, rn, o.where),
					Fix:     "choose a different word for the resource"}
			}
		}
	}
	return nil
}

// valueAliasCollision is the uniqueness verdict for one value alias.
func valueAliasCollision(prefix, rn, fn, val, a, n string, local map[string]string, global map[string][]aliasOwner) *ValidationError {
	for _, f := range ValueForms(n) {
		if owner, dup := local[f]; dup {
			if owner == fn+" = "+val {
				return &ValidationError{Field: prefix, Rule: "alias_is_value", Got: a,
					Message: fmt.Sprintf("alias %q of %s.%s = %q is the value's own form (a declared value needs no alias)", a, rn, fn, val),
					Fix:     "remove it"}
			}
			return &ValidationError{Field: prefix, Rule: "alias_duplicate", Got: a,
				Message: fmt.Sprintf("alias %q of %s.%s = %q already means %s in %q — a word cannot mean two values of one resource", a, rn, fn, val, owner, rn),
				Fix:     "keep the word on the one value people mean by it"}
		}
	}
	for _, f := range ValueForms(n) {
		for _, o := range global[f] {
			if o.kind == "resource" || o.kind == "resource alias" {
				return &ValidationError{Field: prefix, Rule: "alias_is_resource_name", Got: a,
					Message: fmt.Sprintf("alias %q of %s.%s = %q is also how the resource %q is named — a word cannot mean a resource and a state at once", a, rn, fn, val, o.where),
					Fix:     "choose a different word for the value"}
			}
		}
	}
	return nil
}

// checkAliasText validates one alias literal: non-empty after normalization,
// bounded, letters/digits/spaces only. Returns the normalized form.
func checkAliasText(prefix, a string) (string, *ValidationError) {
	n := NormalizeText(a)
	switch {
	case n == "":
		return "", &ValidationError{Field: prefix, Rule: "alias_empty", Got: a,
			Message: fmt.Sprintf("alias %q is empty after normalization (letters and digits only)", a),
			Fix:     "use a word people actually say"}
	case len([]rune(a)) > MaxAliasLength:
		return "", &ValidationError{Field: prefix, Rule: "alias_too_long", Got: a,
			Message: fmt.Sprintf("alias %q is longer than %d characters", a, MaxAliasLength),
			Fix:     "aliases are the short words people use, not sentences"}
	case len(strings.Fields(n)) > 4:
		return "", &ValidationError{Field: prefix, Rule: "alias_too_long", Got: a,
			Message: fmt.Sprintf("alias %q has more than four words", a),
			Fix:     "aliases are the short words people use, not sentences"}
	}
	return n, nil
}

func sortedFieldNames(r ResourceSchema) []string {
	names := make([]string, 0, len(r.Fields))
	for n := range r.Fields {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
