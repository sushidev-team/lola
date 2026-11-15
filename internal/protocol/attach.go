package protocol

// Image attachments over the remote link. A phone uploads an image in parts into
// the session's <worktree>/.lola/images folder and reads one back in chunks.
// Contract: docs/mobile/02-events.md §12.

// AttachImageArgs is one part of an upload.
type AttachImageArgs struct {
	Session string `json:"session"`
	Name    string `json:"name"` // the UPLOAD KEY, ^[A-Za-z0-9_-]{8,64}$, client-random; NOT a filename
	Mime    string `json:"mime"` // a key of AttachMimes
	Data    []byte `json:"data"` // base64 on the wire; 1..AttachChunkBytes
	Part    int    `json:"part"` // 0-based, strictly sequential
	Last    bool   `json:"last,omitempty"`
}

// AttachImageData answers a part. A non-last part reports Received only; the
// last reports Path, the same token the desktop's ImageAttachment carries.
type AttachImageData struct {
	Received int64  `json:"received"`
	Path     string `json:"path,omitempty"`  // <worktree>/.lola/images/paste-<16 hex><ext>
	Thumb    string `json:"thumb,omitempty"` // always "" from the daemon; the client has the pixels
}

const (
	AttachChunkBytes = 512 << 10 // raw bytes per part; base64 is about 683 KiB, under MaxFrameBytes
	AttachMaxBytes   = 20 << 20  // the desktop's maxImageBytes
	AttachMaxParts   = AttachMaxBytes / AttachChunkBytes
)

// AttachMimes maps an accepted mime type to the extension it is stored under.
var AttachMimes = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// Refusal sentences of attachImage (Response.Error). Fixed; never built from
// input. The ones marked "desktop" are the desktop's own sentences.
const (
	AttachErrNoFolder = "this session has no folder to put an image in"                      // desktop
	AttachErrGone     = "this session's folder is gone"                                      // desktop
	AttachErrLink     = "refusing to write an image through a link in this session's folder" // desktop
	AttachErrTooLarge = "that image is over 20 MB"                                           // desktop
	AttachErrType     = "that file is not an image"                                          // desktop
	AttachErrOrder    = "image upload out of order"
	AttachErrBusy     = "too many images uploading"
	AttachErrExpired  = "image upload expired"
	AttachErrPin      = "this sandboxed session cannot take images"
	AttachErrQuota    = "no room for more images in this session today"
	AttachErrDisk     = "the disk is too full to take an image"
)

// AttachmentImageArgs reads one chunk of an attachment back.
type AttachmentImageArgs struct {
	Session string `json:"session"`
	Path    string `json:"path"`             // the token as it appears in the message
	Offset  int64  `json:"offset,omitempty"` // a multiple of AttachChunkBytes
}

// AttachmentImageData is one chunk.
type AttachmentImageData struct {
	Mime   string `json:"mime"` // sniffed, one of AttachMimes
	Size   int64  `json:"size"`
	Offset int64  `json:"offset"`
	Data   []byte `json:"data"` // at most AttachChunkBytes
	EOF    bool   `json:"eof,omitempty"`
}

// Refusal sentences of attachmentImage (Response.Error), the desktop's own.
const (
	AttachErrNotHere           = "this is not an attachment in this session"
	AttachErrFolderUnavailable = "this session's image folder is unavailable"
	AttachErrUnavailable       = "this image is no longer available"
	AttachErrUnreadable        = "could not read this image"
	AttachErrNotImage          = "this attachment is not an image"
)
