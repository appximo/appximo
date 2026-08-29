package main

// A minimal DigitalOcean API v2 client — standard library only, so the guard
// tests can prove "refused WITHOUT touching the network" against a recording
// transport instead of trusting a vendored SDK.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// TokenPath is where Miguel places the lab token (custom scopes:
// droplet:create/read/delete, ssh_key:read, vpc:read). The tool reads it at
// runtime; it is never echoed, logged, committed or included in an error.
const TokenPath = "/root/.do-lab-token"

const apiBase = "https://api.digitalocean.com"

// LoadToken reads the token file. The error names the PATH, never content.
func LoadToken(path string) (string, error) {
	b, err := os.ReadFile(path) //nolint:gosec // fixed operator-owned path
	if err != nil {
		return "", fmt.Errorf("lab token not readable at %s — Miguel creates it (custom-scoped DO token, mode 600); the lab never rotates or prints it: %w", path, err)
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", fmt.Errorf("lab token file %s is empty", path)
	}
	return tok, nil
}

// Droplet is the slice of the API object the lab cares about.
type Droplet struct {
	ID         int64
	Name       string
	Status     string
	SizeSlug   string
	Region     string
	Tags       []string
	CreatedAt  time.Time
	PublicIPs  []string
	PrivateIPs []string
}

type apiDroplet struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	Status    string   `json:"status"`
	Tags      []string `json:"tags"`
	CreatedAt string   `json:"created_at"`
	SizeSlug  string   `json:"size_slug"`
	Region    struct {
		Slug string `json:"slug"`
	} `json:"region"`
	Networks struct {
		V4 []struct {
			IPAddress string `json:"ip_address"`
			Type      string `json:"type"`
		} `json:"v4"`
	} `json:"networks"`
}

func (a apiDroplet) droplet() Droplet {
	d := Droplet{ID: a.ID, Name: a.Name, Status: a.Status, Tags: a.Tags, SizeSlug: a.SizeSlug, Region: a.Region.Slug}
	d.CreatedAt, _ = time.Parse(time.RFC3339, a.CreatedAt)
	for _, n := range a.Networks.V4 {
		switch n.Type {
		case "public":
			d.PublicIPs = append(d.PublicIPs, n.IPAddress)
		case "private":
			d.PrivateIPs = append(d.PrivateIPs, n.IPAddress)
		}
	}
	return d
}

// Client wraps the API. apply=false (the default everywhere) is a DRY-RUN:
// mutating calls print what they would do and touch nothing.
type Client struct {
	base  string
	token string
	http  *http.Client
	apply bool
	out   io.Writer
}

func NewClient(token string, apply bool, out io.Writer) *Client {
	return &Client{base: apiBase, token: token, http: &http.Client{Timeout: 60 * time.Second}, apply: apply, out: out}
}

func (c *Client) logf(format string, a ...any) {
	fmt.Fprintf(c.out, format+"\n", a...)
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, truncate(string(raw), 400))
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// ListLab returns every droplet that claims lab identity: the `applab-` name
// prefix, or the lab tag (so a mis-named-but-tagged stray still surfaces for
// the reaper to REPORT). It pages through the full droplet listing and
// filters client-side — the scoped token has no tag:read, so `?tag_name=`
// listing is not available (LAB-CAPACIDAD-S2). Read-only; runs in dry-run too.
func (c *Client) ListLab(ctx context.Context) ([]Droplet, error) {
	var ds []Droplet
	for page := 1; page <= 20; page++ {
		var env struct {
			Droplets []apiDroplet `json:"droplets"`
		}
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/v2/droplets?per_page=200&page=%d", page), nil, &env); err != nil {
			return nil, err
		}
		for _, a := range env.Droplets {
			d := a.droplet()
			if ValidLabName(d.Name) || hasTag(d.Tags, LabTag) {
				ds = append(ds, d)
			}
		}
		if len(env.Droplets) < 200 {
			break
		}
	}
	return ds, nil
}

