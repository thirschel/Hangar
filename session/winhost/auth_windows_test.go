//go:build windows

package winhost

import (
	"bytes"
	"errors"
	"testing"

	"claude-squad/session/winhost/proto"
)

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}

func testHostInfo() *hostInfo {
	return &hostInfo{
		PipeName:    `\\.\pipe\claudesquad-host-test`,
		PID:         4242,
		CreatedUnix: 1710000000,
		Nonce:       "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
		Version:     proto.Version,
	}
}

func TestAuthHandshakeAcceptsCorrectProof(t *testing.T) {
	hi := testHostInfo()
	clientNonce := "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd"
	proof, err := hostNonceProof(hi.Nonce, clientNonce, proto.Version, hi.PID, hi.CreatedUnix, hi.PipeName)
	if err != nil {
		t.Fatalf("proof: %v", err)
	}
	resp := &proto.Response{
		OK:              true,
		HostVersion:     proto.Version,
		HostPID:         hi.PID,
		HostCreatedUnix: hi.CreatedUnix,
		HostNonceProof:  proof,
	}
	if err := verifyAuthenticatedHello(hi, clientNonce, resp); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestAuthHandshakeRejectsWrongNonce(t *testing.T) {
	hi := testHostInfo()
	clientNonce := "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd"
	wrongNonce := "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	proof, err := hostNonceProof(wrongNonce, clientNonce, proto.Version, hi.PID, hi.CreatedUnix, hi.PipeName)
	if err != nil {
		t.Fatalf("proof: %v", err)
	}
	resp := &proto.Response{
		OK:              true,
		HostVersion:     proto.Version,
		HostPID:         hi.PID,
		HostCreatedUnix: hi.CreatedUnix,
		HostNonceProof:  proof,
	}
	if err := verifyAuthenticatedHello(hi, clientNonce, resp); !errors.Is(err, errHostAuthFailed) {
		t.Fatalf("verify err = %v, want errHostAuthFailed", err)
	}
}

func TestAuthHandshakeRejectsVersionMismatch(t *testing.T) {
	hi := testHostInfo()
	clientNonce := "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd"
	resp := &proto.Response{OK: true, HostVersion: proto.Version - 1, HostPID: hi.PID, HostCreatedUnix: hi.CreatedUnix}
	err := verifyAuthenticatedHello(hi, clientNonce, resp)
	var vm *VersionMismatch
	if !errors.As(err, &vm) {
		t.Fatalf("verify err = %v, want VersionMismatch", err)
	}

	wrongVersion := *hi
	wrongVersion.Version = proto.Version - 1
	err = validateHostInfoForAuth(&wrongVersion, hi.PipeName, func(int) (int64, error) {
		return hi.CreatedUnix, nil
	})
	if !errors.As(err, &vm) {
		t.Fatalf("host.json version err = %v, want VersionMismatch", err)
	}
}

func TestValidateHostInfoRejectsWrongPIDAndCreatedUnix(t *testing.T) {
	hi := testHostInfo()
	if err := validateHostInfoForAuth(hi, hi.PipeName, func(pid int) (int64, error) {
		if pid != hi.PID {
			t.Fatalf("pid = %d, want %d", pid, hi.PID)
		}
		return hi.CreatedUnix, nil
	}); err != nil {
		t.Fatalf("validate good host info: %v", err)
	}

	wrongPID := *hi
	wrongPID.PID = hi.PID + 1
	if err := validateHostInfoForAuth(&wrongPID, hi.PipeName, func(int) (int64, error) {
		return 0, errors.New("process not found")
	}); !errors.Is(err, errUntrustedHostInfo) {
		t.Fatalf("wrong pid err = %v, want errUntrustedHostInfo", err)
	}

	wrongCreated := *hi
	wrongCreated.CreatedUnix = hi.CreatedUnix + 10
	if err := validateHostInfoForAuth(&wrongCreated, hi.PipeName, func(int) (int64, error) {
		return hi.CreatedUnix, nil
	}); !errors.Is(err, errUntrustedHostInfo) {
		t.Fatalf("wrong created err = %v, want errUntrustedHostInfo", err)
	}
}

func TestRandomNonceHexReadFailure(t *testing.T) {
	oldReader := nonceReader
	nonceReader = errReader{}
	t.Cleanup(func() { nonceReader = oldReader })

	nonce, err := randomNonceHex(8)
	if err == nil {
		t.Fatal("expected error from randomNonceHex when entropy source fails")
	}
	if nonce != "" {
		t.Fatalf("nonce = %q, want empty string on failure", nonce)
	}
}

func TestRandomNonceHexReturnsRequestedBytes(t *testing.T) {
	oldReader := nonceReader
	nonceReader = bytes.NewReader(bytes.Repeat([]byte{0xab}, 8))
	t.Cleanup(func() { nonceReader = oldReader })

	nonce, err := randomNonceHex(8)
	if err != nil {
		t.Fatalf("randomNonceHex returned error: %v", err)
	}
	if nonce != "abababababababab" {
		t.Fatalf("nonce = %q, want %q", nonce, "abababababababab")
	}
}
