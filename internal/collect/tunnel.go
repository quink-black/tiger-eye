package collect

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"time"

	"github.com/quink/tiger-eye/internal/config"
)

// tunnel manages a persistent `ssh -L` local forward to a remote node that
// binds loopback only. Reverse tunnels (-R) are deliberately not used: DC
// policy bans them, and a forward keeps the secure posture (the node is never
// network-exposed; only this collector, holding ssh access, can reach it).
type tunnel struct {
	host      config.Host
	localPort int
	cmd       *exec.Cmd
}

// startTunnel allocates a free local port and launches `ssh -N -L
// localPort:127.0.0.1:remotePort <alias>`. The ssh alias is expected to encode
// any jump host via the user's ~/.ssh/config. It returns the local base URL the
// puller should hit.
func startTunnel(ctx context.Context, h config.Host) (*tunnel, string, error) {
	lp, err := freePort()
	if err != nil {
		return nil, "", err
	}
	fwd := fmt.Sprintf("%d:127.0.0.1:%d", lp, h.Port)
	// -N: no remote command; -T: no tty; ServerAlive keeps the link up and
	// detects dead peers so the supervisor can respawn.
	args := []string{
		"-N", "-T",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "ExitOnForwardFailure=yes",
		"-L", fwd,
		h.SSH,
	}
	cmd := exec.CommandContext(ctx, "ssh", args...)
	if err := cmd.Start(); err != nil {
		return nil, "", err
	}
	t := &tunnel{host: h, localPort: lp, cmd: cmd}
	baseURL := "http://127.0.0.1:" + strconv.Itoa(lp)

	// Wait briefly for the forward to become connectable before first poll.
	if err := waitDial(baseURL, lp, 5*time.Second); err != nil {
		// Kill alone leaves a zombie; Wait reaps the ssh process so the
		// supervisor can respawn without leaking entries in the process
		// table.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, "", fmt.Errorf("ssh -L to %s did not come up: %w", h.Name, err)
	}
	return t, baseURL, nil
}

// wait blocks until the ssh process exits (tunnel died), so the supervisor can
// respawn it.
func (t *tunnel) wait() error { return t.cmd.Wait() }

// close terminates the ssh process and ensures its wait goroutine has reaped
// it. The waited channel is the buffered(1) channel that receives
// tun.wait()'s result. After Kill, Wait returns promptly, so blocking here
// does not hang.
//
// If the poll-loop select already consumed the value (tunnel exited on its
// own), the goroutine has finished and the buffer is empty. In that case the
// process is already reaped and Kill is a harmless no-op; we still must not
// block on an empty, never-closed channel, so we use a non-blocking receive
// as a fallback.
func (t *tunnel) close(waited <-chan error) {
	_ = t.cmd.Process.Kill()
	select {
	case <-waited:
	default:
	}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func waitDial(_ string, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := "127.0.0.1:" + strconv.Itoa(port)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			c.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("dial %s timed out", addr)
}
