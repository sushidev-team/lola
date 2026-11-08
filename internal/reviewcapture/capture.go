// Package reviewcapture implements optional, explicit local browser capture.
// It is a CLI helper, never a daemon endpoint or independent commit proof.
package reviewcapture

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/png"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/reviewpack"
)

//go:embed capture.mjs
var script string

type Options struct {
	URL         string `json:"url"`
	Selector    string `json:"selector"`
	Channel     string `json:"channel,omitempty"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Destination string `json:"destination"`
	Title       string `json:"-"`
}

func (o Options) Validate() error {
	if o.Channel != "" && o.Channel != "chrome" && o.Channel != "msedge" {
		return errors.New("capture channel must be chrome or msedge, or omitted for Playwright Chromium")
	}
	u, err := url.Parse(o.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || len(o.URL) > 2048 {
		return errors.New("capture requires an HTTP(S) URL without embedded credentials")
	}
	if o.Width < 240 || o.Height < 240 || o.Width > 4096 || o.Height > 4096 {
		return errors.New("capture width and height must be 240–4096 pixels")
	}
	if len(o.Selector) > 500 || strings.TrimSpace(o.Title) == "" || len(o.Title) > 200 {
		return errors.New("capture requires a title up to 200 bytes and a selector up to 500 bytes")
	}
	if !filepath.IsLocal(o.Destination) || filepath.Clean(o.Destination) != o.Destination || !strings.HasSuffix(o.Destination, ".png") {
		return errors.New("capture output must be a clean checkout-relative .png path")
	}
	return nil
}

type Run func(context.Context, string, string, []byte) ([]byte, error)

func Capture(ctx context.Context, workspace string, o Options, run Run) (reviewpack.Screen, error) {
	if err := o.Validate(); err != nil {
		return reviewpack.Screen{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	tmp, err := os.MkdirTemp("", "lola-review-capture-")
	if err != nil {
		return reviewpack.Screen{}, err
	}
	defer os.RemoveAll(tmp)
	input := o
	input.Destination = filepath.Join(tmp, "capture.png")
	b, _ := json.Marshal(input)
	if run == nil {
		run = runNode
	}
	out, err := run(ctx, workspace, script, b)
	if err != nil {
		return reviewpack.Screen{}, err
	}
	var result struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return reviewpack.Screen{}, err
	}
	resolved := o
	resolved.URL = result.URL
	if err := resolved.Validate(); err != nil {
		return reviewpack.Screen{}, err
	}
	info, err := os.Lstat(input.Destination)
	if err != nil {
		return reviewpack.Screen{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > reviewpack.MaxImage {
		return reviewpack.Screen{}, errors.New("capture exceeds the 4 MiB image limit")
	}
	data, err := os.ReadFile(input.Destination)
	if err != nil {
		return reviewpack.Screen{}, err
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != "png" || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 20_000_000 {
		return reviewpack.Screen{}, errors.New("capture is not a valid bounded PNG")
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return reviewpack.Screen{}, err
	}
	defer root.Close()
	if err := root.MkdirAll(filepath.Dir(o.Destination), 0700); err != nil {
		return reviewpack.Screen{}, err
	}
	// O_EXCL preserves existing evidence; OpenRoot confines symlink resolution.
	f, err := root.OpenFile(o.Destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return reviewpack.Screen{}, err
	}
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return reviewpack.Screen{}, err
	}
	if closeErr != nil {
		return reviewpack.Screen{}, closeErr
	}
	return reviewpack.Screen{Title: o.Title, After: o.Destination, URL: result.URL, Viewport: fmt.Sprintf("%d × %d", o.Width, o.Height), Selector: o.Selector}, nil
}

func runNode(ctx context.Context, dir, script string, input []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "node", "-e", script)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(input)
	cmd.WaitDelay = 2 * time.Second
	containProcess(cmd)
	out := &limitedOutput{}
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("browser capture: %w: %s", err, out.String())
	}
	return out.Bytes(), nil
}

type limitedOutput struct{ bytes.Buffer }

func (b *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	_, _ = b.Buffer.Write(p[:min(n, max(0, 8192-b.Len()))])
	return n, nil
}
