package main

import (
	"errors"
	"slices"
	"strconv"

	"github.com/sushidev-team/lola/internal/devlogin"
	"github.com/sushidev-team/lola/internal/preview"
	"github.com/sushidev-team/lola/internal/protocol"
)

type TestLoginList struct {
	AutoLogin devlogin.AutoLogin `json:"autoLogin"`
	Project   string             `json:"project"`
	Profiles  []devlogin.Profile `json:"profiles"`
}

// Match the advertised origin to exactly one project. On older stack snapshots
// a member's port lease identifies it only when the session advertises that origin.
func loginOwner(data protocol.SessionsData, session, url string) (string, error) {
	u, err := preview.Allowed(url)
	if err != nil {
		return "", err
	}
	port, _ := strconv.Atoi(effectivePort(u))
	match := func(raw string) bool { return sameOrigin(raw, url) }
	for _, s := range data.Sessions {
		if s.ID != session {
			continue
		}
		advertised := slices.ContainsFunc(s.DevURLs, match)
		if !s.Stack && len(s.Apps) == 0 && advertised && s.Project != "" {
			return s.Project, nil
		}
		var owners, leases []string
		for _, app := range s.Apps {
			if slices.ContainsFunc(app.DevURLs, match) {
				owners = append(owners, app.Name)
			}
			if slices.Contains(app.Ports, port) {
				leases = append(leases, app.Name)
			}
		}
		if len(owners) == 0 && advertised {
			owners = leases
		}
		if len(owners) == 1 && owners[0] != "" {
			return owners[0], nil
		}
		break
	}
	return "", errors.New("test logins need a dev address belonging to a single app or project")
}

func (s *PreviewService) TestLogins(session, url string) (TestLoginList, error) {
	data, err := s.refreshSessions()
	if err != nil {
		return TestLoginList{}, err
	}
	project, err := loginOwner(data, session, url)
	if err != nil {
		return TestLoginList{}, err
	}
	v, err := devlogin.ForProject(project)
	if err != nil {
		return TestLoginList{}, err
	}
	profiles, err := v.List(false)
	if err != nil {
		return TestLoginList{}, err
	}
	choice, err := v.AutoLogin()
	return TestLoginList{Project: project, Profiles: profiles, AutoLogin: choice}, err
}

// Recheck the project the picker displayed before changing or releasing a login.
// The same lock orders snapshot retirement and the live listener check.
func (s *PreviewService) loginStore(session, url, project, frameOrigin string) (devlogin.Store, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.snapshot()
	if err != nil {
		return devlogin.Store{}, err
	}
	owner, err := loginOwner(data, session, url)
	if err != nil {
		return devlogin.Store{}, err
	}
	if owner != project {
		return devlogin.Store{}, errors.New("this address changed projects; reopen Test logins")
	}
	if frameOrigin != "" {
		current, err := s.srv.Open(session, url)
		if err != nil {
			return devlogin.Store{}, err
		}
		if !sameOrigin(current, frameOrigin) {
			return devlogin.Store{}, errors.New("reload the preview before signing in")
		}
	}
	return devlogin.ForProject(owner)
}

func (s *PreviewService) SaveTestLogin(session, url, project string, profile devlogin.Profile, password string, editing bool) error {
	v, err := s.loginStore(session, url, project, "")
	if err != nil {
		return err
	}
	return v.Save(profile, password, editing)
}

func (s *PreviewService) RemoveTestLogin(session, url, project, name string) error {
	v, err := s.loginStore(session, url, project, "")
	if err != nil {
		return err
	}
	return v.Delete(name)
}

func (s *PreviewService) UseTestLogin(session, url, project, name, frameOrigin string) (devlogin.Credentials, error) {
	if frameOrigin == "" {
		return devlogin.Credentials{}, errors.New("open a preview before signing in")
	}
	v, err := s.loginStore(session, url, project, frameOrigin)
	if err != nil {
		return devlogin.Credentials{}, err
	}
	return v.Credentials(name)
}

func (s *PreviewService) SetAutoLogin(session, url, project, name string, enabled bool) error {
	v, err := s.loginStore(session, url, project, "")
	if err != nil {
		return err
	}
	origin, err := preview.Origin(url)
	if err != nil {
		return err
	}
	return v.SetAutoLogin(devlogin.AutoLogin{Name: name, Origin: origin}, enabled)
}

func (s *PreviewService) AutoTestLogin(session, url, project, name, frameOrigin string) (*devlogin.Credentials, error) {
	if frameOrigin == "" {
		return nil, errors.New("open a preview before signing in")
	}
	v, err := s.loginStore(session, url, project, frameOrigin)
	if err != nil {
		return nil, err
	}
	origin, err := preview.Origin(url)
	if err != nil {
		return nil, err
	}
	return v.AutoCredentials(devlogin.AutoLogin{Name: name, Origin: origin})
}
