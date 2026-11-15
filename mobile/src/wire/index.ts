// The public surface of the wire package.
//
// mobile/src/wire is the client's view of internal/protocol: a hand-maintained
// mirror of the types, a codec that reproduces the daemon's bytes exactly, the
// platform-free Transport seam the native plugin implements, and the
// request/response correlator every command goes through.
//
// The golden vectors in ./testdata/*.json are read by BOTH this package's tests
// and internal/protocol/goldenvectors_test.go: they keep Go, TypeScript and
// Swift from drifting apart on a device where nothing can be debugged.

export * from "./limits";
export * from "./protocol";
export * from "./panes";
export * from "./codec";
export * from "./correlator";
export * from "./events";
export type * from "./push";
export * from "./tiers";
export * from "./transport";
export { FakeChannel } from "./fake";
