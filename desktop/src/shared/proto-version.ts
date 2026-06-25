// Wire-protocol version shared by the main process (host-client/Hello) and the
// renderer (version-mismatch banner). Kept in its own node-free module so the
// renderer can import the value without pulling main-process `node:` deps into
// the browser bundle. Must match `Version` in session/winhost/proto/proto.go.
export const PROTO_VERSION = 17;
