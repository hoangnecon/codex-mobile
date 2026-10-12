package tunnel

import (
	"bufio"
	"fmt"
	"log"
	"os/exec"
	"strings"

	"github.com/skip2/go-qrcode"
)

// PrintQRCode prints an ASCII QR code to the terminal for easy mobile access.
func PrintQRCode(url string) {
	qr, err := qrcode.New(url, qrcode.Medium)
	if err != nil {
		return
	}
	fmt.Println()
	fmt.Println("📱 Scan with phone to open Codex Mobile:")
	fmt.Println(qr.ToSmallString(false))
	fmt.Printf("   URL: %s\n\n", url)
}

// StartCloudflared launches a background cloudflared tunnel if available.
func StartCloudflared(localPort int) (string, func(), error) {
	bin, err := exec.LookPath("cloudflared")
	if err != nil {
		return "", nil, fmt.Errorf("cloudflared not found in PATH")
	}

	cmd := exec.Command(bin, "tunnel", "--url", fmt.Sprintf("http://localhost:%d", localPort))
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", nil, err
	}

	if err := cmd.Start(); err != nil {
		return "", nil, err
	}

	tunnelURLChan := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "trycloudflare.com") {
				parts := strings.Fields(line)
				for _, p := range parts {
					if strings.HasPrefix(p, "https://") && strings.Contains(p, "trycloudflare.com") {
						select {
						case tunnelURLChan <- p:
						default:
						}
					}
				}
			}
		}
	}()

	cleanup := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}

	select {
	case u := <-tunnelURLChan:
		log.Printf("[tunnel] Cloudflare Tunnel established: %s", u)
		return u, cleanup, nil
	case <-cmdProcessDone(cmd):
		return "", cleanup, fmt.Errorf("cloudflared exited prematurely")
	}
}

func cmdProcessDone(cmd *exec.Cmd) <-chan error {
	ch := make(chan error, 1)
	go func() {
		ch <- cmd.Wait()
	}()
	return ch
}
