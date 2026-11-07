package protocol

import (
	"encoding/json"
	"time"
)

const (
	CmdReviewPublish  = "reviewPackagePublish" // agent-side, bound to Request.Session
	CmdReviewPackages = "reviewPackages"       // human-side, retained metadata
	CmdReviewImage    = "reviewPackageImage"   // human-side, retained image only
)

type ReviewPublishArgs struct {
	Manifest json.RawMessage `json:"manifest"`      // validated by reviewpack, not the transport
	App      string          `json:"app,omitempty"` // a member of this session's stack
}

type ReviewPackagesData struct {
	Packages []ReviewPackage `json:"packages"`
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