// Get refreshes one droplet by id.
func (c *Client) Get(ctx context.Context, id int64) (Droplet, error) {
	var env struct {
		Droplet apiDroplet `json:"droplet"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/v2/droplets/%d", id), nil, &env); err != nil {
		return Droplet{}, err
	}
	return env.Droplet.droplet(), nil
}

// CreateRequest describes one lab droplet.
type CreateRequest struct {
	Name     string
	Size     string
	Region   string
	Image    string // slug or a snapshot image id as a string
	VPCUUID  string
	SSHKeys  []any // ids or fingerprints
	UserData string
}

// Create makes one droplet, guard first, cap second, network last.
// In dry-run it prints the plan and returns a synthetic droplet (ID 0).
func (c *Client) Create(ctx context.Context, r CreateRequest) (Droplet, error) {
	tags := []string{LabTag}
	if err := GuardCreate(r.Name, tags, 0); err != nil {
		return Droplet{}, err // name/tag violation: refused before any I/O
	}
	existing, err := c.ListLab(ctx)
	if err != nil {
		return Droplet{}, fmt.Errorf("create %s: cannot verify the cap: %w", r.Name, err)
	}
	if err := GuardCreate(r.Name, tags, len(existing)); err != nil {
		return Droplet{}, err
	}
	body := map[string]any{
		"name": r.Name, "size": r.Size, "region": r.Region, "image": r.Image,
		"tags": tags, "monitoring": false, "backups": false, "ipv6": false,
	}
	if r.VPCUUID != "" {
		body["vpc_uuid"] = r.VPCUUID
	}
	if len(r.SSHKeys) > 0 {
		body["ssh_keys"] = r.SSHKeys
	}
	if r.UserData != "" {
		body["user_data"] = r.UserData
	}
	if !c.apply {
		c.logf("DRY-RUN would create droplet %s (size=%s region=%s image=%v vpc=%s tag=%s)", r.Name, r.Size, r.Region, r.Image, r.VPCUUID, LabTag)
		return Droplet{Name: r.Name, Tags: tags, SizeSlug: r.Size, Region: r.Region, Status: "dry-run"}, nil
	}
	var env struct {
		Droplet apiDroplet `json:"droplet"`
	}
	err = c.do(ctx, http.MethodPost, "/v2/droplets", body, &env)
	if err != nil && strings.Contains(err.Error(), "tag:create") {
		// The scoped token cannot create/apply tags (verified live in
		// LAB-CAPACIDAD-S2: POST /v2/tags is 403 too). Degrade LOUDLY to an
		// untagged create — the guard's second factor for these droplets is
		// the lab size fingerprint, and listing is by name prefix.
		c.logf("WARNING: the token lacks tag scopes — creating %s WITHOUT the %q tag; destroy will rely on the name prefix + lab-size fingerprint (grant tag:create to restore the tag factor, OPS-38)", r.Name, LabTag)
		delete(body, "tags")
		err = c.do(ctx, http.MethodPost, "/v2/droplets", body, &env)
	}
	if err != nil {
		return Droplet{}, err
	}
	c.logf("created %s (id %d)", r.Name, env.Droplet.ID)
	return env.Droplet.droplet(), nil
}

// Destroy deletes ONE droplet, judged by GuardDestroy on the object as the
// API reported it. A refusal returns before any network call.
func (c *Client) Destroy(ctx context.Context, d Droplet) error {
	if err := GuardDestroy(d); err != nil {
		return err
	}
	if !c.apply {
		c.logf("DRY-RUN would destroy %s (id %d, created %s)", d.Name, d.ID, d.CreatedAt.Format(time.RFC3339))
		return nil
	}
	if err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/v2/droplets/%d", d.ID), nil, nil); err != nil {
		return err
	}
	c.logf("destroyed %s (id %d)", d.Name, d.ID)
	return nil
}

// DestroyByName destroys one droplet by name. The NAME is guarded before the
// first network call; the droplet object is guarded again after lookup.
func (c *Client) DestroyByName(ctx context.Context, name string) error {
	if !ValidLabName(name) {
		return guardErr("destroy %q: not an applab name — refusing without calling the API", name)
	}
	ds, err := c.ListLab(ctx)
	if err != nil {
		return err
	}
	for _, d := range ds {
		if d.Name == name {
			return c.Destroy(ctx, d)
		}
	}
	return fmt.Errorf("destroy %q: no lab droplet by that name (the listing is by the %q tag)", name, LabTag)
}

// SSHKeyIDs lists the account's registered SSH key ids (scope ssh_key:read).
func (c *Client) SSHKeyIDs(ctx context.Context) ([]any, error) {
	var env struct {
		Keys []struct {
			ID int64 `json:"id"`
		} `json:"ssh_keys"`
	}
	if err := c.do(ctx, http.MethodGet, "/v2/account/keys?per_page=100", nil, &env); err != nil {
		return nil, err
	}
	ids := make([]any, 0, len(env.Keys))
	for _, k := range env.Keys {
		ids = append(ids, k.ID)
	}
	return ids, nil
}

// DefaultVPC returns the region's default VPC uuid and range (scope vpc:read).
func (c *Client) DefaultVPC(ctx context.Context, region string) (uuid, ipRange string, err error) {
	var env struct {
		VPCs []struct {
			ID      string `json:"id"`
			Region  string `json:"region"`
			Default bool   `json:"default"`
			IPRange string `json:"ip_range"`
		} `json:"vpcs"`
	}
	if err := c.do(ctx, http.MethodGet, "/v2/vpcs?per_page=100", nil, &env); err != nil {
		return "", "", err
	}
	for _, v := range env.VPCs {
		if v.Region == region && v.Default {
			return v.ID, v.IPRange, nil
		}
	}
	return "", "", fmt.Errorf("no default VPC in region %s", region)
}

// Snapshot asks for a snapshot of a LAB droplet (guarded like a destroy —
// snapshotting a foreign box would still be acting on it). Best-effort: the
// custom token's scopes may not cover droplet actions; the caller downgrades
// a 403 to "provision from scratch each time" and says so.
func (c *Client) Snapshot(ctx context.Context, d Droplet, name string) error {
	if err := GuardDestroy(d); err != nil {
		return err
	}
	if !c.apply {
		c.logf("DRY-RUN would snapshot %s (id %d) as %q", d.Name, d.ID, name)
		return nil
	}
	body := map[string]any{"type": "snapshot", "name": name}
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/v2/droplets/%d/actions", d.ID), body, nil)
}

// DropletSnapshots lists a lab droplet's snapshots (droplet:read covers it).
func (c *Client) DropletSnapshots(ctx context.Context, id int64) ([]int64, error) {
	var env struct {
		Snapshots []struct {
			ID int64 `json:"id"`
		} `json:"snapshots"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/v2/droplets/%d/snapshots", id), nil, &env); err != nil {
		return nil, err
	}
	var ids []int64
	for _, s := range env.Snapshots {
		ids = append(ids, s.ID)
	}
	return ids, nil
}
