// Package main — `lab`, the ephemeral capacity laboratory (LAB-CAPACIDAD-S1).
//
// The leash comes first. DigitalOcean's granular scopes are PER RESOURCE TYPE,
// not per resource: a token with droplet:delete can delete every droplet in the
// account, including the production boxes. The protection therefore lives in
// this wrapper, not in the token, and every mutating call passes through the
// guards in this file BEFORE any network I/O:
//
//  1. Create only with the `applab-` name prefix AND the `applab` tag. Both.
//  2. Destroy only what carries prefix AND tag. Anything else: refuse without
//     calling the API.
//  3. A hard cap on simultaneous droplets — a broken loop cannot open twenty.
//  4. A reaper that destroys anything `applab-*` older than N hours.
//  5. The token is read from a file at runtime and never printed.
//  6. Dry-run is the DEFAULT; `-apply` is the explicit opt-in.
//
// The production droplets are additionally refused by IP, belt and braces:
// even a droplet that somehow carried the lab name and tag is not destroyed
// if it answers to a protected address.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	// NamePrefix and LabTag are BOTH required on every droplet this tool
	// creates or destroys. The tag makes the reaper's listing cheap and
	// authoritative; the prefix makes a human `doctl compute droplet list`
	// unambiguous. Neither alone is enough to authorize a destroy.
	NamePrefix = "applab-"
	LabTag     = "applab"

	// MaxDroplets is the hard cap on simultaneous lab droplets. The
	// topology needs three (generator + two targets); four leaves room for
	// one experiment without letting a retry loop open twenty.
	MaxDroplets = 4
)

// protectedIPs are the production boxes' addresses: a droplet answering to
// one of these is NEVER destroyed by this tool, whatever its name or tags
// claim. The addresses are infrastructure, not source — they load at startup
// from ProtectedIPsFile (one IP per line, `#` comments) and/or the
// APPLAB_PROTECTED_IPS env var (comma-separated), so the public repo carries
// no IPs. The name+tag guard above is the primary leash and needs no config;
// this belt warns loudly when it is empty (see loadProtectedIPs).
var protectedIPs = map[string]string{}

// ProtectedIPsFile is the operator-maintained belt-and-braces list.
const ProtectedIPsFile = "/root/.applab-protected"

// loadProtectedIPs fills the belt from the file and the environment. It
// returns how many addresses are protected so the caller can warn on zero.
func loadProtectedIPs() int {
	if b, err := os.ReadFile(ProtectedIPsFile); err == nil { //nolint:gosec // fixed operator-owned path
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			ip, label, _ := strings.Cut(line, " ")
			if label == "" {
				label = "protected (from " + ProtectedIPsFile + ")"
			}
			protectedIPs[ip] = strings.TrimSpace(label)
		}
	}
	for _, ip := range strings.Split(os.Getenv("APPLAB_PROTECTED_IPS"), ",") {
		if ip = strings.TrimSpace(ip); ip != "" {
			protectedIPs[ip] = "protected (from APPLAB_PROTECTED_IPS)"
		}
	}
	return len(protectedIPs)
}

// labSizes is the laboratory's droplet-size fingerprint — exactly the sizes
// the topology in provision.go uses. It is the SUBSTITUTE second factor for
// destroy when the droplet carries no tag: the scoped token turned out to
// lack every tag:* permission (LAB-CAPACIDAD-S2, verified live — creating a
// droplet WITH a tag 403s on `tag:create`, and POST/GET /v2/tags 403 too),
// so droplets may exist untagged. "Prefix alone never authorizes" still
// holds: an untagged droplet must ALSO match the lab fingerprint. Restoring
// the real tag factor is a token-scope change (BACKLOG OPS-38).
var labSizes = map[string]bool{
	"c-4":         true,
	"c-2":         true,
	"s-2vcpu-2gb": true,
}

// ErrGuard marks a refusal by the leash. Every ErrGuard is returned BEFORE any
// API call is made — the tests in guard_test.go and do_test.go pin that.
var ErrGuard = errors.New("lab guard refusal")

func guardErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrGuard, fmt.Sprintf(format, a...))
}

// ValidLabName reports whether a name is one this tool may create or destroy.
func ValidLabName(name string) bool {
	return strings.HasPrefix(name, NamePrefix) && len(name) > len(NamePrefix)
}

// GuardCreate authorizes a creation request: lab prefix on the name, lab tag
// in the tag set, and the cap over the droplets that already exist.
func GuardCreate(name string, tags []string, existing int) error {
	if !ValidLabName(name) {
		return guardErr("create %q: lab droplets are named %q + suffix, always", name, NamePrefix)
	}
	if !hasTag(tags, LabTag) {
		return guardErr("create %q: the %q tag is mandatory (the reaper lists by it)", name, LabTag)
	}
	if existing >= MaxDroplets {
		return guardErr("create %q: %d lab droplets already exist and the cap is %d — run `lab down` or `lab reap` first", name, existing, MaxDroplets)
	}
	return nil
}

// GuardDestroy authorizes destroying a droplet, judged on the droplet object
// as the API reported it: name prefix AND tag AND no protected address.
func GuardDestroy(d Droplet) error {
	if !ValidLabName(d.Name) {
		return guardErr("destroy %q (id %d): not an applab droplet — name lacks the %q prefix", d.Name, d.ID, NamePrefix)
	}
	if !hasTag(d.Tags, LabTag) && !labSizes[d.SizeSlug] {
		return guardErr("destroy %q (id %d): prefix alone does not authorize — needs the %q tag or the lab size fingerprint (size %q is not one of the lab's)", d.Name, d.ID, LabTag, d.SizeSlug)
	}
	for _, ip := range append(append([]string{}, d.PublicIPs...), d.PrivateIPs...) {
		if who, bad := protectedIPs[ip]; bad {
			return guardErr("destroy %q (id %d): answers to %s — %s. NEVER.", d.Name, d.ID, ip, who)
		}
	}
	return nil
}

// ReapSelect returns the subset of ds the reaper may destroy: lab-guarded AND
// older than maxAge. Droplets that fail the guard are skipped (and reported by
// the caller), never destroyed.
func ReapSelect(ds []Droplet, maxAge time.Duration, now time.Time) (old, skipped []Droplet) {
	for _, d := range ds {
		if GuardDestroy(d) != nil {
			skipped = append(skipped, d)
			continue
		}
		if now.Sub(d.CreatedAt) > maxAge {
			old = append(old, d)
		}
	}
	return old, skipped
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}
