package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
	"github.com/sushidev-team/lola/internal/devlogin"
	"github.com/sushidev-team/lola/internal/protocol"
)

func loginProject(s protocol.SessionInfo, app string) (string, error) {
	if s.Sandbox != "" {
		return "", errors.New("test login commands require a host session")
	}
	if !s.Stack && len(s.Apps) == 0 && app == "" && s.Project != "" {
		return s.Project, nil
	}
	for _, a := range s.Apps {
		if app != "" && a.Name == app {
			return app, nil
		}
	}
	return "", errors.New("choose a member of this session with --app (omit it for a single project)")
}

func devLoginCmd() *cobra.Command {
	root := &cobra.Command{Use: "login", Short: "Use test accounts shared with host agents"}
	var app string
	root.PersistentFlags().StringVar(&app, "app", "", "stack member project")
	vault := func() (devlogin.Store, error) {
		if os.Getenv("LOLA_SANDBOX") != "" {
			return devlogin.Store{}, errors.New("test login commands require a host session")
		}
		s, err := currentDevSession()
		if err != nil {
			return devlogin.Store{}, err
		}
		project, err := loginProject(s, app)
		if err != nil {
			return devlogin.Store{}, err
		}
		return devlogin.ForProject(project)
	}
	list := &cobra.Command{Use: "list", Short: "List shared test login names", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		s, err := vault()
		if err != nil {
			return err
		}
		profiles, err := s.List(true)
		if err != nil {
			return err
		}
		for _, p := range profiles {
			if _, err := fmt.Fprintln(c.OutOrStdout(), p.Name); err != nil {
				return err
			}
		}
		return nil
	}}
	run := &cobra.Command{Use: "run NAME -- COMMAND [ARG...]", Short: "Run a test with LOLA_TEST_USERNAME and LOLA_TEST_PASSWORD in its environment", Args: cobra.MinimumNArgs(2), RunE: func(c *cobra.Command, args []string) error {
		if c.ArgsLenAtDash() != 1 {
			return errors.New("use run NAME -- COMMAND [ARG...]")
		}
		s, err := vault()
		if err != nil {
			return err
		}
		child := exec.CommandContext(c.Context(), args[1], args[2:]...)
		child.Stdin, child.Stdout, child.Stderr = c.InOrStdin(), c.OutOrStdout(), c.ErrOrStderr()
		return s.Run(args[0], child)
	}}
	root.AddCommand(list, run)
	return root
}
