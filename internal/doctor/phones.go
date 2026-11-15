package doctor

import (
	"fmt"

	"github.com/sushidev-team/lola/internal/devices"
)

// checkPhones names the paired-phone registry row.
const checkPhones = "phones"

// PhonesResult reports the paired-phone registry under home. It fails while
// devices.json or devices.revoked cannot be trusted (the registry then
// authenticates nobody) or while a revocation lives in memory only. Never
// critical. Details name the files, never their contents, and carry no
// device key or name.
func PhonesResult(home string) Result {
	reg := devices.Open(home, nil, nil)
	switch {
	case reg.Err() != nil:
		return Result{
			Name:   checkPhones,
			Detail: devices.FileName + " or " + devices.RevokedFileName + " is unreadable; no phone can connect until it is repaired (Settings, Phones)",
			Fix:    FixPhonesRegistry,
		}
	case reg.Unsaved() != nil:
		return Result{
			Name:   checkPhones,
			Detail: "a phone revocation is not saved: neither " + devices.FileName + " nor " + devices.RevokedFileName + " could be written (Settings, Phones)",
			Fix:    FixPhonesRegistry,
		}
	}
	n := len(reg.List())
	if n == 0 {
		return Result{Name: checkPhones, OK: true, Detail: "no paired phone"}
	}
	return Result{Name: checkPhones, OK: true, Detail: plural(n, "paired phone")}
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
