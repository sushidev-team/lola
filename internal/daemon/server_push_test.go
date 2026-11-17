package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
)

func TestUnixSocketWatchRefused(t *testing.T) {
	d, _ := talkFakeDaemon(t)
	for _, cmd := range []string{protocol.CmdWatch, protocol.CmdUnwatch} {
		args, _ := json.Marshal(protocol.WatchArgs{Topics: []string{"sessions"}})
		resp := d.handle(context.Background(), protocol.Request{Cmd: cmd, Args: args})
		if resp.OK || resp.Error != "watch needs a stream connection" {
			t.Fatalf("%s: %+v", cmd, resp)
		}
	}
}

func TestHandlePushIgnoresOtherCommands(t *testing.T) {
	d, _ := talkFakeDaemon(t)
	if _, ok := d.handlePush(context.Background(), protocol.Request{Cmd: "sessions"}); ok {
		t.Fatal("handlePush took a command that is not its own")
	}
}

func TestAttachCommandsRouteThroughHandle(t *testing.T) {
	e := newAttachEnv(t)
	args, _ := json.Marshal(protocol.AttachImageArgs{Session: e.id, Name: "upload-key-1", Mime: "image/png", Data: pngBytes(64), Last: true})
	resp := e.d.handle(context.Background(), protocol.Request{Cmd: protocol.CmdAttachImage, Args: args})
	var data protocol.AttachImageData
	if !resp.OK || json.Unmarshal(resp.Data, &data) != nil || data.Path == "" {
		t.Fatalf("%+v", resp)
	}
	resp = e.d.handle(context.Background(), protocol.Request{Cmd: protocol.CmdAttachImage, Args: json.RawMessage(`{"data":5}`)})
	if resp.OK || resp.Error != "bad request" {
		t.Fatalf("%+v", resp)
	}
	args, _ = json.Marshal(protocol.AttachmentImageArgs{Session: e.id, Path: data.Path})
	resp = e.d.handle(context.Background(), protocol.Request{Cmd: protocol.CmdAttachmentImage, Args: args})
	if !resp.OK {
		t.Fatalf("%+v", resp)
	}
}
