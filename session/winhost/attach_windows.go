//go:build windows

package winhost

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"hangar/session/winhost/proto"

	"github.com/Microsoft/go-winio"
	"github.com/muesli/cancelreader"
	"golang.org/x/sys/windows"
	"golang.org/x/term"
)

// consoleSize returns the current console size, defaulting to 80x24.
func consoleSize() (int, int) {
	if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 && h > 0 {
		return w, h
	}
	return 80, 24
}

// consoleRestore holds the console handles + original modes so they can be put
// back after an attach.
type consoleRestore struct {
	cin, cout       windows.Handle
	inMode, outMode uint32
}

func openConsoleHandle(name string) (windows.Handle, error) {
	p, _ := windows.UTF16PtrFromString(name)
	return windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
}

// enterRawConsole puts the console into raw + virtual-terminal mode (the
// bubbletea tty_windows.go pattern) so keystrokes pass through to the agent and
// its VT output renders. The returned restore() reverts everything.
func enterRawConsole() (*consoleRestore, error) {
	cin, err := openConsoleHandle("CONIN$")
	if err != nil {
		return nil, fmt.Errorf("open CONIN$: %w", err)
	}
	cout, err := openConsoleHandle("CONOUT$")
	if err != nil {
		windows.CloseHandle(cin)
		return nil, fmt.Errorf("open CONOUT$: %w", err)
	}
	var inMode, outMode uint32
	if err := windows.GetConsoleMode(cin, &inMode); err != nil {
		windows.CloseHandle(cin)
		windows.CloseHandle(cout)
		return nil, err
	}
	if err := windows.GetConsoleMode(cout, &outMode); err != nil {
		windows.CloseHandle(cin)
		windows.CloseHandle(cout)
		return nil, err
	}
	const rawClear = windows.ENABLE_LINE_INPUT | windows.ENABLE_ECHO_INPUT | windows.ENABLE_PROCESSED_INPUT
	_ = windows.SetConsoleMode(cin, (inMode&^rawClear)|windows.ENABLE_VIRTUAL_TERMINAL_INPUT)
	_ = windows.SetConsoleMode(cout, outMode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING|windows.DISABLE_NEWLINE_AUTO_RETURN)
	return &consoleRestore{cin: cin, cout: cout, inMode: inMode, outMode: outMode}, nil
}

func (r *consoleRestore) restore() {
	_ = windows.SetConsoleMode(r.cin, r.inMode)
	_ = windows.SetConsoleMode(r.cout, r.outMode)
	windows.CloseHandle(r.cin)
	windows.CloseHandle(r.cout)
}

// Attach connects this terminal to the live session: it takes over the console
// (raw + VT mode), repaints from the host's emulator snapshot, streams output to
// stdout and keystrokes to the agent, and detaches on Ctrl-Q. The returned
// channel is closed when the user detaches (or the agent exits).
//
// The caller blocks on the channel while attached (mirroring the tmux backend),
// which pauses the bubbletea update loop so it stops consuming console input.
func (s *Session) Attach() (chan struct{}, error) {
	cols, rows := consoleSize()
	var pipe, token string
	if err := withClient(func(c *Client) error {
		p, t, err := c.Attach(s.name, cols, rows)
		pipe, token = p, t
		return err
	}); err != nil {
		return nil, err
	}

	to := 5 * time.Second
	conn, err := winio.DialPipe(pipe, &to)
	if err != nil {
		return nil, fmt.Errorf("dial attach pipe: %w", err)
	}
	if err := proto.WriteRawFrame(conn, []byte(token)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("attach auth: %w", err)
	}

	rc, err := enterRawConsole()
	if err != nil {
		conn.Close()
		return nil, err
	}
	cr, err := cancelreader.NewReader(os.Stdin)
	if err != nil {
		rc.restore()
		conn.Close()
		return nil, err
	}

	attachCh := make(chan struct{})
	var once sync.Once
	detach := func() {
		once.Do(func() {
			// Fast path: cancel the stdin read. Guaranteed path: closing the pipe
			// unblocks the output copy and fails the next input write, so detach
			// always completes even if Cancel() can't (the documented VT-input case).
			cr.Cancel()
			_ = conn.Close()
			rc.restore()
			close(attachCh)
		})
	}

	// Output: host -> stdout. Ends when the host closes (agent exited / detach).
	go func() {
		_, _ = io.Copy(os.Stdout, conn)
		detach()
	}()

	// Input: stdin -> host. Ctrl-Q (0x11) detaches; everything else is forwarded.
	go func() {
		defer cr.Close()
		buf := make([]byte, 256)
		for {
			n, rerr := cr.Read(buf)
			if n == 1 && buf[0] == 0x11 {
				detach()
				return
			}
			if n > 0 {
				if _, werr := conn.Write(buf[:n]); werr != nil {
					detach()
					return
				}
			}
			if rerr != nil {
				detach()
				return
			}
		}
	}()

	return attachCh, nil
}
