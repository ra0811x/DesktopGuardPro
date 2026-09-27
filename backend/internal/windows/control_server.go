package windows

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	"desktopguardpro/internal/contracts"
	"desktopguardpro/internal/ipc"
	coreservice "desktopguardpro/internal/service"
)

const controlIOTimeout = 5 * time.Second

func ServeControl(
	ctx context.Context,
	listener net.Listener,
	api *coreservice.API,
) error {
	watcherDone := make(chan struct{})
	defer close(watcherDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = listener.Close()
		case <-watcherDone:
		}
	}()

	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}

		go func() {
			defer connection.Close()
			identity, err := IdentifyPipeClient(connection)
			if err != nil {
				return
			}
			_ = ServeControlConnection(ctx, connection, api, identity)
		}()
	}
}

func ServeControlConnection(
	ctx context.Context,
	connection net.Conn,
	api *coreservice.API,
	identity PipeClientIdentity,
) error {
	client := coreservice.ClientIdentity{
		ProcessID: identity.ProcessID, WindowsSessionID: identity.WindowsSessionID,
		UserSID: identity.UserSID, ImagePath: identity.ImagePath,
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	defer connection.Close()
	requests := make(chan contracts.Message)
	go func() {
		deadline := time.Now().Add(controlIOTimeout)
		for {
			if err := connection.SetReadDeadline(deadline); err != nil {
				cancel(err)
				return
			}
			request, err := ipc.ReadMessage(connection)
			if err != nil {
				cancel(err)
				return
			}
			select {
			case requests <- request:
			case <-ctx.Done():
				return
			}
			// Keep observing EOF while analysis runs, without imposing the short
			// idle timeout on a legitimate longer report request.
			deadline = time.Now().Add(controlIOTimeout)
			if request.DeadlineUTC.After(deadline) {
				deadline = minTime(request.DeadlineUTC, time.Now().Add(30*time.Second))
			}
		}
	}()
	connectionError := func() error {
		err := context.Cause(ctx)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
			return nil
		}
		return err
	}
	for {
		var request contracts.Message
		select {
		case <-ctx.Done():
			return connectionError()
		case request = <-requests:
		}

		response, err := api.HandleForClientContext(ctx, request, client)
		if ctx.Err() != nil {
			return connectionError()
		}
		if err != nil {
			return err
		}
		if err := connection.SetWriteDeadline(time.Now().Add(controlIOTimeout)); err != nil {
			return err
		}
		if err := ipc.WriteMessage(connection, response); err != nil {
			return err
		}
	}
}

func minTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}
