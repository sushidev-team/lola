package main

import (
	"os"
	"reflect"
	"testing"

	"github.com/sushidev-team/lola/internal/reviewprofile"
)

const specialistConfig = minimalConfig + `
[[review.provider]]
provider = "codex-session"
enabled = true
[[review.profile]]
id = "backend"
kind = "backend"
provider = "codex-session"
version = "v1"
enabled = true
required = true
paths = ["internal"]
`

func TestSettingsRoundTripSpecialistsAndOlderClients(t *testing.T) {
	writeTestConfig(t, specialistConfig)
	svc := &ConfigService{}
	dto, err := svc.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if len(dto.ReviewProfiles) != 1 || len(dto.ReviewSpecialties) != 5 {
		t.Fatalf("missing profiles/catalog: %+v", dto.ReviewProfiles)
	}
	original := dto.ReviewProfiles[0]
	dto.ReviewProfiles = nil
	if err = svc.SaveSettings(dto); err != nil {
		t.Fatal(err)
	}
	dto, err = svc.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if len(dto.ReviewProfiles) != 1 || !reflect.DeepEqual(dto.ReviewProfiles[0], original) {
		t.Fatal("older client wiped specialist")
	}
	dto.ReviewProfiles = append(dto.ReviewProfiles, reviewprofile.Profile{ID: " uiux ", Kind: "uiux", Provider: "codex-session", Version: "2", Enabled: true, Paths: []string{" frontend ", " "}})
	if err = svc.SaveSettings(dto); err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ReviewProfiles) != 2 || got.ReviewProfiles[1].ID != "uiux" || !reflect.DeepEqual(got.ReviewProfiles[1].Paths, []string{"frontend"}) {
		t.Fatalf("roundtrip: %+v", got.ReviewProfiles)
	}
	got.ReviewProfiles = []reviewprofile.Profile{}
	if err = svc.SaveSettings(got); err != nil {
		t.Fatal(err)
	}
	got, err = svc.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.ReviewProfiles == nil || len(got.ReviewProfiles) != 0 {
		t.Fatal("explicit empty array must remove profiles and remain editable")
	}
}

func TestSettingsRejectSpecialistProviderRemovalWithoutWriting(t *testing.T) {
	for _, change := range []string{"provider", "duplicate", "scope", "timeout"} {
		t.Run(change, func(t *testing.T) {
			path := writeTestConfig(t, specialistConfig)
			before, _ := os.ReadFile(path)
			svc := &ConfigService{}
			dto, err := svc.GetSettings()
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "provider":
				dto.ReviewProviders = []ReviewProviderDTO{}
			case "duplicate":
				dto.ReviewProfiles = append(dto.ReviewProfiles, dto.ReviewProfiles[0])
			case "scope":
				dto.ReviewProfiles[0].Projects = []string{"not-configured"}
			case "timeout":
				dto.ReviewProfiles[0].TimeoutSeconds = 901
			}
			if err = svc.SaveSettings(dto); err == nil {
				t.Fatal("invalid profile saved")
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("failed save changed configuration")
			}
		})
	}
}
