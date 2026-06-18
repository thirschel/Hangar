//go:build windows

package winhost

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"hangar/session/winhost/proto"

	"golang.org/x/sys/windows"
)

const helloProofPrefix = "hangar-winhost-hello-v7\n"

var (
	errHostAuthFailed       = errors.New("session-host authentication failed")
	errHostIdentityMismatch = errors.New("session-host identity mismatch")
	errUntrustedHostInfo    = errors.New("untrusted session-host state")
	errUnauthenticatedPipe  = errors.New("unauthenticated session-host pipe exists")

	processCreationUnix           = currentProcessCreationUnix
	nonceReader         io.Reader = rand.Reader
)

type hostIdentity struct {
	PipeName    string
	PID         int
	CreatedUnix int64
	Nonce       string
	Version     int
}

func newHostIdentity(pipe string) (*hostIdentity, error) {
	created, err := processCreationUnix(os.Getpid())
	if err != nil {
		return nil, fmt.Errorf("read host process creation time: %w", err)
	}
	nonce, err := randomNonceHex(32)
	if err != nil {
		return nil, err
	}
	return &hostIdentity{
		PipeName:    pipe,
		PID:         os.Getpid(),
		CreatedUnix: created,
		Nonce:       nonce,
		Version:     proto.Version,
	}, nil
}

func (id *hostIdentity) hostInfo() hostInfo {
	return hostInfo{
		PipeName:    id.PipeName,
		PID:         id.PID,
		CreatedUnix: id.CreatedUnix,
		Nonce:       id.Nonce,
		Version:     id.Version,
	}
}

func randomNonceHex(nbytes int) (string, error) {
	b := make([]byte, nbytes)
	if _, err := io.ReadFull(nonceReader, b); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func validLowerHexBytes(s string, nbytes int) bool {
	if len(s) != nbytes*2 {
		return false
	}
	for _, r := range s {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			continue
		}
		return false
	}
	return true
}

func hostNonceProof(nonce, clientNonce string, hostVersion, hostPID int, hostCreatedUnix int64, pipeName string) (string, error) {
	if !validLowerHexBytes(nonce, 32) {
		return "", fmt.Errorf("%w: invalid host nonce", errUntrustedHostInfo)
	}
	if !validLowerHexBytes(clientNonce, 32) {
		return "", fmt.Errorf("%w: invalid hello challenge", errHostAuthFailed)
	}
	key, err := hex.DecodeString(nonce)
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("%s%s\n%d\n%d\n%d\n%s", helloProofPrefix, clientNonce, hostVersion, hostPID, hostCreatedUnix, pipeName)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func hostNonceProofForIdentity(id *hostIdentity, clientNonce string) (string, error) {
	return hostNonceProof(id.Nonce, clientNonce, id.Version, id.PID, id.CreatedUnix, id.PipeName)
}

func readHostInfoFile() (*hostInfo, error) {
	p, err := hostInfoPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var hi hostInfo
	if err := json.Unmarshal(data, &hi); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return &hi, nil
}

func validateHostInfoForAuth(hi *hostInfo, expectedPipe string, creationUnix func(int) (int64, error)) error {
	if hi == nil {
		return errUntrustedHostInfo
	}
	if hi.PipeName != expectedPipe {
		return fmt.Errorf("%w: unexpected pipe name", errUntrustedHostInfo)
	}
	if hi.Version != proto.Version {
		return &VersionMismatch{HostVersion: hi.Version, ClientVersion: proto.Version}
	}
	if !validLowerHexBytes(hi.Nonce, 32) {
		return fmt.Errorf("%w: invalid nonce", errUntrustedHostInfo)
	}
	if hi.PID <= 0 || hi.CreatedUnix <= 0 {
		return fmt.Errorf("%w: invalid pid or creation time", errUntrustedHostInfo)
	}
	got, err := creationUnix(hi.PID)
	if err != nil {
		return fmt.Errorf("%w: process creation check failed: %v", errUntrustedHostInfo, err)
	}
	if d := got - hi.CreatedUnix; d < -1 || d > 1 {
		return fmt.Errorf("%w: pid/creation mismatch", errUntrustedHostInfo)
	}
	return nil
}

func loadHostInfoForAuth() (*hostInfo, error) {
	hi, err := readHostInfoFile()
	if err != nil {
		return nil, err
	}
	expectedPipe, err := controlPipeName()
	if err != nil {
		return nil, err
	}
	if err := validateHostInfoForAuth(hi, expectedPipe, processCreationUnix); err != nil {
		return nil, err
	}
	return hi, nil
}

func verifyAuthenticatedHello(hi *hostInfo, clientNonce string, r *proto.Response) error {
	if r == nil {
		return errHostAuthFailed
	}
	if !r.OK {
		if r.Error == "" {
			return errHostAuthFailed
		}
		return errors.New(r.Error)
	}
	if r.HostVersion != proto.Version {
		return &VersionMismatch{HostVersion: r.HostVersion, ClientVersion: proto.Version}
	}
	if r.HostPID != hi.PID || r.HostCreatedUnix != hi.CreatedUnix {
		return errHostIdentityMismatch
	}
	if !validLowerHexBytes(r.HostNonceProof, sha256.Size) {
		return errHostAuthFailed
	}
	want, err := hostNonceProof(hi.Nonce, clientNonce, r.HostVersion, r.HostPID, r.HostCreatedUnix, hi.PipeName)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(r.HostNonceProof), []byte(want)) != 1 {
		return errHostAuthFailed
	}
	return nil
}

func currentProcessCreationUnix(pid int) (int64, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("invalid pid %d", pid)
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(h)

	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return 0, err
	}
	return filetimeUnixSeconds(creation), nil
}

func filetimeUnixSeconds(ft windows.Filetime) int64 {
	const windowsToUnix100ns = 116444736000000000
	ticks := (uint64(ft.HighDateTime) << 32) | uint64(ft.LowDateTime)
	if ticks < windowsToUnix100ns {
		return 0
	}
	return int64((ticks - windowsToUnix100ns) / uint64(time.Second/100))
}
