package protocol

import (
	"encoding/json"
	"time"

	"github.com/sushidev-team/lola/internal/goalpolicy"
)

const (
	CmdReviewComplete = "reviewPackageComplete" // publish and report completion, never lifecycle authority
	CmdReviewPublish  = "reviewPackagePublish"  // agent-side, bound to Request.Session
	CmdReviewPackages = "reviewPackages"        // human-side, retained metadata
	CmdReviewImage    = "reviewPackageImage"    // human-side, retained image only
	CmdReviewMark     = "reviewPackageMark"     // human inspection, never merge approval
	CmdReviewDiff     = "reviewPackageDiff"     // exact retained heads, read-only
	CmdReviewVerify   = "reviewPackageVerify"   // human-authorized isolated check
	CmdReviewChecks   = "reviewPackageChecks"   // retained independent receipts
	CmdReviewRecover  = "reviewPackageRecover"  // stop an interrupted owned check
	CmdReviewFeedback = "reviewPackageFeedback" // human feedback bound to retained material
)

type ReviewFeedbackArgs struct {
	Package string `json:"package"`
	Section string `json:"section,omitempty"`
	Note    string `json:"note"`
}

type ReviewVerifyArgs struct {
	Package string           `json:"package"`
	ID      string           `json:"id"`
	Spec    goalpolicy.Check `json:"spec"`
}

type ReviewChecksData struct {
	Checks []ReviewCheck `json:"checks"`
}

type ReviewCheck struct {
	ID         string           `json:"id"`
	Package    string           `json:"package"`
	Session    string           `json:"session"`
	Base       string           `json:"base"`
	Head       string           `json:"head"`
	Spec       goalpolicy.Check `json:"spec"`
	State      string           `json:"state"`
	Stopped    bool             `json:"stopped"`
	Output     string           `json:"output,omitempty"`
	Error      string           `json:"error,omitempty"`
	StartedAt  time.Time        `json:"startedAt"`
	FinishedAt time.Time        `json:"finishedAt,omitempty"`
}

type ReviewPublishArgs struct {
	Manifest json.RawMessage `json:"manifest"`      // validated by reviewpack, not the transport
	App      string          `json:"app,omitempty"` // a member of this session's stack
}

type ReviewPackagesData struct {
	ReviewTracking bool                    `json:"reviewTracking,omitempty"`
	Packages       []ReviewPackage         `json:"packages"`
	Reviewed       map[string]ReviewRecord `json:"reviewed,omitempty"`
}

type ReviewRecord struct {
	Package string    `json:"package"`
	At      time.Time `json:"at"`
}

type ReviewMarkArgs struct {
	Package  string `json:"package"`
	Previous string `json:"previous"`
}

type ReviewDiffArgs struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ReviewPackage is a wire projection of retained material. All content is agent-provided.
// Keep protocol independent of filesystem/storage packages.
type ReviewPackage struct {
	Version   int              `json:"version"`
	Title     string           `json:"title"`
	Body      string           `json:"body"`
	Base      string           `json:"base"`
	Head      string           `json:"head"`
	Screens   []ReviewScreen   `json:"screens"`
	ID        string           `json:"id"`
	Session   string           `json:"session"`
	Repo      string           `json:"repo"`
	PR        int              `json:"pr"`
	CreatedAt time.Time        `json:"createdAt"`
	Artifacts []ReviewArtifact `json:"artifacts"`
}

type ReviewScreen struct {
	Title    string `json:"title"`
	Caption  string `json:"caption,omitempty"`
	Before   string `json:"before,omitempty"`
	After    string `json:"after"`
	URL      string `json:"url,omitempty"`
	Viewport string `json:"viewport,omitempty"`
	Selector string `json:"selector,omitempty"`
}

type ReviewArtifact struct {
	ID     string `json:"id"`
	MIME   string `json:"mime"`
	Bytes  int    `json:"bytes"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type ReviewImageArgs struct {
	Package  string `json:"package"`
	Artifact string `json:"artifact"`
}

type ReviewImageData struct {
	DataURL string `json:"dataURL"`
}
