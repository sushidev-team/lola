package main

import (
	"strings"

	"github.com/sushidev-team/lola/internal/toolchain"
)

// ToolchainDTO is one toolchain the project form shows.
type ToolchainDTO struct {
	Kind    string `json:"kind"`
	Version string `json:"version"`
}

// ToolchainInfoDTO is what the project form needs to offer toolchains: what
// the checkout's files ask for, and every toolchain lola can add.
type ToolchainInfoDTO struct {
	Detected []ToolchainDTO `json:"detected"`
	// Extras are details detection adds on its own: PHP extensions and node
	// package managers ("redis", "pnpm@9.15.0").
	Extras []string `json:"extras"`
	Kinds  []string `json:"kinds"`
}

// DetectToolchains reads the checkout at path (the project form's folder) and
// reports the toolchains a sandboxed session of it would get with no
// sandbox_toolchains set. Read-only and bounded (internal/toolchain).
func (s *ConfigService) DetectToolchains(path string) ToolchainInfoDTO {
	out := ToolchainInfoDTO{Detected: []ToolchainDTO{}, Extras: []string{}}
	for _, k := range toolchain.Kinds {
		out.Kinds = append(out.Kinds, string(k))
	}
	if path = strings.TrimSpace(path); path == "" {
		return out
	}
	spec := toolchain.Detect(path)
	for _, t := range spec.Toolchains {
		out.Detected = append(out.Detected, ToolchainDTO{Kind: string(t.Kind), Version: t.Version})
	}
	out.Extras = append(out.Extras, spec.PHPExtensions...)
	out.Extras = append(out.Extras, spec.NodeTools...)
	return out
}

// nonNil is in, or an empty slice: the frontend reads [] as "set, empty".
func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// trimAll trims every entry.
func trimAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.TrimSpace(s)
	}
	return out
}
