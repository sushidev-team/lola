package sshterm

import (
	"reflect"
	"testing"
)

func TestDetectPromptKnowsTheCommonQuestions(t *testing.T) {
	for _, tc := range []struct {
		raw, kind, word string
	}{
		{"[sudo] password for martin: ", PromptSecret, ""},
		{"Password: ", PromptSecret, ""},
		{"martin@box's password: ", PromptSecret, ""},
		{"Enter passphrase for key '/Users/m/.ssh/id_ed25519': ", PromptSecret, ""},
		{"Bad passphrase, try again for /Users/m/.ssh/id: ", PromptSecret, ""},
		{"Verification code: ", PromptSecret, ""},
		{"One-time password (OATH) for `martin': ", PromptSecret, ""},
		{"Enter MFA code:", PromptSecret, ""},
		{"OTP: ", PromptSecret, ""},
		{"Duo passcode or option (1-3): ", PromptSecret, ""},
		{"Enter PIN for 'yubikey': ", PromptSecret, ""},
		{"Enter code: ", PromptSecret, ""},
		{"\x1b[1mPassword:\x1b[0m ", PromptSecret, ""},
		{"Are you sure you want to continue connecting (yes/no/[fingerprint])? ", PromptConfirm, "yes"},
		{"Please type 'yes', 'no' or the fingerprint: ", PromptConfirm, "yes"},
		{"Do you want to continue? [Y/n] ", PromptConfirm, "y"},
		{"Remove it (y/N)? ", PromptConfirm, "y"},
		{"Really delete? (yes/no): ", PromptConfirm, "yes"},
	} {
		p, ok := DetectPrompt("some output\n" + tc.raw)
		if !ok || p.Kind != tc.kind || p.Word != tc.word {
			t.Errorf("%q: %+v, %v", tc.raw, p, ok)
		}
	}
}

func TestDetectPromptLeavesOrdinaryOutputAlone(t *testing.T) {
	for _, raw := range []string{
		"",
		"Password: \n",              // answered: the cursor has moved on
		"Password: \r\nWelcome\r\n", // the same
		"building password module... ",
		"progress: 45% ",
		"exit code: ",
		"spinning up: ",
		"$ ",
		"root@box:~# ",
		"Password: hunter2", // text after the question: it is not waiting
		"waiting for code to finish",
		"Password: \rDone     ", // overwritten
		string(make([]byte, 300)) + "x password: long lines are not questions and this one is enormous, far beyond what any program prints when it asks for a password, so it is treated as ordinary output even though it ends in the right words and carries a colon at the very end of it, because the limit is the limit, password: ",
	} {
		if p, ok := DetectPrompt(raw); ok {
			t.Errorf("%q read as a prompt: %+v", raw, p)
		}
	}
}

func TestPromptLineCannotHideBehindInvisibleCharacters(t *testing.T) {
	p, ok := DetectPrompt("Pass‮word​: ")
	if !ok || p.Line != "Password:" {
		t.Fatalf("%+v, %v", p, ok)
	}
	// What a carriage return overwrote is gone from screen, so it is gone from the card.
	p, ok = DetectPrompt("Lola needs your key\rPassword: ")
	if !ok || p.Line != "Password:" {
		t.Fatalf("%+v, %v", p, ok)
	}
	p, ok = DetectPrompt("\x1b]0;title\x07\x1b[2K\rPassword: ")
	if !ok || p.Line != "Password:" {
		t.Fatalf("%+v, %v", p, ok)
	}
}

func TestPromptContextIsForQuestionsThatNeedIt(t *testing.T) {
	raw := "banner\n\nThe authenticity of host 'box' can't be established.\nED25519 key fingerprint is SHA256:abc.\nAre you sure you want to continue connecting (yes/no/[fingerprint])? "
	p, ok := DetectPrompt(raw)
	want := []string{"The authenticity of host 'box' can't be established.", "ED25519 key fingerprint is SHA256:abc."}
	if !ok || !reflect.DeepEqual(p.Context, want) {
		t.Fatalf("%+v", p)
	}
	if p, _ := DetectPrompt("Permission denied.\nPassword: "); p.Context != nil {
		t.Fatalf("a password question got context: %+v", p)
	}
	// A menu names what the answer means, so it stays even though it asks for a code.
	if p, _ := DetectPrompt("1. Push\n2. Call\nPasscode or option (1-2): "); len(p.Context) != 2 {
		t.Fatalf("passcode context: %+v", p.Context)
	}
}

func TestOpenLine(t *testing.T) {
	for raw, want := range map[string]bool{"Enter a value: ": true, "done\n": false, "": false, "a\r\n": false, "x\r": false} {
		if OpenLine(raw) != want {
			t.Errorf("%q: want %v", raw, want)
		}
	}
}
