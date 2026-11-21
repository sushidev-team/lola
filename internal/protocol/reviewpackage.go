package protocol

import (
	"encoding/json"
	"time"

	"github.com/sushidev-team/lola/internal/goalpolicy"
)

const (
	CmdReviewCapture  = "reviewPackageCapture"  // explicit human browser capture; new immutable version
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
	Pages     []ReviewPage     `json:"pages,omitempty"`
	ID        string           `json:"id"`
	Session   string           `json:"session"`
	Repo      string           `json:"repo"`
	PR        int              `json:"pr"`
	CreatedAt time.Time        `json:"createdAt"`
	Artifacts []ReviewArtifact `json:"artifacts"`
}

// ReviewPage is a live dev-server page the reviewer can open in the preview.
type ReviewPage struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Caption string `json:"caption,omitempty"`
	ReviewPrep
}

type ReviewScreen struct {
	Title    string `json:"title"`
	Caption  string `json:"caption,omitempty"`
	Before   string `json:"before,omitempty"`
	After    string `json:"after"`
	URL      string `json:"url,omitempty"`
	Viewport string `json:"viewport,omitempty"`
	Selector string `json:"selector,omitempty"`
	ReviewPrep
}

// ReviewPrep is how a reviewer's view of a page is prepared (reviewpack.Prep):
// agent-provided, declarative, bounded.
type ReviewPrep struct {
	Device  string       `json:"device,omitempty"`
	WaitFor string       `json:"waitFor,omitempty"`
	ScrollY int          `json:"scrollY,omitempty"`
	Marks   []ReviewMark `json:"marks,omitempty"`
	Steps   []ReviewStep `json:"steps,omitempty"`
}

// ReviewStep is one action on a prepared page (reviewpack.Step): click, fill,
// press, wait or goto, run only on a local host.
type ReviewStep struct {
	Do       string `json:"do"`
	Selector string `json:"selector,omitempty"`
	Text     string `json:"text,omitempty"`
	Value    string `json:"value,omitempty"`
	Key      string `json:"key,omitempty"`
	Path     string `json:"path,omitempty"`
	Ms       int    `json:"ms,omitempty"`
}

type ReviewMark struct {
	Selector string `json:"selector,omitempty"`
	Text     string `json:"text,omitempty"`
	Label    string `json:"label,omitempty"`
}

type ReviewArtifact struct {
	ID     string `json:"id"`
	MIME   string `json:"mime"`
	Bytes  int    `json:"bytes"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// ReviewImageArgs names one retained screenshot. Without Chunked the answer is
// the whole image as a data URL (the desktop's read; up to MaxImage, over a
// phone's frame cap). With Chunked it is one ReviewImageChunkBytes part from
// Offset, which must be a multiple of the chunk size.
type ReviewImageArgs struct {
	Package  string `json:"package"`
	Artifact string `json:"artifact"`
	Chunked  bool   `json:"chunked,omitempty"`
	Offset   int64  `json:"offset,omitempty"`
}

// ReviewImageData is the whole image (DataURL) or, for a chunked read, one
// part: Mime and Size describe the whole file, Data is at most
// ReviewImageChunkBytes from Offset, and EOF marks the last part.
type ReviewImageData struct {
	DataURL string `json:"dataURL"`
	Mime    string `json:"mime,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Offset  int64  `json:"offset,omitempty"`
	Data    []byte `json:"data,omitempty"`
	EOF     bool   `json:"eof,omitempty"`
}

// ReviewImageChunkBytes is the raw size of one chunked screenshot part: its
// base64 fits a remote frame, like an attachment part.
const ReviewImageChunkBytes = AttachChunkBytes

// Screen is -1 to add a screen, otherwise the existing screen's index to update.
// Paths and revision identities are resolved by the daemon, never the caller.
type ReviewCaptureArgs struct {
	Package  string `json:"package"`
	Screen   int    `json:"screen"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	Selector string `json:"selector"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Channel  string `json:"channel"`
}
