package main

// `lab up` — the topology, provisioned the way a customer's box is.
//
//   applab-gen           c-4         CPU-Optimized, 4 DEDICATED vCPU. The
//                                    measuring instrument may not have noisy-
//                                    neighbour jitter, and it must be BIGGER
//                                    than the target: a generator that
//                                    saturates first measures itself.
//   applab-target-basic  s-2vcpu-2gb Basic, SHARED vCPU — what a customer
//                                    actually buys. This box produces the
//                                    honest number you tell a customer.
//   applab-target-dedic  c-2         CPU-Optimized, 2 dedicated vCPU — low
//                                    variance, the box for detecting
//                                    regressions between versions. On 21 %
//                                    steal you cannot tell a regression from
//                                    a neighbour.
//
// All three in ONE region and ONE private VPC (without the VPC you measure the
// internet); the base latency of the private link is measured and recorded.
// Targets are provisioned with scripts/install.sh — the exact path a customer
// runs — so every lab run also exercises the installer; an installer failure
// is a finding, not an inconvenience.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type nodeSpec struct {
	role, name, size string
	target           bool // runs the engine (vs the generator)
}

var topology = []nodeSpec{
	{role: "gen", name: NamePrefix + "gen", size: "c-4", target: false},
	{role: "target-basic", name: NamePrefix + "target-basic", size: "s-2vcpu-2gb", target: true},
	{role: "target-dedic", name: NamePrefix + "target-dedic", size: "c-2", target: true},
}

// hourlyUSD is DigitalOcean list pricing (2026-08), used by `lab report` to
// state what a run cost. Prices move; the report labels them as list prices.
var hourlyUSD = map[string]float64{
	"c-4":         0.12500,
	"c-2":         0.06250,
	"s-2vcpu-2gb": 0.02679,
}

const (
	baseImage  = "ubuntu-24-04-x64"
	enginePort = 8090 // install.sh's default internal port; the sweep hits it directly over the VPC
	labTenant  = "lab"
	labRole    = "dueno"
	sshOpts    = "-o StrictHostKeyChecking=accept-new -o ConnectTimeout=10 -o BatchMode=yes"
)

// ── local process helpers ──────────────────────────────────────────────────

func sh(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// sshRun executes a bash script (stdin) on a droplet as root.
func sshRun(ip, script string) (string, error) {
	args := append(strings.Fields(sshOpts), "root@"+ip, "bash", "-se")
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("ssh %s: %w\n%s", ip, err, truncate(string(out), 2000))
	}
	return string(out), nil
}

func scpTo(local, ip, remote string) error {
	args := append(strings.Fields(sshOpts), "-q", local, "root@"+ip+":"+remote)
	out, err := exec.Command("scp", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("scp %s → %s:%s: %w\n%s", local, ip, remote, err, truncate(string(out), 800))
	}
	return nil
}

func scpFrom(ip, remote, local string) error {
	args := append(strings.Fields(sshOpts), "-q", "root@"+ip+":"+remote, local)
	out, err := exec.Command("scp", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("scp %s:%s → %s: %w\n%s", ip, remote, local, err, truncate(string(out), 800))
	}
	return nil
}

func waitSSH(ip string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := sshRun(ip, "true"); err == nil {
			return nil
		}
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("ssh to %s not up after %s", ip, timeout)
}

// repoRoot finds the appximo checkout this binary was built from, so `up` can
// build the engine + capacity binaries and ship install.sh/schema/seed.
func repoRoot() (string, error) {
	for _, cand := range []string{".", "/root/appximo"} {
		if _, err := os.Stat(filepath.Join(cand, "scripts", "install.sh")); err == nil {
			abs, _ := filepath.Abs(cand)
			return abs, nil
		}
	}
	return "", fmt.Errorf("cannot find the appximo repo (scripts/install.sh) from the working directory — run from the checkout")
}

// buildArtifacts builds the engine and capacity binaries for linux/amd64.
func buildArtifacts(root string) (engineBin, capacityBin, version string, err error) {
	dir := artifactDir()
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	engineBin = filepath.Join(dir, "appximo")
	capacityBin = filepath.Join(dir, "capacity")
	env := append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	for _, b := range []struct{ out, pkg string }{{engineBin, "./cmd/appximo"}, {capacityBin, "./tools/capacity"}} {
		cmd := exec.Command("go", "build", "-o", b.out, b.pkg)
		cmd.Dir = root
		cmd.Env = env
		var out []byte
		if out, err = cmd.CombinedOutput(); err != nil {
			err = fmt.Errorf("go build %s: %w\n%s", b.pkg, err, truncate(string(out), 1200))
			return
		}
	}
	v, _ := sh(engineBin, "version")
	version = strings.TrimSpace(strings.Split(v, "\n")[0])
	return
}

