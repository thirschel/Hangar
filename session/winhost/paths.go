package winhost

import (
	"path/filepath"

	"hangar/config"
)

// Host state files live alongside the rest of hangar's state in
// ~/.hangar. These helpers are platform-neutral; the host itself is
// Windows-only.
const (
	hostLockName = "host.lock"
	hostInfoName = "host.json"
	hostLogName  = "host.log"
)

func hostStatePath(name string) (string, error) {
	dir, err := config.GetConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

func hostLockPath() (string, error) { return hostStatePath(hostLockName) }
func hostInfoPath() (string, error) { return hostStatePath(hostInfoName) }
func hostLogPath() (string, error)  { return hostStatePath(hostLogName) }

// hostInfo is persisted to host.json so a client can discover and validate the
// running host. PID alone is insufficient (PIDs are reused), so we also store
// the process creation time and a random nonce.
type hostInfo struct {
	PipeName    string `json:"pipeName"`
	PID         int    `json:"pid"`
	CreatedUnix int64  `json:"createdUnix"`
	Nonce       string `json:"nonce"`
	Version     int    `json:"version"`
}
