package windows

import (
	"context"
	"net"

	winio "github.com/Microsoft/go-winio"
)

const (
	ControlPipePath = `\\.\pipe\DesktopGuardPro.Control.v1`

	DefaultPipeSecurityDescriptor = "D:P" +
		"(A;;GA;;;SY)" +
		"(A;;GA;;;BA)" +
		"(A;;GRGW;;;IU)"
)

func ListenControlPipe() (net.Listener, error) {
	return ListenPipe(ControlPipePath)
}

func ListenPipe(path string) (net.Listener, error) {
	return winio.ListenPipe(path, &winio.PipeConfig{
		SecurityDescriptor: DefaultPipeSecurityDescriptor,
		MessageMode:        false,
		InputBufferSize:    64 * 1024,
		OutputBufferSize:   64 * 1024,
	})
}

func DialPipe(ctx context.Context, path string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, path)
}
