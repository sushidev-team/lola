package attention

import "testing"

func TestKeyAgentLocked(t *testing.T) {
	for in, want := range map[string]bool{
		"sign_and_send_pubkey: signing failed for ED25519 \"k\" from agent: agent refused operation": true,
		"git@github.com: Permission denied (publickey).\nCommunication with agent failed.":           true,
		"Can you Unlock 1Password, then tell me to continue?":                                        true,
		"Permission denied (publickey).":                                                             false,
		"agent refused\noperation":                                                                   true,
		"\x1b[31mcommunication with\x1b[0m\n  agent failed":                                          true,
		"all tests pass": false,
	} {
		if got := KeyAgentLocked(in); got != want {
			t.Errorf("KeyAgentLocked(%q) = %v, want %v", in, got, want)
		}
	}
}