func userData() string {
	for _, p := range []string{"/root/.ssh/id_ed25519.pub", "/root/.ssh/id_rsa.pub"} {
		if b, err := os.ReadFile(p); err == nil { //nolint:gosec
			return "#cloud-config\nssh_authorized_keys:\n  - " + strings.TrimSpace(string(b)) + "\n"
		}
	}
	return ""
}

// ensureNode creates one droplet if it is not already alive, then waits for
// an active status and both IPs.
func ensureNode(ctx context.Context, c *Client, st *State, spec nodeSpec, image string, vpc string, keys []any, ud string) (NodeState, error) {
	live, err := c.ListLab(ctx)
	if err != nil {
		return NodeState{}, err
	}
	var d Droplet
	found := false
	for _, x := range live {
		if x.Name == spec.name {
			d, found = x, true
			break
		}
	}
	if !found {
		d, err = c.Create(ctx, CreateRequest{
			Name: spec.name, Size: spec.size, Region: st.Region,
			Image: image, VPCUUID: vpc, SSHKeys: keys, UserData: ud,
		})
		if err != nil {
			return NodeState{}, err
		}
	}
	if !c.apply {
		return NodeState{ID: d.ID, Name: spec.name, Size: spec.size}, nil
	}
	deadline := time.Now().Add(6 * time.Minute)
	for !(d.Status == "active" && len(d.PublicIPs) > 0 && len(d.PrivateIPs) > 0) {
		if time.Now().After(deadline) {
			return NodeState{}, fmt.Errorf("%s (id %d) not active with IPs after 6m (status %s)", spec.name, d.ID, d.Status)
		}
		time.Sleep(5 * time.Second)
		if d, err = c.Get(ctx, d.ID); err != nil {
			return NodeState{}, err
		}
	}
	n := NodeState{ID: d.ID, Name: d.Name, Size: d.SizeSlug, PublicIP: d.PublicIPs[0], PrivateIP: d.PrivateIPs[0], CreatedAt: d.CreatedAt}
	st.Nodes[spec.role] = n
	if err := st.save(); err != nil {
		return n, err
	}
	return n, nil
}

// datasetVars maps the two documented sizes to the seed's psql variables.
func datasetVars(size string) (string, error) {
	switch size {
	case "large":
		return "-v productos=20000 -v clientes=8000 -v ordenes=60000", nil
	case "small":
		return "-v productos=400 -v clientes=200 -v ordenes=1500", nil
	}
	return "", fmt.Errorf("dataset %q: use small or large (dataset/README.md)", size)
}

