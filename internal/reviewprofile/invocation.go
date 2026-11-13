package reviewprofile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Invocation preserves the instruction and requested model, not a claim about
// the model actually served by a provider. Empty RequestedModel delegates to
// the agent's default, which can change outside Lola.
type Invocation struct {
	Agent          string `json:"agent"`
	RequestedModel string `json:"requested_model,omitempty"`
	Environment    string `json:"environment"`
	Instruction    string `json:"instruction"`
	PromptSHA256   string `json:"prompt_sha256"`
}

func NewInvocation(agent, model, environment, instruction string) *Invocation {
	hash := sha256.Sum256([]byte(instruction))
	return &Invocation{Agent: agent, RequestedModel: model, Environment: environment, Instruction: instruction, PromptSHA256: hex.EncodeToString(hash[:])}
}

func (v *Invocation) Validate() error {
	if v == nil {
		return nil // legacy evidence remains inspectable with unknown provenance
	}
	if !validOwnerText(v.Agent) || len(v.RequestedModel) > 240 || (v.Environment != "host-read-only" && v.Environment != "container-read-only") || len(v.Instruction) == 0 || len(v.Instruction) > 20000 || *v != *NewInvocation(v.Agent, v.RequestedModel, v.Environment, v.Instruction) {
		return fmt.Errorf("invalid specialist invocation provenance")
	}
	return nil
}

func (v *Invocation) Matches(expected *Invocation) bool {
	return v != nil && expected != nil && *v == *expected
}

// CheckInvocation keeps direct legacy adapter callers compatible while refusing
// to execute a new provenance-bearing request with different model or prompts.
func (in Inputs) CheckInvocation(expected *Invocation) error {
	if in.Invocation != nil && !in.Invocation.Matches(expected) {
		return fmt.Errorf("specialist invocation changed after reservation")
	}
	return nil
}
