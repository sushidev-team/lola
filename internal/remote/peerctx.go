package remote

import "context"

type peerKey struct{}

// WithPeer returns ctx carrying the authenticated peer of the connection a
// request arrived on. Options.Handle runs under it, so a handler that needs the
// device (attachImage keys uploads by device) reads it from the context rather
// than from anything the peer sent. A unix-socket request has no peer.
func WithPeer(ctx context.Context, p Peer) context.Context {
	return context.WithValue(ctx, peerKey{}, p)
}

// PeerFromContext returns the peer WithPeer stored.
func PeerFromContext(ctx context.Context) (Peer, bool) {
	p, ok := ctx.Value(peerKey{}).(Peer)
	return p, ok
}