// provisionTarget takes an active droplet through the CUSTOMER path:
// install.sh → capacity-arm env (threshold raised, middleware untouched) →
// tenant registration → deterministic seed → a minted tenant token → smoke.
func provisionTarget(root string, n *NodeState, engineBin, dataset string) error {
	if err := waitSSH(n.PublicIP, 5*time.Minute); err != nil {
		return err
	}
	if _, err := sshRun(n.PublicIP, "mkdir -p /root/lab"); err != nil {
		return err
	}
	for local, remote := range map[string]string{
		engineBin: "/root/lab/appximo",
		filepath.Join(root, "scripts", "install.sh"):                  "/root/lab/install.sh",
		filepath.Join(root, "tools", "lab", "dataset", "schema.json"): "/root/lab/schema.json",
		filepath.Join(root, "tools", "lab", "dataset", "seed.sql"):    "/root/lab/seed.sql",
	} {
		if err := scpTo(local, n.PublicIP, remote); err != nil {
			return err
		}
	}
	// 1. The installer — the same one a customer runs. If it fails or its
	// post-install verification complains, that IS a finding; surface it whole.
	install := fmt.Sprintf(`chmod +x /root/lab/appximo
bash /root/lab/install.sh --domain %s.internal --email lab@appximo.dev \
  --binary /root/lab/appximo --schema /root/lab/schema.json \
  --port %d --internal-tls --yes`, n.Name, enginePort)
	if out, err := sshRun(n.PublicIP, install); err != nil {
		return fmt.Errorf("install.sh FAILED on %s (a finding — the customer path broke):\n%s\n%w", n.Name, truncate(out, 3000), err)
	}
	// 2. The capacity arm: the per-tenant limiter's THRESHOLD moves out of the
	// measurement's way; the middleware stays in the chain (CAPACIDAD-USL-S1).
	// Selfmon at 10 s ticks so ?since= windows match the runs.
	arm := `grep -q '^RATE_LIMIT_RPS=' /etc/appximo/appximo.env || cat >> /etc/appximo/appximo.env <<'EOF'
RATE_LIMIT_RPS=1000000
RATE_LIMIT_BURST=100000
APPXIMO_SELFMON_INTERVAL=10s
EOF
systemctl restart appximo
for i in $(seq 1 30); do curl -fsS http://127.0.0.1:` + strconv.Itoa(enginePort) + `/healthz >/dev/null 2>&1 && break; sleep 1; done
curl -fsS http://127.0.0.1:` + strconv.Itoa(enginePort) + `/healthz >/dev/null`
	if out, err := sshRun(n.PublicIP, arm); err != nil {
		return fmt.Errorf("capacity arm on %s: %s: %w", n.Name, truncate(out, 800), err)
	}
	// 3. Tenant + seed + token. Idempotent: an existing tenant is tolerated,
	// the seed TRUNCATEs first, the token is re-minted each time.
	vars, err := datasetVars(dataset)
	if err != nil {
		return err
	}
	setup := fmt.Sprintf(`set -o pipefail
KEY=$(grep '^ADMIN_KEY=' /etc/appximo/appximo.env | head -1 | cut -d= -f2-)
SEC=$(grep '^JWT_SECRET=' /etc/appximo/appximo.env | head -1 | cut -d= -f2-)
CODE=$(curl -s -o /root/lab/register.out -w '%%{http_code}' -X POST http://127.0.0.1:9090/tenants \
  -H "X-Admin-Key: $KEY" -H 'Content-Type: application/json' \
  -d "{\"tenant_id\":\"%[1]s\",\"display_name\":\"Lab\",\"email\":\"lab@appximo.dev\",\"plan\":\"free\",\"schema\":$(cat /root/lab/schema.json)}")
case "$CODE" in 2*|409) : ;; *) echo "tenant registration: HTTP $CODE"; cat /root/lab/register.out; exit 1;; esac
sudo -u postgres psql -q appximo %[2]s -f /root/lab/seed.sql
TOK=$(/opt/appximo/bin/appximo token --secret "$SEC" --tenant %[1]s --role %[3]s | tail -1)
curl -fsS -H "Host: %[1]s.%[4]s.internal" -H "Authorization: Bearer $TOK" \
  "http://127.0.0.1:%[5]d/api/productos?per_page=1&fields=id" >/dev/null
echo "ADMINKEY=$KEY"
echo "LABTOKEN=$TOK"`, labTenant, vars, labRole, n.Name, enginePort)
	out, err := sshRun(n.PublicIP, setup)
	if err != nil {
		return fmt.Errorf("tenant/seed/token on %s: %w", n.Name, err)
	}
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, "ADMINKEY="); ok {
			n.AdminKey = strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(line, "LABTOKEN="); ok {
			n.Token = strings.TrimSpace(v)
		}
	}
	if n.AdminKey == "" || n.Token == "" {
		return fmt.Errorf("provision %s: could not read back admin key / lab token", n.Name)
	}
	n.Port = enginePort
	return nil
}

func provisionGen(n *NodeState, capacityBin string) error {
	if err := waitSSH(n.PublicIP, 5*time.Minute); err != nil {
		return err
	}
	if _, err := sshRun(n.PublicIP, "mkdir -p /root/lab/results"); err != nil {
		return err
	}
	if err := scpTo(capacityBin, n.PublicIP, "/root/lab/capacity"); err != nil {
		return err
	}
	_, err := sshRun(n.PublicIP, "chmod +x /root/lab/capacity")
	return err
}

var pingAvgRe = regexp.MustCompile(`= [0-9.]+/([0-9.]+)/`)

// vpcLatency measures the private link's base RTT from the generator.
func vpcLatency(genIP, targetPrivIP string) (float64, error) {
	out, err := sshRun(genIP, "ping -c 20 -i 0.2 -q "+targetPrivIP+" | tail -1")
	if err != nil {
		return 0, err
	}
	m := pingAvgRe.FindStringSubmatch(out)
	if m == nil {
		return 0, fmt.Errorf("cannot parse ping output: %s", truncate(out, 200))
	}
	return strconv.ParseFloat(m[1], 64)
}
