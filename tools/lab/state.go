package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// State is the orchestrator's memory of the laboratory, written after every
// step so a crashed `up` leaves enough to resume — but NOTE: `down` and `reap`
// never trust it; they list by tag from the API. It carries the targets'
// admin keys and lab tokens, so it lives outside every repo with mode 0600.
type State struct {
	Region        string               `json:"region"`
	VPCUUID       string               `json:"vpc_uuid,omitempty"`
	VPCRange      string               `json:"vpc_range,omitempty"`
	VPCLatencyMs  map[string]float64   `json:"vpc_latency_ms,omitempty"` // role → avg ping ms from the generator
	SnapshotImage int64                `json:"snapshot_image,omitempty"`
	Dataset       string               `json:"dataset,omitempty"` // small | large
	EngineVersion string               `json:"engine_version,omitempty"`
	Nodes         map[string]NodeState `json:"nodes"`
}

// NodeState is one droplet's coordinates plus the secrets `up` minted on it.
type NodeState struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Size      string    `json:"size"`
	PublicIP  string    `json:"public_ip"`
	PrivateIP string    `json:"private_ip"`
	CreatedAt time.Time `json:"created_at"`
	Port      int       `json:"port,omitempty"`
	AdminKey  string    `json:"admin_key,omitempty"`
	Token     string    `json:"token,omitempty"`
}

func stateDir() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/root"
	}
	return filepath.Join(home, ".applab")
}

func statePath() string   { return filepath.Join(stateDir(), "state.json") }
func resultsDir() string  { return filepath.Join(stateDir(), "results") }
func artifactDir() string { return filepath.Join(stateDir(), "artifacts") }

func loadState() (*State, error) {
	b, err := os.ReadFile(statePath()) //nolint:gosec
	if os.IsNotExist(err) {
		return &State{Nodes: map[string]NodeState{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("state file %s is corrupt (%w) — move it aside; `lab down`/`lab reap` do not need it", statePath(), err)
	}
	if s.Nodes == nil {
		s.Nodes = map[string]NodeState{}
	}
	return &s, nil
}

func (s *State) save() error {
	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath(), b, 0o600)
}
