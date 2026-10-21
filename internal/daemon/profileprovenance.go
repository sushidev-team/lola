package daemon

import (
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/goalreview"
	"github.com/sushidev-team/lola/internal/reviewprofile"
)

func profileInvocation(p reviewprofile.Profile, provider config.ReviewProvider, isolated bool) *reviewprofile.Invocation {
	agent, _ := config.ReviewAgentFor(p.Provider)
	environment, instruction := "host-read-only", p.Instruction()
	if isolated {
		environment, instruction = "container-read-only", goalreview.Instruction(p)
	}
	return reviewprofile.NewInvocation(agent, provider.Model, environment, instruction)
}

func (d *Daemon) profileInvocationCurrent(p reviewprofile.Profile, in reviewprofile.Inputs, isolated bool) bool {
	current, provider, err := d.profileFor(p.ID)
	return err == nil && in.Invocation.Matches(profileInvocation(current, provider, isolated))
}
